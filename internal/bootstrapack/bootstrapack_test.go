package bootstrapack

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testExpectation(t *testing.T, required bool, now time.Time) Expectation {
	t.Helper()
	e, err := NewExpectation(required, now)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEvaluateGraceOverdueLegacyAndNotRequired(t *testing.T) {
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	id := Identity{Handle: "cto", Role: "cto", Profile: "review", Session: "issue-396", Root: filepath.Join(dir, "root")}
	e := testExpectation(t, true, now)
	if got := Evaluate(&e, id, dir, now.Add(GracePeriod-time.Second)); got.State != "pending" {
		t.Fatalf("state=%s", got.State)
	}
	if got := Evaluate(&e, id, dir, now.Add(GracePeriod)); got.State != "unverified" {
		t.Fatalf("state=%s", got.State)
	}
	if got := Evaluate(nil, id, dir, now); got.State != "legacy_unknown" {
		t.Fatalf("state=%s", got.State)
	}
	notRequired := testExpectation(t, false, now)
	if got := Evaluate(&notRequired, id, dir, now); got.State != "not_required" {
		t.Fatalf("state=%s", got.State)
	}
}

func TestMarkerStrictPermissionsIdentityAndStaleLaunch(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()
	root := filepath.Join(dir, "custom-root")
	id := Identity{Handle: "qa", Role: "qa", Profile: "named", Session: "s", Root: root}
	e := testExpectation(t, true, now)
	m := Marker{LaunchID: e.LaunchID, PromptVersion: e.PromptVersion, AcknowledgedAt: now, Handle: id.Handle, Role: id.Role, Profile: id.Profile, Session: id.Session, Root: id.Root, SkillVersion: "2.19.0", Steps: append([]string(nil), RequiredSteps...)}
	if err := Write(dir, m); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(Path(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode=%o", dirInfo.Mode().Perm())
	}
	if got := Evaluate(&e, id, dir, now); got.State != "verified" {
		t.Fatalf("state=%s detail=%s", got.State, got.Detail)
	}
	wrongRole := id
	wrongRole.Role = "cto"
	if got := Evaluate(&e, wrongRole, dir, now); got.State != "mismatch" {
		t.Fatalf("role state=%s", got.State)
	}
	m.AcknowledgedAt = e.IssuedAt.Add(-time.Minute)
	if err := Write(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := Evaluate(&e, id, dir, now); got.State != "mismatch" {
		t.Fatalf("timestamp state=%s", got.State)
	}
	m.AcknowledgedAt = now
	if err := Write(dir, m); err != nil {
		t.Fatal(err)
	}
	stale := e
	stale.LaunchID = "rotated"
	if got := Evaluate(&stale, id, dir, now); got.State != "malformed" {
		t.Fatalf("stale state=%s", got.State)
	}
	rotated := testExpectation(t, true, now)
	if got := Evaluate(&rotated, id, dir, now); got.State != "mismatch" {
		t.Fatalf("rotated state=%s", got.State)
	}
	if err := os.Chmod(Path(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Evaluate(&e, id, dir, now); got.State != "malformed" {
		t.Fatalf("permission state=%s", got.State)
	}
	if err := os.Remove(Path(dir)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, Path(dir)); err != nil {
		t.Fatal(err)
	}
	if got := Evaluate(&e, id, dir, now); got.State != "malformed" {
		t.Fatalf("symlink state=%s", got.State)
	}
}

func TestMarkerRejectsMalformedUnknownAndConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"launch_id":"x","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("expected strict parse failure")
	}
	if err := os.WriteFile(path, []byte(`{} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("expected trailing JSON failure")
	}
	now := time.Now().UTC()
	e := testExpectation(t, true, now)
	base := Marker{LaunchID: e.LaunchID, PromptVersion: e.PromptVersion, AcknowledgedAt: now, Handle: "dev", Role: "dev", Session: "s", Root: filepath.Join(dir, "root"), SkillVersion: "2.19.0", Steps: append([]string(nil), RequiredSteps...)}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Write(dir, base); err != nil {
				t.Errorf("write: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := Read(dir); err != nil {
		t.Fatalf("atomic result unreadable: %v", err)
	}
}
