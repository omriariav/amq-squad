package piruntime

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestTeamIdentityPinsThePiLeadToTheSquadSession(t *testing.T) {
	base := filepath.Join(t.TempDir(), "mail")
	id, err := NewTeamIdentity("cto", base, "v31")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"AM_ME":        "cto",
		"AM_ROOT":      filepath.Join(base, "v31"),
		"AM_BASE_ROOT": base,
		"AM_SESSION":   "v31",
	}
	if got := id.Overlay(); !reflect.DeepEqual(got, want) {
		t.Fatalf("overlay = %#v, want %#v", got, want)
	}
	parent := []string{
		"AM_ME=solo",
		"AM_ROOT=/wrong/solo",
		"AM_BASE_ROOT=/wrong",
		"AM_SESSION=solo",
		"PATH=/bin",
	}
	env, _ := ScrubEnv(parent, nil, id.Overlay())
	got := envMap(env)
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("%s = %q, want %q in %v", name, got[name], value, env)
		}
	}
	if got["PATH"] != "/bin" {
		t.Fatalf("unrelated parent environment was lost: %v", env)
	}
}

func TestTeamIdentityMakesTheSquadTaskStoreVisible(t *testing.T) {
	base := filepath.Join(t.TempDir(), "mail")
	id, err := NewTeamIdentity("cto", base, "v31")
	if err != nil {
		t.Fatal(err)
	}
	store, err := task.Open(id.Root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.Add(task.AddInput{Title: "squad task", Class: "plan"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	env, _ := ScrubEnv([]string{"AM_ROOT=/wrong/solo", "AM_SESSION=solo"}, nil, id.Overlay())
	visible, err := task.Open(envMap(env)["AM_ROOT"])
	if err != nil {
		t.Fatal(err)
	}
	got, err := visible.Get(added.ID)
	if err != nil || got.Title != added.Title {
		t.Fatalf("team lead task lookup = %+v, %v, want %s", got, err, added.ID)
	}
}

func TestTeamIdentityRefusesPartialOrUnsafePins(t *testing.T) {
	base := filepath.Join(t.TempDir(), "mail")
	for _, tc := range []struct {
		name, handle, root, session string
	}{
		{name: "bad handle", handle: "Lead One", root: base, session: "v31"},
		{name: "relative root", handle: "lead", root: "mail", session: "v31"},
		{name: "unclean root", handle: "lead", root: base + string(filepath.Separator) + ".." + string(filepath.Separator) + "mail", session: "v31"},
		{name: "bad session", handle: "lead", root: base, session: "../v31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewTeamIdentity(tc.handle, tc.root, tc.session); err == nil {
				t.Fatal("unsafe identity was accepted")
			}
		})
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		for i := range kv {
			if kv[i] == '=' {
				out[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return out
}
