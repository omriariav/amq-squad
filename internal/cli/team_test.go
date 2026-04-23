package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

func TestSplitCSV(t *testing.T) {
	cases := map[string][]string{
		"a,b,c":       {"a", "b", "c"},
		" a , b , c ": {"a", "b", "c"},
		",,a,,":       {"a"},
		"":            {},
		"  ":          {},
	}
	for in, want := range cases {
		got := splitCSV(in)
		if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("splitCSV(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseKV(t *testing.T) {
	got, err := parseKV("qa=codex, pm=codex,cto=claude")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"qa": "codex", "pm": "codex", "cto": "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	empty, err := parseKV("")
	if err != nil || len(empty) != 0 {
		t.Errorf("parseKV(\"\") = %v, %v", empty, err)
	}

	for _, bad := range []string{"nokey", "=noval", "key="} {
		if _, err := parseKV(bad); err == nil {
			t.Errorf("parseKV(%q) expected error", bad)
		}
	}
}

func TestEmitTeamCommandShape(t *testing.T) {
	m := team.Member{
		Role:    "designer",
		Binary:  "claude",
		Handle:  "designer",
		Session: "designer",
	}
	cmd := emitTeamCommand("/home/u/proj", "amq-squad", m)
	for _, want := range []string{
		"cd /home/u/proj",
		"amq-squad launch",
		"--role designer",
		"--session designer",
		"--me designer",
		" claude",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitTeamCommand missing %q in: %s", want, cmd)
		}
	}
}

func TestEmitTeamCommandQuotesPathsWithSpaces(t *testing.T) {
	m := team.Member{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "cpo"}
	cmd := emitTeamCommand("/home/user/my project", "amq-squad", m)
	if !strings.Contains(cmd, "'/home/user/my project'") {
		t.Errorf("project path not quoted: %s", cmd)
	}
}

func TestEmitTeamCommandUsesBinaryPath(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}
	cmd := emitTeamCommand("/p", "/usr/local/bin/amq-squad", m)
	if !strings.Contains(cmd, "/usr/local/bin/amq-squad launch") {
		t.Errorf("expected absolute binary path in: %s", cmd)
	}
}

func TestUniqueMemberCWDs(t *testing.T) {
	home := "/home/u/proj-a"
	members := []team.Member{
		{Role: "cto", CWD: ""},              // inherits home
		{Role: "cpo", CWD: ""},              // inherits home
		{Role: "qa", CWD: "/home/u/proj-b"}, // different project
		{Role: "fullstack", CWD: home},      // explicit but same as home
	}
	got := uniqueMemberCWDs(home, members)
	if len(got) != 2 {
		t.Fatalf("uniqueMemberCWDs = %v, want 2 entries", got)
	}
	if got[0] != "/home/u/proj-a" || got[1] != "/home/u/proj-b" {
		t.Errorf("uniqueMemberCWDs = %v, want [proj-a proj-b]", got)
	}
}

func TestExpandPathTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := map[string]string{
		"~":               home,
		"~/Code/proj":     filepath.Join(home, "Code", "proj"),
		"/already/abs":    "/already/abs",
		"relative/subdir": "", // expect an absolute result; exact value depends on cwd
	}
	for in, want := range cases {
		got, err := expandPath(in)
		if err != nil {
			t.Errorf("expandPath(%q) err: %v", in, err)
			continue
		}
		if !filepath.IsAbs(got) {
			t.Errorf("expandPath(%q) = %q, not absolute", in, got)
		}
		if want != "" && got != want {
			t.Errorf("expandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEffectiveCWDFallback(t *testing.T) {
	m := team.Member{Role: "cto"} // CWD empty
	if got := m.EffectiveCWD("/home/u/proj"); got != "/home/u/proj" {
		t.Errorf("EffectiveCWD empty: got %q, want /home/u/proj", got)
	}
	m.CWD = "/other"
	if got := m.EffectiveCWD("/home/u/proj"); got != "/other" {
		t.Errorf("EffectiveCWD set: got %q, want /other", got)
	}
}
