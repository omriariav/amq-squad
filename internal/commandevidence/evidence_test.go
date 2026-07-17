package commandevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_COMMAND_EVIDENCE_HELPER") != "1" {
		return
	}
	mode := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
			break
		}
	}
	switch mode {
	case "binary-large":
		stdout := append([]byte{'o', 0, 'k', '\n'}, bytes.Repeat([]byte("stdout-block\x00"), 180000)...)
		stderr := append([]byte{'e', 0, 'r', 'r', '\n'}, bytes.Repeat([]byte("stderr-block\x00"), 90000)...)
		_, _ = os.Stdout.Write(stdout)
		_, _ = os.Stderr.Write(stderr)
	case "small":
		_, _ = os.Stdout.Write([]byte("small stdout\n"))
		_, _ = os.Stderr.Write([]byte("small stderr\n"))
	case "env":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("COMMAND_EVIDENCE_TEST_VALUE"))
	case "nonzero":
		_, _ = os.Stdout.Write([]byte("before failure\n"))
		_, _ = os.Stderr.Write([]byte("ordinary exit 7\n"))
		os.Exit(7)
	case "noop":
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown helper mode %q", mode)
		os.Exit(97)
	}
	os.Exit(0)
}

func TestRunPreservesBinaryLargeStdoutAndStderrSeparately(t *testing.T) {
	store := newTestStore(t, "review", "evidence")
	req := testRequest(t, store, "attempt-binary", "binary-large")
	result, err := store.Run(req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Process == nil || result.Process.State != "succeeded" || result.Outcome == nil || result.Outcome.FinalizationState != "complete" {
		t.Fatalf("unexpected terminal result: %+v", result)
	}
	wantOut := append([]byte{'o', 0, 'k', '\n'}, bytes.Repeat([]byte("stdout-block\x00"), 180000)...)
	wantErr := append([]byte{'e', 0, 'r', 'r', '\n'}, bytes.Repeat([]byte("stderr-block\x00"), 90000)...)
	assertArtifact(t, result.Outcome.Stdout, wantOut)
	assertArtifact(t, result.Outcome.Stderr, wantErr)
	attemptDir := filepath.Dir(result.ManifestPath)
	for _, name := range []string{stdoutSpool, stderrSpool} {
		if _, err := os.Lstat(filepath.Join(attemptDir, name)); !os.IsNotExist(err) {
			t.Fatalf("committed spool %s was not removed: %v", name, err)
		}
	}
	if info, err := os.Stat(result.SummaryPath); err != nil || info.Size() > SummaryMaxBytes {
		t.Fatalf("summary cap: info=%v err=%v", info, err)
	}
}

func TestNonzeroExitIsProcessTruthNotTransportFailure(t *testing.T) {
	store := newTestStore(t, "review", "nonzero")
	result, err := store.Run(testRequest(t, store, "attempt-nonzero", "nonzero"))
	if err != nil {
		t.Fatalf("ordinary nonzero exit should still finalize evidence: %v", err)
	}
	if result.Process == nil || result.Process.State != "exited_nonzero" || result.Process.ExitCode == nil || *result.Process.ExitCode != 7 {
		t.Fatalf("unexpected nonzero process record: %+v", result.Process)
	}
	if result.Process.TransportError != "" || len(result.Process.CaptureErrors) != 0 {
		t.Fatalf("ordinary exit was misclassified: %+v", result.Process)
	}
	assertArtifact(t, result.Outcome.Stdout, []byte("before failure\n"))
	assertArtifact(t, result.Outcome.Stderr, []byte("ordinary exit 7\n"))
}

func TestExplicitAttemptIdempotenceAndConflict(t *testing.T) {
	store := newTestStore(t, "review", "idempotence")
	req := testRequest(t, store, "attempt-stable", "small")
	first, err := store.Run(req)
	if err != nil {
		t.Fatal(err)
	}
	same := req
	same.StartedAt = req.StartedAt.Add(24 * time.Hour)
	same.TaskSHA256 = digestBytes([]byte("mutable task state changed"))
	second, err := store.Run(same)
	if err != nil {
		t.Fatalf("same immutable request must collapse: %v", err)
	}
	if !second.Existing || second.Manifest.RequestSHA256 != first.Manifest.RequestSHA256 {
		t.Fatalf("idempotent replay did not return existing result: %+v", second)
	}
	conflict := req
	conflict.Subject = "different immutable subject"
	if _, err := store.Run(conflict); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting explicit attempt was not rejected: %v", err)
	}
}

func TestRedactionAndSecretPolicy(t *testing.T) {
	store := newTestStore(t, "review", "redaction")
	secret := "super-secret-value"
	req := testRequest(t, store, "attempt-redacted", "noop", "--token", secret)
	req.Subject = "subject contains " + secret
	req.ArgvEvidence = BuildArguments(req.Argv, map[int]bool{len(req.Argv) - 1: true})
	req.Environment = append(req.Environment, BuildEnvironment(map[string]string{"API_TOKEN": secret}, map[string]bool{"API_TOKEN": true})...)
	result, err := store.Run(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Manifest.Subject, secret) || !strings.Contains(result.Manifest.Subject, "[REDACTED]") {
		t.Fatalf("subject was not scrubbed: %q", result.Manifest.Subject)
	}
	err = filepath.Walk(store.Root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(b, []byte(secret)) {
			return fmt.Errorf("secret leaked into %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	unredacted := testRequest(t, store, "attempt-secret-rejected", "noop")
	unredacted.Environment = append(unredacted.Environment, BuildEnvironment(map[string]string{"API_TOKEN": secret}, nil)...)
	if _, err := store.Run(unredacted); err == nil || !strings.Contains(err.Error(), "must be redacted") {
		t.Fatalf("unredacted secret-class environment was accepted: %v", err)
	}
}

func TestEnvironmentProjectionIsExplicitAndBounded(t *testing.T) {
	if _, err := BuildBaselineEnvironment(map[string]string{"UNAPPROVED_BASELINE": "x"}); err == nil {
		t.Fatal("invalid baseline environment name accepted")
	}
	store := newTestStore(t, "review", "environment")
	req := testRequest(t, store, "attempt-env", "env")
	req.Environment = append(req.Environment, BuildEnvironment(map[string]string{"COMMAND_EVIDENCE_TEST_VALUE": "projected"}, nil)...)
	result, err := store.Run(req)
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, result.Outcome.Stdout, []byte("projected"))
	tooLarge := testRequest(t, store, "attempt-env-large", "noop")
	tooLarge.Environment = append(tooLarge.Environment, BuildEnvironment(map[string]string{"TOO_LARGE": strings.Repeat("x", maxEnvValueBytes+1)}, nil)...)
	if _, err := store.Run(tooLarge); err == nil {
		t.Fatal("oversized environment value accepted")
	}
}

func TestExecutableBytesVerifiedBeforeReservation(t *testing.T) {
	store := newTestStore(t, "review", "executable")
	req := testRequest(t, store, "attempt-bad-executable", "noop")
	req.ExecutableSHA256 = "sha256:" + strings.Repeat("0", 64)
	if _, err := store.Run(req); err == nil || !strings.Contains(err.Error(), "digest does not match") {
		t.Fatalf("mismatched executable bytes were accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "tasks", req.TaskID, "attempts", req.AttemptID)); !os.IsNotExist(err) {
		t.Fatalf("reservation occurred before executable verification: %v", err)
	}
}

func TestRetryIsDistinctAndBoundToSameTask(t *testing.T) {
	store := newTestStore(t, "review", "retry")
	parentReq := testRequest(t, store, "attempt-parent", "small")
	if _, err := store.Run(parentReq); err != nil {
		t.Fatal(err)
	}
	retry := testRequest(t, store, "attempt-retry", "small")
	retry.RetryOf = parentReq.AttemptID
	result, err := store.Run(retry)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.RetryOf != parentReq.AttemptID || result.Manifest.AttemptID == result.Manifest.RetryOf {
		t.Fatalf("retry identity not preserved: %+v", result.Manifest)
	}
	self := testRequest(t, store, "attempt-self", "noop")
	self.RetryOf = self.AttemptID
	if _, err := store.Run(self); err == nil {
		t.Fatal("self retry was accepted")
	}
}

func TestRecoveryNeverRerunsAndCanFinalizeDurableProcess(t *testing.T) {
	store := newTestStore(t, "review", "recover")
	missingReq := testRequest(t, store, "attempt-no-process", "nonzero")
	missingPlan, err := store.buildPlan(missingReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.reserve(missingPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Recover(missingReq.TaskID, missingReq.AttemptID, time.Now()); err == nil || !strings.Contains(err.Error(), "no-rerun") {
		t.Fatalf("missing-process recovery did not refuse rerun: %v", err)
	}
	missing, err := store.Read(missingReq.TaskID, missingReq.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Process != nil || missing.Outcome != nil || len(missing.Findings) != 1 || missing.Findings[0].Kind != "process_outcome_unknown" {
		t.Fatalf("unexpected no-rerun finding: %+v", missing)
	}

	durableReq := testRequest(t, store, "attempt-durable-process", "nonzero")
	plan, err := store.buildPlan(durableReq)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.reserve(plan)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.openAttempt(durableReq.TaskID, durableReq.AttemptID, false)
	if err != nil {
		t.Fatal(err)
	}
	writeSpool(t, attempt, stdoutSpool, []byte("durable stdout"))
	writeSpool(t, attempt, stderrSpool, []byte("durable stderr"))
	code := 23
	process := ProcessRecord{SchemaVersion: SchemaVersion, AttemptID: durableReq.AttemptID, RequestSHA256: reserved.Manifest.RequestSHA256, State: "exited_nonzero", EndedAt: time.Now().UTC(), ElapsedNanos: 1, ExitCode: &code}
	process.RecordSHA256 = digestJSON(processWithoutDigest(process))
	if err := writeImmutableRoot(attempt, "process.json", process, recordMaxBytes); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Recover(durableReq.TaskID, durableReq.AttemptID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Process == nil || recovered.Process.ExitCode == nil || *recovered.Process.ExitCode != 23 || recovered.Outcome == nil || recovered.Outcome.FinalizationState != "complete" {
		t.Fatalf("durable process was not finalized exactly: %+v", recovered)
	}
}

func TestArtifactCorruptionIsDetectedAndNeverOverwritten(t *testing.T) {
	store := newTestStore(t, "review", "corruption")
	first, err := store.Run(testRequest(t, store, "attempt-object-one", "small"))
	if err != nil {
		t.Fatal(err)
	}
	ref := first.Outcome.Stdout
	corrupt := bytes.Repeat([]byte{'x'}, int(ref.Size))
	if err := os.WriteFile(ref.Path, corrupt, fileMode); err != nil {
		t.Fatal(err)
	}
	second, err := store.Run(testRequest(t, store, "attempt-object-two", "small"))
	if err == nil || second.Outcome == nil || second.Outcome.FinalizationState != "artifact_incomplete" {
		t.Fatalf("corrupt existing object was not surfaced: result=%+v err=%v", second, err)
	}
	got, readErr := os.ReadFile(ref.Path)
	if readErr != nil || !bytes.Equal(got, corrupt) {
		t.Fatalf("corrupt object was overwritten: got=%q err=%v", got, readErr)
	}
}

func TestSymlinkRecordsAndAncestorsAreRejected(t *testing.T) {
	store := newTestStore(t, "review", "symlink")
	req := testRequest(t, store, "attempt-symlink", "noop")
	plan, err := store.buildPlan(req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.reserve(plan)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "manifest.json")
	b, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, b, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(result.ManifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, result.ManifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(req.TaskID, req.AttemptID); err == nil || !strings.Contains(err.Error(), "no-symlink") {
		t.Fatalf("symlinked record was accepted: %v", err)
	}
}

func TestArtifactFailureRetainsSpoolsAndCannotEscapeProject(t *testing.T) {
	store := newTestStore(t, "review", "retention")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store.Root, "objects"), dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store.Root, "objects", "sha256")); err != nil {
		t.Fatal(err)
	}
	result, err := store.Run(testRequest(t, store, "attempt-retained", "small"))
	if err == nil || result.Outcome == nil || result.Outcome.FinalizationState != "artifact_incomplete" {
		t.Fatalf("artifact failure was not durable: result=%+v err=%v", result, err)
	}
	for _, name := range []string{stdoutSpool, stderrSpool} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(result.ManifestPath), name)); err != nil {
			t.Fatalf("incomplete spool %s not retained: %v", name, err)
		}
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("evidence escaped project through symlink: entries=%v err=%v", entries, err)
	}
}

func TestSemanticStateTamperRejectedEvenWithRecomputedDigest(t *testing.T) {
	store := newTestStore(t, "review", "state-tamper")
	result, err := store.Run(testRequest(t, store, "attempt-state", "small"))
	if err != nil {
		t.Fatal(err)
	}
	processPath := filepath.Join(filepath.Dir(result.ManifestPath), "process.json")
	process := *result.Process
	bad := 9
	process.State = "succeeded"
	process.ExitCode = &bad
	process.RecordSHA256 = digestJSON(processWithoutDigest(process))
	writeJSONForTamper(t, processPath, process)
	if _, err := store.Read(result.Manifest.TaskID, result.Manifest.AttemptID); err == nil || !strings.Contains(err.Error(), "successful process state is inconsistent") {
		t.Fatalf("semantic state tamper was accepted: %v", err)
	}
}

func TestSummaryRecomputedDigestCannotBreakExactBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Summary)
	}{
		{"subject", func(s *Summary) { s.Subject = "other subject" }},
		{"manifest path", func(s *Summary) { s.ManifestPath += ".other" }},
		{"manifest sha", func(s *Summary) { s.ManifestSHA256 = digestBytes([]byte("other manifest")) }},
		{"outcome path", func(s *Summary) { s.OutcomePath += ".other" }},
		{"full artifact ref", func(s *Summary) {
			ref := *s.Stdout
			ref.Path += ".other"
			ref.Size++
			ref.MediaType = "text/plain"
			s.Stdout = &ref
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t, "review", "summary-"+strings.ReplaceAll(tt.name, " ", "-"))
			result, err := store.Run(testRequest(t, store, "attempt-summary", "small"))
			if err != nil {
				t.Fatal(err)
			}
			summary := *result.Summary
			tt.mutate(&summary)
			summary.SummarySHA256 = digestJSON(summaryWithoutDigest(summary))
			writeJSONForTamper(t, result.SummaryPath, summary)
			if _, err := store.Read(result.Manifest.TaskID, result.Manifest.AttemptID); err == nil {
				t.Fatalf("recomputed summary tamper %q was accepted", tt.name)
			}
		})
	}
}

func TestManifestRecomputedDigestCannotBreakProjectAndRevisionBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"cwd outside project", func(m *Manifest) { m.CWD = filepath.Dir(filepath.Dir(m.CWD)) }},
		{"invalid task digest", func(m *Manifest) { m.TaskSHA256 = "sha256:not-canonical" }},
		{"invalid git head", func(m *Manifest) { m.GitHead = strings.Repeat("G", 40) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t, "review", "manifest-"+strings.ReplaceAll(tt.name, " ", "-"))
			result, err := store.Run(testRequest(t, store, "attempt-manifest", "small"))
			if err != nil {
				t.Fatal(err)
			}
			manifest := result.Manifest
			tt.mutate(&manifest)
			manifest.RequestSHA256 = requestDigest(manifest)
			writeJSONForTamper(t, result.ManifestPath, manifest)
			if _, err := store.Read(result.Manifest.TaskID, result.Manifest.AttemptID); err == nil || !strings.Contains(err.Error(), "binding") && !strings.Contains(err.Error(), "lifecycle") {
				t.Fatalf("recomputed manifest tamper %q was accepted or misclassified: %v", tt.name, err)
			}
		})
	}
}

func TestRecoveryFindingRecomputedDigestShapeOrderAndBounds(t *testing.T) {
	t.Run("shape", func(t *testing.T) {
		store := newTestStore(t, "review", "finding-shape")
		req := testRequest(t, store, "attempt-finding-shape", "noop")
		plan, err := store.buildPlan(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.reserve(plan); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Recover(req.TaskID, req.AttemptID, time.Now().UTC()); err == nil {
			t.Fatal("missing-process recovery unexpectedly succeeded")
		}
		attemptDir := filepath.Join(store.Root, "tasks", req.TaskID, "attempts", req.AttemptID)
		entries, err := os.ReadDir(attemptDir)
		if err != nil {
			t.Fatal(err)
		}
		var findingPath string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "recovery-") {
				findingPath = filepath.Join(attemptDir, entry.Name())
				break
			}
		}
		if findingPath == "" {
			t.Fatal("recovery finding not written")
		}
		b, err := os.ReadFile(findingPath)
		if err != nil {
			t.Fatal(err)
		}
		var finding RecoveryFinding
		if err := json.Unmarshal(b, &finding); err != nil {
			t.Fatal(err)
		}
		finding.Kind = "invented_terminal_truth"
		finding.Detail = ""
		finding.FindingSHA256 = digestJSON(findingWithoutDigest(finding))
		writeJSONForTamper(t, findingPath, finding)
		if _, err := store.Read(req.TaskID, req.AttemptID); err == nil || !strings.Contains(err.Error(), "finding") {
			t.Fatalf("recomputed finding shape tamper was accepted: %v", err)
		}
	})

	t.Run("deterministic tie order", func(t *testing.T) {
		store := newTestStore(t, "review", "finding-order")
		req := testRequest(t, store, "attempt-finding-order", "noop")
		plan, err := store.buildPlan(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.reserve(plan); err != nil {
			t.Fatal(err)
		}
		attempt, err := store.openAttempt(req.TaskID, req.AttemptID, false)
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 7, 16, 1, 0, 0, 123, time.UTC)
		if _, err := store.appendFinding(attempt, req.AttemptID, at, "terminal_outcome_verified", "second semantic kind"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.appendFinding(attempt, req.AttemptID, at, "process_outcome_unknown", "first semantic kind"); err != nil {
			t.Fatal(err)
		}
		attempt.Close()
		result, err := store.Read(req.TaskID, req.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Findings) != 2 || result.Findings[0].FindingSHA256 >= result.Findings[1].FindingSHA256 {
			t.Fatalf("same-time findings are not deterministically digest ordered: %+v", result.Findings)
		}
	})

	t.Run("bounded count", func(t *testing.T) {
		store := newTestStore(t, "review", "finding-count")
		req := testRequest(t, store, "attempt-finding-count", "noop")
		plan, err := store.buildPlan(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.reserve(plan); err != nil {
			t.Fatal(err)
		}
		attempt, err := store.openAttempt(req.TaskID, req.AttemptID, false)
		if err != nil {
			t.Fatal(err)
		}
		base := time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC)
		for i := 0; i < maxFindings+1; i++ {
			at := base.Add(time.Duration(i) * time.Nanosecond)
			if _, err := store.appendFinding(attempt, req.AttemptID, at, "process_outcome_unknown", fmt.Sprintf("finding %d", i)); err != nil {
				t.Fatal(err)
			}
		}
		attempt.Close()
		if _, err := store.Read(req.TaskID, req.AttemptID); err == nil || !strings.Contains(err.Error(), "findings exceed") {
			t.Fatalf("finding count cap was not enforced: %v", err)
		}
	})
}

func TestNamedProfileIsolationAndMailboxBoundary(t *testing.T) {
	project := t.TempDir()
	named, err := NewStore(project, "review", "same-session")
	if err != nil {
		t.Fatal(err)
	}
	def, err := NewStore(project, "default", "same-session")
	if err != nil {
		t.Fatal(err)
	}
	if named.Root == def.Root {
		t.Fatal("named and default evidence roots collided")
	}
	if _, err := named.Run(testRequest(t, named, "attempt-named", "small")); err != nil {
		t.Fatal(err)
	}
	if _, err := def.Run(testRequest(t, def, "attempt-default", "small")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".agent-mail")); !os.IsNotExist(err) {
		t.Fatalf("command evidence mutated mailbox namespace: %v", err)
	}
	if _, err := named.Read("t2", "attempt-default"); !os.IsNotExist(err) {
		t.Fatalf("named profile could read default evidence: %v", err)
	}
}

func TestImmutableOversizeFailureCreatesNoPartialRecord(t *testing.T) {
	store := newTestStore(t, "review", "immutable")
	root, err := store.openRelative("scratch", true)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeImmutableRoot(root, "oversize.json", strings.Repeat("x", 1024), 32); err == nil {
		t.Fatal("oversized immutable record accepted")
	}
	if _, err := root.Lstat("oversize.json"); !os.IsNotExist(err) {
		t.Fatalf("oversized record left partial file: %v", err)
	}
}

func newTestStore(t *testing.T, profile, session string) Store {
	t.Helper()
	store, err := NewStore(t.TempDir(), profile, session)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testRequest(t *testing.T, store Store, attemptID, mode string, extra ...string) Request {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{executable, "-test.run=^TestHelperProcess$", "--", mode}
	argv = append(argv, extra...)
	return Request{
		ProjectDir:       store.ProjectDir,
		Profile:          store.Profile,
		Session:          store.Session,
		NamespaceID:      store.Namespace.ID,
		TaskID:           "t2",
		TaskPath:         filepath.Join(store.Namespace.Paths.Tasks, "t2.json"),
		TaskSHA256:       digestBytes([]byte("task snapshot")),
		Actor:            "platform-dev",
		Subject:          "package evidence test",
		Argv:             argv,
		ArgvEvidence:     BuildArguments(argv, nil),
		Executable:       executable,
		ExecutableSHA256: digestBytes(b),
		CWD:              store.ProjectDir,
		Environment:      BuildEnvironment(map[string]string{"GO_WANT_COMMAND_EVIDENCE_HELPER": "1"}, nil),
		StartedAt:        time.Now().UTC(),
		Seed:             "exact-seed",
		GitHead:          strings.Repeat("a", 40),
		AttemptID:        attemptID,
	}
}

func assertArtifact(t *testing.T, ref *ArtifactRef, want []byte) {
	t.Helper()
	if err := validateArtifactRef(ref); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("artifact mismatch: got %d bytes want %d", len(got), len(want))
	}
	if ref.Size != int64(len(want)) || ref.SHA256 != digestBytes(want) {
		t.Fatalf("artifact identity mismatch: %+v", ref)
	}
}

func writeSpool(t *testing.T, root *os.Root, name string, content []byte) {
	t.Helper()
	f, err := createExclusive(root, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeJSONForTamper(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, fileMode); err != nil {
		t.Fatal(err)
	}
}

func TestErrorIdentityHelpers(t *testing.T) {
	if !errors.Is(errors.Join(os.ErrExist), os.ErrExist) {
		t.Fatal("joined immutable errors must preserve errors.Is identity")
	}
}
