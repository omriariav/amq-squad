package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestParseTmuxClientsReturnsControlModeClients(t *testing.T) {
	got := parseTmuxClients("/dev/ttys001\t1\tattached,control-mode,pause-after=120\n/dev/ttys002\t0\tattached\n")
	if len(got) != 1 {
		t.Fatalf("got %d clients, want 1", len(got))
	}
	if got[0].TTY != "/dev/ttys001" || !strings.Contains(got[0].Flags, "pause-after=120") {
		t.Fatalf("client = %+v", got[0])
	}
}

// filterMembersBySession tests

func TestFilterMembersBySessionReturnsAllWhenUnpinned(t *testing.T) {
	members := []team.Member{
		{Role: "cto", Session: ""},
		{Role: "fullstack", Session: ""},
	}
	active, skipped := filterMembersBySession(members, "v2-3-0")
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2", len(active))
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %d, want 0", len(skipped))
	}
}

func TestFilterMembersBySessionReturnsMatchingAndUnpinned(t *testing.T) {
	members := []team.Member{
		{Role: "go-dev", Session: "v2-3-0"},
		{Role: "architect", Session: ""},
		{Role: "pm-copilot", Session: "pm-copilot"},
	}
	active, skipped := filterMembersBySession(members, "v2-3-0")
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2 (go-dev + architect)", len(active))
	}
	if active[0].Role != "go-dev" || active[1].Role != "architect" {
		t.Fatalf("active roles = %v, want [go-dev architect]", []string{active[0].Role, active[1].Role})
	}
	if len(skipped) != 1 || skipped[0].Role != "pm-copilot" {
		t.Fatalf("skipped = %v, want [pm-copilot]", skipped)
	}
}

func TestFilterMembersBySessionSkipsAllCrossSession(t *testing.T) {
	members := []team.Member{
		{Role: "pm-a", Session: "pm-copilot"},
		{Role: "pm-b", Session: "pm-copilot"},
	}
	active, skipped := filterMembersBySession(members, "v2-3-0")
	if len(active) != 0 {
		t.Fatalf("active = %d, want 0", len(active))
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %d, want 2", len(skipped))
	}
}
