package task

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandEvidenceLinkCASActorIdempotenceAndConflict(t *testing.T) {
	previousVerify := verifyCommandEvidenceLinkRecord
	verifyCommandEvidenceLinkRecord = func(string, string, string, string, CommandEvidenceLink) error { return nil }
	t.Cleanup(func() { verifyCommandEvidenceLinkRecord = previousVerify })
	project := t.TempDir()
	now := time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC)
	created, err := AddForProfile(project, "review", "s", AddInput{Title: "evidence", AssignTo: "worker"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimForProfile(project, "review", "s", created.ID, "worker", now); err != nil {
		t.Fatal(err)
	}
	digest := taskFileDigest(t, project, "review", "s", created.ID)
	base := filepath.Join(project, ".amq-squad", "evidence", "commands", "review", "s", "tasks", created.ID, "attempts", "attempt-1")
	sha := "sha256:" + strings.Repeat("a", 64)
	link := CommandEvidenceLink{
		AttemptID: "attempt-1", Actor: "worker", Subject: "test", ProcessState: "succeeded", FinalizationState: "complete",
		ManifestPath: filepath.Join(base, "manifest.json"), ManifestSHA256: sha,
		OutcomePath: filepath.Join(base, "outcome.json"), OutcomeSHA256: sha,
		SummaryPath: filepath.Join(base, "summary.json"), SummarySHA256: sha,
	}
	linked, changed, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, digest, "worker", link, now.Add(time.Minute))
	if err != nil || !changed || len(linked.CommandEvidence) != 1 || linked.CommandEvidence[0].LinkedAt.IsZero() {
		t.Fatalf("link result=%+v changed=%t err=%v", linked, changed, err)
	}
	currentDigest := taskFileDigest(t, project, "review", "s", created.ID)
	repeat, changed, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, currentDigest, "worker", link, now.Add(2*time.Minute))
	if err != nil || changed || len(repeat.CommandEvidence) != 1 {
		t.Fatalf("idempotent repeat result=%+v changed=%t err=%v", repeat, changed, err)
	}
	conflict := link
	conflict.SummarySHA256 = "sha256:" + strings.Repeat("b", 64)
	if _, _, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, currentDigest, "worker", conflict, now.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting link accepted: %v", err)
	}
	if _, _, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, currentDigest, "other", link, now.Add(3*time.Minute)); err == nil {
		t.Fatal("wrong actor link accepted")
	}
	if _, _, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, strings.Repeat("0", 64), "worker", link, now.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale task digest accepted: %v", err)
	}
}

func TestCommandEvidenceLinkRejectsEscapedPathBeforeMutation(t *testing.T) {
	project := t.TempDir()
	now := time.Now().UTC()
	created, _ := AddForProfile(project, "review", "s", AddInput{Title: "evidence", AssignTo: "worker"}, now)
	_, _ = ClaimForProfile(project, "review", "s", created.ID, "worker", now)
	digest := taskFileDigest(t, project, "review", "s", created.ID)
	sha := "sha256:" + strings.Repeat("a", 64)
	outside := filepath.Join(t.TempDir(), "record.json")
	link := CommandEvidenceLink{AttemptID: "attempt-1", Actor: "worker", Subject: "test", ProcessState: "succeeded", FinalizationState: "complete", ManifestPath: outside, ManifestSHA256: sha, OutcomePath: outside, OutcomeSHA256: sha, SummaryPath: outside, SummarySHA256: sha}
	if _, _, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, digest, "worker", link, now); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaped evidence path accepted: %v", err)
	}
	got, _ := ShowForProfile(project, "review", "s", created.ID)
	if len(got.CommandEvidence) != 0 {
		t.Fatalf("rejected link mutated task: %+v", got.CommandEvidence)
	}
}

func taskFileDigest(t *testing.T, project, profile, session, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(DirForProfile(project, profile, session), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
