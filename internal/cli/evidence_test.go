package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/commandevidence"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

func TestEvidenceCLIHelper(t *testing.T) {
	if os.Getenv("EVIDENCE_CLI_HELPER") != "1" {
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
	case "small":
		_, _ = os.Stdout.Write([]byte{'o', 0, 'k', '\n'})
		_, _ = os.Stderr.Write([]byte("stderr\n"))
	case "nonzero":
		os.Exit(7)
	default:
		os.Exit(97)
	}
	os.Exit(0)
}

func TestEvidenceRunAuthorityLinkAndExistingBeforeInactiveRejection(t *testing.T) {
	project, task := seedEvidenceTask(t, false)
	args := evidenceRunArgs(t, project, task.ID, "attempt-stable", "subject one", true)
	out, _, err := captureOutput(t, func() error { return runEvidence(args) })
	if err != nil {
		t.Fatal(err)
	}
	data := decodeJSONEnvelope[evidenceRunData](t, out).Data
	if data.Result.Process == nil || data.Result.Process.State != "succeeded" || !data.Linked || data.Report.State != "suppressed" {
		t.Fatalf("run data=%+v", data)
	}
	persisted, err := taskstore.ShowForProfile(project, "review", "s", task.ID)
	if err != nil || len(persisted.CommandEvidence) != 1 || persisted.CommandEvidence[0].AttemptID != "attempt-stable" {
		t.Fatalf("task link=%+v err=%v", persisted.CommandEvidence, err)
	}
	if _, err := taskstore.DoneForProfile(project, "review", "s", task.ID, "worker", "done", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	repeatOut, _, err := captureOutput(t, func() error { return runEvidence(args) })
	if err != nil {
		t.Fatalf("same attempt must return before inactive rejection: %v", err)
	}
	repeat := decodeJSONEnvelope[evidenceRunData](t, repeatOut).Data
	if !repeat.Result.Existing || repeat.Report.State != "not_repeated" {
		t.Fatalf("existing result=%+v", repeat)
	}
	conflict := evidenceRunArgs(t, project, task.ID, "attempt-stable", "different subject", true)
	if _, _, err := captureOutput(t, func() error { return runEvidence(conflict) }); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting attempt did not win before inactive state: %v", err)
	}
}

func TestEvidenceSymlinkAliasBindsCanonicalNamespaceTaskAndStore(t *testing.T) {
	project := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Skipf("symlink aliases unavailable: %v", err)
	}
	task := seedEvidenceTaskAt(t, project, false)
	args := evidenceRunArgs(t, alias, task.ID, "attempt-alias", "alias binding", true)
	out, _, err := captureOutput(t, func() error { return runEvidence(args) })
	if err != nil {
		t.Fatal(err)
	}
	data := decodeJSONEnvelope[evidenceRunData](t, out).Data
	realProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	wantTaskPath := filepath.Join(taskstore.DirForProfile(realProject, "review", "s"), task.ID+".json")
	if data.Result.Manifest.NamespaceID != "review/s" || data.Result.Manifest.TaskPath != wantTaskPath {
		t.Fatalf("alias selected a different namespace/task: namespace=%q task_path=%q want=%q", data.Result.Manifest.NamespaceID, data.Result.Manifest.TaskPath, wantTaskPath)
	}
	persisted, err := taskstore.ShowForProfile(realProject, "review", "s", task.ID)
	if err != nil || len(persisted.CommandEvidence) != 1 {
		t.Fatalf("canonical task link=%+v err=%v", persisted.CommandEvidence, err)
	}
	canonicalStore, err := commandevidence.NewStore(realProject, "review", "s")
	if err != nil {
		t.Fatal(err)
	}
	aliasStore, err := commandevidence.NewStore(alias, "review", "s")
	if err != nil {
		t.Fatal(err)
	}
	if canonicalStore.Root != aliasStore.Root || filepath.Dir(data.Result.ManifestPath) != filepath.Join(canonicalStore.Root, "tasks", task.ID, "attempts", "attempt-alias") {
		t.Fatalf("alias selected a different evidence store: canonical=%q alias=%q manifest=%q", canonicalStore.Root, aliasStore.Root, data.Result.ManifestPath)
	}
	canonicalResult, err := canonicalStore.Read(task.ID, "attempt-alias")
	if err != nil || canonicalResult.ManifestSHA256 != data.Result.ManifestSHA256 {
		t.Fatalf("canonical evidence read digest=%q want=%q err=%v", canonicalResult.ManifestSHA256, data.Result.ManifestSHA256, err)
	}
}

func TestEvidenceRunRejectsWrongActorWithoutReservation(t *testing.T) {
	project, task := seedEvidenceTask(t, false)
	args := evidenceRunArgs(t, project, task.ID, "attempt-wrong-actor", "subject", true)
	for i := range args {
		if args[i] == "worker" && i > 0 && args[i-1] == "--me" {
			args[i] = "other"
		}
	}
	if _, _, err := captureOutput(t, func() error { return runEvidence(args) }); err == nil || !strings.Contains(err.Error(), "active assignee") {
		t.Fatalf("wrong actor accepted: %v", err)
	}
	store, _ := commandevidence.NewStore(project, "review", "s")
	if _, err := os.Stat(filepath.Join(store.Root, "tasks", task.ID, "attempts", "attempt-wrong-actor")); !os.IsNotExist(err) {
		t.Fatalf("wrong actor reserved evidence: %v", err)
	}
}

func TestEvidenceReportUsesOnlyDispatchRouteAndStructuredContext(t *testing.T) {
	project, task := seedEvidenceTask(t, true)
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(project, ".agent-mail", "review", "s"), BaseRoot: filepath.Join(project, ".agent-mail", "review")}, "")
	args := evidenceRunArgs(t, project, task.ID, "attempt-report", "report subject", false)
	out, _, err := captureOutput(t, func() error { return runEvidence(args) })
	if err != nil {
		t.Fatal(err)
	}
	data := decodeJSONEnvelope[evidenceRunData](t, out).Data
	if data.Report.State != "sent" || data.Report.To != "cto" || data.Report.Thread != "p2p/cto__worker" || data.Report.MessageID == "" {
		t.Fatalf("report=%+v", data.Report)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls=%+v", *calls)
	}
	call := (*calls)[0]
	if amqFlagValue(call.Arg, "me") != "worker" || amqFlagValue(call.Arg, "to") != "cto" || amqFlagValue(call.Arg, "thread") != "p2p/cto__worker" {
		t.Fatalf("report escaped dispatch route: %v", call.Arg)
	}
	var context map[string]any
	if err := json.Unmarshal([]byte(amqFlagValue(call.Arg, "context")), &context); err != nil || context["task_id"] != task.ID || context["attempt_id"] != "attempt-report" {
		t.Fatalf("structured report context=%v err=%v", context, err)
	}
}

func TestEvidenceExactLinkRejectsCrossNamespacePathAndHash(t *testing.T) {
	project, task := seedEvidenceTask(t, false)
	args := evidenceRunArgs(t, project, task.ID, "attempt-link", "link subject", true)
	if _, _, err := captureOutput(t, func() error { return runEvidence(args) }); err != nil {
		t.Fatal(err)
	}
	persisted, _ := taskstore.ShowForProfile(project, "review", "s", task.ID)
	link := persisted.CommandEvidence[0]
	digest := evidenceTaskDigest(t, project, "review", "s", task.ID)
	cross := link
	cross.SummaryPath = strings.Replace(cross.SummaryPath, string(filepath.Separator)+"review"+string(filepath.Separator), string(filepath.Separator)+"other"+string(filepath.Separator), 1)
	if _, _, err := taskstore.LinkCommandEvidenceForProfile(project, "review", "s", task.ID, digest, "worker", cross, time.Now().UTC()); err == nil {
		t.Fatal("cross-profile evidence path accepted")
	}
	badHash := link
	badHash.SummarySHA256 = "sha256:" + strings.Repeat("f", 64)
	if _, _, err := taskstore.LinkCommandEvidenceForProfile(project, "review", "s", task.ID, digest, "worker", badHash, time.Now().UTC()); err == nil {
		t.Fatal("mismatched immutable summary hash accepted")
	}
}

func TestEvidenceShowListAreConciseAndBounded(t *testing.T) {
	project, task := seedEvidenceTask(t, false)
	args := evidenceRunArgs(t, project, task.ID, "attempt-bounded", strings.Repeat("s", 240), true)
	if _, _, err := captureOutput(t, func() error { return runEvidence(args) }); err != nil {
		t.Fatal(err)
	}
	listOut, _, err := captureOutput(t, func() error {
		return runEvidence([]string{"list", task.ID, "--project", project, "--profile", "review", "--session", "s", "--json"})
	})
	if err != nil || len(listOut) > 20000 || strings.Contains(listOut, `"argv"`) || strings.Contains(listOut, `"environment"`) || strings.Contains(listOut, `"findings"`) {
		t.Fatalf("unbounded list len=%d err=%v body=%s", len(listOut), err, listOut)
	}
	showOut, _, err := captureOutput(t, func() error {
		return runEvidence([]string{"show", task.ID, "attempt-bounded", "--project", project, "--profile", "review", "--session", "s", "--json"})
	})
	if err != nil || len(showOut) > 20000 || strings.Contains(showOut, `"argv"`) || strings.Contains(showOut, `"environment"`) {
		t.Fatalf("unbounded show len=%d err=%v body=%s", len(showOut), err, showOut)
	}
	if _, _, err := captureOutput(t, func() error {
		return runEvidence([]string{"list", task.ID, "--project", project, "--profile", "review", "--session", "s", "--limit", "101"})
	}); err == nil || !strings.Contains(err.Error(), "between 1 and 100") {
		t.Fatalf("list cap not enforced: %v", err)
	}
}

func TestEvidenceBareExecutableRejectsRelativeRecordedPATH(t *testing.T) {
	project, task := seedEvidenceTask(t, false)
	t.Setenv("PATH", "relative:/bin")
	args := []string{"run", task.ID, "--project", project, "--profile", "review", "--session", "s", "--me", "worker", "--subject", "path", "--attempt-id", "attempt-path", "--no-report", "--", "sh", "-c", "true"}
	if _, _, err := captureOutput(t, func() error { return runEvidence(args) }); err == nil || !strings.Contains(err.Error(), "relative or non-canonical") {
		t.Fatalf("relative recorded PATH accepted: %v", err)
	}
}

func TestEvidenceRecoverUsesExistingLinkCASWithoutDuplication(t *testing.T) {
	project, task := seedEvidenceTask(t, false)
	args := evidenceRunArgs(t, project, task.ID, "attempt-recover", "recover", true)
	if _, _, err := captureOutput(t, func() error { return runEvidence(args) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runEvidence([]string{"recover", task.ID, "attempt-recover", "--me", "worker", "--project", project, "--profile", "review", "--session", "s", "--json"})
	}); err != nil {
		t.Fatal(err)
	}
	persisted, _ := taskstore.ShowForProfile(project, "review", "s", task.ID)
	if len(persisted.CommandEvidence) != 1 {
		t.Fatalf("recovery duplicated task link: %+v", persisted.CommandEvidence)
	}
}

func TestEvidenceLookupStructuredPreferenceConflictsBoundsAndMailboxImmutability(t *testing.T) {
	project, task := seedEvidenceTask(t, false)
	root := squadnamespace.AMQRoot(project, "review", "s")
	at := time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC)
	rawPath := seedEvidenceMessage(t, root, "aaa", "same", "EVIDENCE: "+task.ID, "same body "+task.ID, at, nil)
	structuredPath := seedEvidenceMessage(t, root, "zzz", "same", "EVIDENCE: "+task.ID, "same body "+task.ID, at, map[string]any{"task_id": task.ID})
	conflictOne := seedEvidenceMessage(t, root, "aaa", "conflict", "DONE "+task.ID, "one", at.Add(time.Second), nil)
	conflictTwo := seedEvidenceMessage(t, root, "zzz", "conflict", "DONE "+task.ID, "two", at.Add(time.Second), nil)
	before := map[string][]byte{}
	for _, path := range []string{rawPath, structuredPath, conflictOne, conflictTwo} {
		before[path], _ = os.ReadFile(path)
	}
	out, _, err := captureOutput(t, func() error {
		return runEvidence([]string{"lookup", task.ID, "--project", project, "--profile", "review", "--session", "s", "--limit", "10", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	data := decodeJSONEnvelope[evidenceLookupData](t, out).Data
	if len(out) > 100000 || data.Returned != 3 {
		t.Fatalf("lookup bounds/rows len=%d data=%+v", len(out), data)
	}
	var same evidenceLookupRow
	conflicts := 0
	for _, row := range data.Rows {
		if row.MessageID == "same" {
			same = row
		}
		if row.MessageID == "conflict" && row.Conflict {
			conflicts++
		}
		if len(row.Paths) > evidenceReplicaCap || len(row.Body) > evidenceRenderBodyCap {
			t.Fatalf("unbounded lookup row: %+v", row)
		}
	}
	if !same.StructuredTask || same.ReplicaCount != 2 || conflicts != 2 {
		t.Fatalf("structured preference/conflict grouping failed: same=%+v conflicts=%d rows=%+v", same, conflicts, data.Rows)
	}
	for path, want := range before {
		got, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("lookup mutated mailbox %s: %v", path, readErr)
		}
	}
}

func seedEvidenceTask(t *testing.T, dispatch bool) (string, taskstore.Task) {
	t.Helper()
	project := t.TempDir()
	return project, seedEvidenceTaskAt(t, project, dispatch)
}

func seedEvidenceTaskAt(t *testing.T, project string, dispatch bool) taskstore.Task {
	t.Helper()
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	task, err := taskstore.AddForProfile(project, "review", "s", taskstore.AddInput{Title: "evidence", AssignTo: "worker"}, now)
	if err != nil {
		t.Fatal(err)
	}
	task, err = taskstore.ClaimForProfile(project, "review", "s", task.ID, "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if dispatch {
		task, err = taskstore.LinkDispatchForProfile(project, "review", "s", task.ID, taskstore.Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", Subject: "evidence"}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func evidenceRunArgs(t *testing.T, project, taskID, attemptID, subject string, noReport bool) []string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"run", taskID, "--project", project, "--profile", "review", "--session", "s", "--me", "worker", "--subject", subject, "--attempt-id", attemptID, "--env", "EVIDENCE_CLI_HELPER=1", "--json"}
	if noReport {
		args = append(args, "--no-report")
	}
	return append(args, "--", executable, "-test.run=^TestEvidenceCLIHelper$", "--", "small")
}

func evidenceTaskDigest(t *testing.T, project, profile, session, taskID string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(taskstore.DirForProfile(project, profile, session), taskID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func seedEvidenceMessage(t *testing.T, root, owner, id, subject, body string, created time.Time, context map[string]any) string {
	t.Helper()
	dir := filepath.Join(root, "agents", owner, "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	header := map[string]any{"schema": 1, "id": id, "from": "worker", "to": []string{"cto"}, "thread": "p2p/cto__worker", "subject": subject, "created": created.Format(time.RFC3339Nano), "kind": string(state.KindStatus)}
	if context != nil {
		header["context"] = context
	}
	b, err := json.MarshalIndent(header, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---json\n%s\n---\n%s\n", b, body)
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
