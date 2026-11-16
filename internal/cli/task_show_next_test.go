package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// S3-1 (3.3.0): task show --json carries next beside the stored task, never
// in it; with no --me every role's entries, with --me the roles that handle
// holds (the lease holder; the lead by its launch record's role).
func TestTaskShowJSONCarriesNextBesideTheTask(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(added.ID, "seat-a", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	leadDir := filepath.Join(root, "agents", "cto")
	if err := launch.WithRecordLock(leadDir, func() error {
		return launch.WriteUnderRecordLock(leadDir, launch.Record{Handle: "cto", Root: root, Role: status.LeadRole, StartedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	holder := []task.NextEntry{{Verb: "task block", Who: task.NextHolder}, {Verb: "task done", Who: task.NextHolder}, {Verb: "task fail", Who: task.NextHolder}, {Verb: "task release", Who: task.NextHolder}}
	lead := []task.NextEntry{{Verb: "task cancel", Who: task.NextLead}, {Verb: "task release", Who: task.NextLead}}
	for _, c := range []struct {
		name string
		args []string
		want []task.NextEntry
	}{
		{"no --me: every role", []string{"show", added.ID, "--json"}, append(append([]task.NextEntry{}, holder...), lead...)},
		{"--me the holder", []string{"show", added.ID, "--json", "--me", "seat-a"}, holder},
		{"--me the lead", []string{"show", added.ID, "--json", "--me", "cto"}, lead},
		{"--me another seat", []string{"show", added.ID, "--json", "--me", "seat-b"}, []task.NextEntry{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := taskCLI(t, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			var env struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			var next []task.NextEntry
			if err := json.Unmarshal(env.Data["next"], &next); err != nil || next == nil {
				t.Fatalf("data.next = %s (%v)", env.Data["next"], err)
			}
			if !reflect.DeepEqual(next, c.want) {
				t.Fatalf("next = %+v\nwant   %+v", next, c.want)
			}
			var stored map[string]json.RawMessage
			if err := json.Unmarshal(env.Data["task"], &stored); err != nil {
				t.Fatal(err)
			}
			if _, ok := stored["next"]; ok {
				t.Fatal("data.task carries next: the stored record never does")
			}
		})
	}
}
