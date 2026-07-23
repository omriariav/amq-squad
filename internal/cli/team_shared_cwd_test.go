package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTeamInitAcceptsSharedCwdExceptionFlag(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := runTeamInit([]string{"--personas", "cto,qa", "--shared-cwd-exception", "qa is read-only this slice"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.SharedCwdException != "qa is read-only this slice" {
		t.Fatalf("SharedCwdException = %q", got.SharedCwdException)
	}
}

func TestTeamSharedCwdExceptionSetShowClear(t *testing.T) {
	dir := seedTeamWithoutSharedCwdException(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})

	out, _, err := captureOutput(t, func() error {
		return runTeam([]string{"shared-cwd-exception", "show"})
	})
	if err != nil {
		t.Fatalf("show (none): %v", err)
	}
	if !strings.Contains(out, "(none)") {
		t.Fatalf("expected no exception recorded, got %q", out)
	}

	if err := runTeam([]string{"shared-cwd-exception", "set", "hotspot serialization"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.SharedCwdException != "hotspot serialization" {
		t.Fatalf("SharedCwdException = %q", got.SharedCwdException)
	}

	out, _, err = captureOutput(t, func() error {
		return runTeam([]string{"shared-cwd-exception", "show"})
	})
	if err != nil {
		t.Fatalf("show (set): %v", err)
	}
	if !strings.Contains(out, "hotspot serialization") {
		t.Fatalf("expected recorded reason in output, got %q", out)
	}

	if err := runTeam([]string{"shared-cwd-exception", "clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.SharedCwdException != "" {
		t.Fatalf("expected cleared exception, got %q", got.SharedCwdException)
	}
}

func TestTeamSharedCwdExceptionSetRequiresReason(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	if err := runTeam([]string{"shared-cwd-exception", "set", ""}); err == nil {
		t.Fatal("expected an error for an empty reason")
	}
}
