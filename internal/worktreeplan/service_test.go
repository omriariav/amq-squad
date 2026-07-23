package worktreeplan

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestMaterializeHandoffAndCleanupLifecycle(t *testing.T) {
	repo := newTestRepo(t)
	configured := writeTestTeam(t, repo, team.DefaultProfile, "worker")
	service := newTestService(t, configured, team.DefaultProfile, "release-24")
	req := Request{Role: "worker", TaskID: "t3", Base: "HEAD", Scope: []string{"internal/runtime/**"}, AMQRoot: filepath.Join(repo, ".agent-mail", "release-24")}

	preview, planned, err := service.Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	if planned.State != StatePlanned || preview.AcceptedBaseSHA == "" {
		t.Fatalf("preview = %+v / %+v", preview, planned)
	}
	if _, exists, err := Read(configured, team.DefaultProfile, "release-24"); err != nil || exists {
		t.Fatalf("plan must be read-only: exists=%t err=%v", exists, err)
	}

	set, materialized, err := service.Materialize(req)
	if err != nil {
		t.Fatal(err)
	}
	if materialized.State != StateMaterialized || set.AcceptedBaseSHA != materialized.BaseSHA {
		t.Fatalf("materialized = %+v set=%+v", materialized, set)
	}
	current, err := team.ReadProfile(repo, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Members[0].CWD; got != materialized.Path {
		t.Fatalf("member cwd = %q, want %q", got, materialized.Path)
	}
	if _, err := service.Activate("worker"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(materialized.Path, "runtime.txt"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, materialized.Path, "add", "runtime.txt")
	runGit(t, materialized.Path, "commit", "-m", "runtime handoff")
	head := runGit(t, materialized.Path, "rev-parse", "HEAD")
	handoff, err := service.Handoff("worker", head)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.State != StateHandoff || handoff.HandoffSHA != head {
		t.Fatalf("handoff = %+v", handoff)
	}
	inspection, err := service.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	status := memberStatus(t, inspection, "worker")
	if !status.HandoffValid || status.Dirty || status.Drifted || !status.Registered {
		t.Fatalf("handoff status = %+v", status)
	}

	cleaned, err := service.Cleanup(CleanupRequest{Role: "worker", Decision: "accepted"})
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.State != StateCleaned || cleaned.HandoffSHA != "" || pathExists(materialized.Path) {
		t.Fatalf("cleaned = %+v path-exists=%t", cleaned, pathExists(materialized.Path))
	}
	current, err = team.ReadProfile(repo, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Members[0].CWD; got != "" {
		t.Fatalf("member cwd was not restored: %q", got)
	}
	again, err := service.Cleanup(CleanupRequest{Role: "worker", Decision: "accepted"})
	if err != nil || again.State != StateCleaned {
		t.Fatalf("idempotent cleanup = %+v err=%v", again, err)
	}
}

func TestPlanPreviewIsDeterministicAndTimestampFree(t *testing.T) {
	repo := newTestRepo(t)
	configured := writeTestTeam(t, repo, team.DefaultProfile, "worker")
	tick := 0
	service, err := NewService(configured, team.DefaultProfile, "deterministic", ExecGit{}, func() time.Time {
		tick++
		return time.Date(2026, 7, 23, 10, 0, tick, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Role: "worker", TaskID: "t1", Base: "HEAD", Scope: []string{"b/**", "a/**"}, AMQRoot: filepath.Join(repo, ".agent-mail", "deterministic")}
	firstSet, firstRecord, err := service.Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	secondSet, secondRecord, err := service.Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	first, err := json.Marshal(struct {
		Set    Set
		Record Record
	}{firstSet, firstRecord})
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(struct {
		Set    Set
		Record Record
	}{secondSet, secondRecord})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("plan preview is nondeterministic:\n%s\n%s", first, second)
	}
	if strings.Contains(string(first), "created_at") || strings.Contains(string(first), "updated_at") {
		t.Fatalf("read-only preview invented timestamps: %s", first)
	}
}

func TestMaterializeRefusesOccupiedPathAndBranchWithoutCreatingStore(t *testing.T) {
	t.Run("path", func(t *testing.T) {
		repo := newTestRepo(t)
		configured := writeTestTeam(t, repo, team.DefaultProfile, "worker")
		service := newTestService(t, configured, team.DefaultProfile, "collision")
		req := Request{
			Role: "worker", TaskID: "t3", Base: "HEAD", Scope: []string{"internal/**"},
			Path: filepath.Join(t.TempDir(), "occupied"), AMQRoot: filepath.Join(repo, ".agent-mail", "collision"),
		}
		if err := os.MkdirAll(req.Path, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.Materialize(req); err == nil || !strings.Contains(err.Error(), "occupied unknown") {
			t.Fatalf("error = %v", err)
		}
		if _, exists, err := Read(configured, team.DefaultProfile, "collision"); err != nil || exists {
			t.Fatalf("refusal mutated store: exists=%t err=%v", exists, err)
		}
	})

	t.Run("branch", func(t *testing.T) {
		repo := newTestRepo(t)
		configured := writeTestTeam(t, repo, team.DefaultProfile, "worker")
		service := newTestService(t, configured, team.DefaultProfile, "collision")
		req := Request{Role: "worker", TaskID: "t3", Base: "HEAD", Scope: []string{"internal/**"}, AMQRoot: filepath.Join(repo, ".agent-mail", "collision")}
		_, preview, err := service.Plan(req)
		if err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "branch", preview.Branch, "HEAD")
		if _, _, err := service.Materialize(req); err == nil || !strings.Contains(err.Error(), "existing branch") {
			t.Fatalf("error = %v", err)
		}
		if _, exists, err := Read(configured, team.DefaultProfile, "collision"); err != nil || exists {
			t.Fatalf("refusal mutated store: exists=%t err=%v", exists, err)
		}
	})
}

func TestPlannedBranchCrashRecoveryAndCleanupRefusals(t *testing.T) {
	repo := newTestRepo(t)
	configured := writeTestTeam(t, repo, "named", "worker")
	service := newTestService(t, configured, "named", "recovery")
	req := Request{Role: "worker", TaskID: "t9", Base: "HEAD", Scope: []string{"cmd/**"}, AMQRoot: filepath.Join(repo, ".agent-mail", "named", "recovery")}
	set, planned, err := service.Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := WithLock(configured, "named", "recovery", func() error {
		return WriteUnderLock(configured, "named", "recovery", set)
	}); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "branch", planned.Branch, planned.BaseSHA)

	_, materialized, err := service.Materialize(req)
	if err != nil {
		t.Fatal(err)
	}
	if materialized.State != StateMaterialized {
		t.Fatalf("recovered state = %s", materialized.State)
	}
	if err := os.WriteFile(filepath.Join(materialized.Path, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cleanup(CleanupRequest{Role: "worker", Decision: "rejected"}); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty cleanup error = %v", err)
	}
	if err := os.Remove(filepath.Join(materialized.Path, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "worktree", "remove", materialized.Path)
	if err := os.MkdirAll(materialized.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cleanup(CleanupRequest{Role: "worker", Decision: "rejected"}); err == nil || !strings.Contains(err.Error(), "unknown path") {
		t.Fatalf("unknown-path cleanup error = %v", err)
	}
	if !pathExists(materialized.Path) {
		t.Fatal("cleanup removed an unknown directory")
	}
}

func TestInspectionDiagnosesSharedIndexAndCoordinationDivergence(t *testing.T) {
	repo := newTestRepo(t)
	configured := writeTestTeam(t, repo, team.DefaultProfile, "one", "two")
	service := newTestService(t, configured, team.DefaultProfile, "diagnostics")
	inspection, err := service.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	collision := diagnostic(t, inspection, "shared-index-collision")
	if collision.Status != DiagnosticFail || !strings.Contains(collision.Detail, "one,two") {
		t.Fatalf("collision = %+v", collision)
	}
	if _, err := service.SetSharedCWDException(true, "serialized shared hotspot"); err != nil {
		t.Fatal(err)
	}
	inspection, err = service.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if got := diagnostic(t, inspection, "shared-index-collision"); got.Status != DiagnosticOK {
		t.Fatalf("exception did not clear collision: %+v", got)
	}

	req := Request{Role: "one", TaskID: "t1", Base: "HEAD", Scope: []string{"one/**"}, AMQRoot: filepath.Join(repo, ".agent-mail", "diagnostics")}
	_, record, err := service.Materialize(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(record.Path, ".agent-mail"), 0o755); err != nil {
		t.Fatal(err)
	}
	inspection, err = service.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if got := diagnostic(t, inspection, "coordination-root-divergence"); got.Status != DiagnosticFail || !strings.Contains(got.Detail, "one") {
		t.Fatalf("coordination diagnostic = %+v", got)
	}
}

func TestAcceptedBaseDriftAndHandoffDrift(t *testing.T) {
	repo := newTestRepo(t)
	configured := writeTestTeam(t, repo, team.DefaultProfile, "worker")
	service := newTestService(t, configured, team.DefaultProfile, "drift")
	req := Request{Role: "worker", TaskID: "t1", Base: "HEAD", Scope: []string{"runtime/**"}, AMQRoot: filepath.Join(repo, ".agent-mail", "drift")}
	_, record, err := service.Materialize(req)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := service.Handoff("worker", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Path, "after.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, record.Path, "add", "after.txt")
	runGit(t, record.Path, "commit", "-m", "after handoff")
	inspection, err := service.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	status := memberStatus(t, inspection, "worker")
	if status.HandoffValid || status.CurrentHEAD == handoff.HandoffSHA {
		t.Fatalf("handoff drift not detected: %+v", status)
	}

	if err := os.WriteFile(filepath.Join(repo, "next.txt"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "next.txt")
	runGit(t, repo, "commit", "-m", "new base")
	req.Base = "HEAD"
	if _, _, err := service.Materialize(req); err == nil || !strings.Contains(err.Error(), "accepted base drift") {
		t.Fatalf("accepted base error = %v", err)
	}
}

func TestLinkedWorktreeServiceReanchorsCanonicalTeamHome(t *testing.T) {
	repo := newTestRepo(t)
	configured := writeTestTeam(t, repo, team.DefaultProfile, "worker")
	runGit(t, repo, "add", team.ProfilePath(repo, team.DefaultProfile))
	runGit(t, repo, "commit", "-m", "track team profile")
	configured, err := team.ReadProfile(repo, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, configured, team.DefaultProfile, "linked")
	req := Request{Role: "worker", TaskID: "t1", Base: "HEAD", Scope: []string{"runtime/**"}, AMQRoot: filepath.Join(repo, ".agent-mail", "linked")}
	_, record, err := service.Materialize(req)
	if err != nil {
		t.Fatal(err)
	}
	linkedProfile, err := team.ReadProfile(record.Path, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	linkedService := newTestService(t, linkedProfile, team.DefaultProfile, "linked")
	if linkedService.team.Project != repo {
		t.Fatalf("service team-home = %q, want canonical %q", linkedService.team.Project, repo)
	}
	if got := StorePath(linkedService.team, team.DefaultProfile, "linked"); !strings.HasPrefix(got, filepath.Join(repo, team.DirName, DirName)) {
		t.Fatalf("store path = %q, want canonical root", got)
	}
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "base")
	return repo
}

func writeTestTeam(t *testing.T, repo, profile string, roles ...string) team.Team {
	t.Helper()
	members := make([]team.Member, 0, len(roles))
	for _, role := range roles {
		members = append(members, team.Member{
			Role: role, Handle: role, Binary: "codex", Session: "test",
			ActorMode: team.ActorModeImplementation,
		})
	}
	configured := team.Team{
		Schema: team.SchemaVersion, Members: members,
		ControlRoot: repo, TargetProjectRoot: repo,
	}
	operator := team.DisabledOperator()
	configured.Operator = &operator
	if err := team.WriteProfile(repo, profile, configured); err != nil {
		t.Fatal(err)
	}
	read, err := team.ReadProfile(repo, profile)
	if err != nil {
		t.Fatal(err)
	}
	return read
}

func newTestService(t *testing.T, configured team.Team, profile, session string) *Service {
	t.Helper()
	service, err := NewService(configured, profile, session, ExecGit{}, func() time.Time {
		return time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func memberStatus(t *testing.T, inspection Inspection, role string) MemberStatus {
	t.Helper()
	for _, status := range inspection.Members {
		if status.Role == role {
			return status
		}
	}
	t.Fatalf("missing member status for %s: %+v", role, inspection.Members)
	return MemberStatus{}
}

func diagnostic(t *testing.T, inspection Inspection, kind string) Diagnostic {
	t.Helper()
	for _, check := range inspection.Diagnostics {
		if check.Kind == kind {
			return check
		}
	}
	t.Fatalf("missing diagnostic %s: %+v", kind, inspection.Diagnostics)
	return Diagnostic{}
}
