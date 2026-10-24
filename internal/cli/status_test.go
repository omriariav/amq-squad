package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func statusProbe(alive map[int]bool, match map[int]bool, now time.Time) duplicateLaunchProbe {
	return duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return match[pid] },
		Now:          func() time.Time { return now },
	}
}

func writeStatusPresence(t *testing.T, base, workstream, handle string, pres presenceFile) {
	t.Helper()
	agentDir := filepath.Join(base, workstream, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, marshalErr := json.Marshal(pres)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := os.WriteFile(presencePath(agentDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSelectStatusLaunchRecordRequiresExactHandleIdentity(t *testing.T) {
	tm := team.Team{Project: "/repo"}
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	entries := []launch.Entry{{
		AgentDir: "/repo/.agent-mail/work/agents/outsider",
		Record: launch.Record{
			TeamHome: "/repo", TeamProfile: squadnamespace.DefaultProfile, Session: "work",
			Role: "dev", Handle: "outsider", Binary: "codex",
			Root: "/repo/.agent-mail/work",
		},
	}}
	selection := selectStatusLaunchRecord(tm, squadnamespace.DefaultProfile, member, "work", duplicateLaunchProbe{}, entries)
	if selection.Found || len(selection.DuplicatePaths) != 0 {
		t.Fatalf("wrong-handle same-role record was selected: %+v", selection)
	}
}
