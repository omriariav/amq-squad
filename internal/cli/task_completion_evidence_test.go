package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTaskMutationCanonicalIDAndNamedProfileSelection(t *testing.T) {
	project := t.TempDir()
	chdir(t, project)
	project, _ = os.Getwd()
	withFixedTaskNow(t)
	seedTaskTwinProfiles(t, project, "s")
	defaultTask, _ := taskstore.AddForProfile(project, team.DefaultProfile, "s", taskstore.AddInput{Title: "default", AssignTo: "worker"}, taskNow())
	namedTask, _ := taskstore.AddForProfile(project, "release", "s", taskstore.AddInput{Title: "named", AssignTo: "worker"}, taskNow())
	if defaultTask.ID != namedTask.ID {
		t.Fatal("fixture did not create twin task ids")
	}
	defaultPath := filepath.Join(taskstore.DirForProfile(project, team.DefaultProfile, "s"), "t1.json")
	namedPath := filepath.Join(taskstore.DirForProfile(project, "release", "s"), "t1.json")
	defaultBefore, _ := os.ReadFile(defaultPath)
	namedBefore, _ := os.ReadFile(namedPath)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "worker", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "pinned to named profile") {
		t.Fatalf("omitted named profile did not fail closed: %v", err)
	}
	assertFileBytes(t, defaultPath, defaultBefore)
	assertFileBytes(t, namedPath, namedBefore)
	if _, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--task", "t1", "--phase", "testing", "--me", "worker", "--project", project, "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "pinned to named profile") {
		t.Fatalf("omitted-profile activity did not fail closed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(squadnamespace.AMQRoot(project, team.DefaultProfile, "s"), "agents", "worker", "activity.json")); !os.IsNotExist(err) {
		t.Fatalf("omitted activity touched default root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(squadnamespace.AMQRoot(project, "release", "s"), "agents", "worker", "activity.json")); !os.IsNotExist(err) {
		t.Fatalf("omitted activity touched named root: %v", err)
	}

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "worker", "--project", project, "--profile", "release", "--session", "s"})
	}); err != nil {
		t.Fatalf("explicit named claim: %v", err)
	}
	assertFileBytes(t, defaultPath, defaultBefore)
	namedClaimed, _ := taskstore.ShowForProfile(project, "release", "s", "t1")
	if namedClaimed.Status != taskstore.StatusInProgress {
		t.Fatalf("named task not claimed: %+v", namedClaimed)
	}
	if _, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--task", "t1", "--phase", "testing", "--me", "other", "--project", project, "--profile", "release", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "active assignee") {
		t.Fatalf("wrong activity actor err=%v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runActivity([]string{"set", "--task", "t1", "--phase", "testing", "--me", "worker", "--project", project, "--profile", "release", "--session", "s"})
	}); err != nil {
		t.Fatalf("named activity: %v", err)
	}
	namedActivity := filepath.Join(squadnamespace.AMQRoot(project, "release", "s"), "agents", "worker", "activity.json")
	if _, err := os.Stat(namedActivity); err != nil {
		t.Fatalf("named activity missing: %v", err)
	}
	defaultActivity := filepath.Join(squadnamespace.AMQRoot(project, team.DefaultProfile, "s"), "agents", "worker", "activity.json")
	if _, err := os.Stat(defaultActivity); !os.IsNotExist(err) {
		t.Fatalf("default root was touched by named activity: %v", err)
	}
	namedAfter, _ := os.ReadFile(namedPath)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "worker", "--project", project, "--profile", "default", "--session", "s"})
	}); err != nil {
		t.Fatalf("explicit default claim: %v", err)
	}
	assertFileBytes(t, namedPath, namedAfter)
	defaultClaimed, _ := taskstore.ShowForProfile(project, team.DefaultProfile, "s", "t1")
	if defaultClaimed.Status != taskstore.StatusInProgress {
		t.Fatalf("explicit default task not claimed: %+v", defaultClaimed)
	}

	outside := filepath.Join(project, "outside.json")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, badID := range []string{"../outside", "t1/../../outside", outside, "t01", "t1_other"} {
		if _, _, err := captureOutput(t, func() error {
			return runTask([]string{"renew", badID, "--me", "worker", "--project", project, "--profile", "release", "--session", "s"})
		}); err == nil || !strings.Contains(err.Error(), "invalid task id") {
			t.Fatalf("bad id %q err=%v", badID, err)
		}
		assertFileBytes(t, outside, []byte("sentinel"))
	}
}

func TestTaskCompletionReconcilePreviewApplyAndReplicaMovement(t *testing.T) {
	project := t.TempDir()
	chdir(t, project)
	project, _ = os.Getwd()
	withFixedTaskNow(t)
	seedTaskTwinProfiles(t, project, "s")
	defaultTask, _ := taskstore.AddForProfile(project, team.DefaultProfile, "s", taskstore.AddInput{Title: "default", AssignTo: "worker"}, taskNow())
	namedTask, _ := taskstore.AddForProfile(project, "release", "s", taskstore.AddInput{Title: "named", AssignTo: "worker"}, taskNow())
	_, _ = taskstore.ClaimForProfile(project, "release", "s", namedTask.ID, "worker", taskNow())
	_, _ = taskstore.LinkDispatchForProfile(project, "release", "s", namedTask.ID, taskstore.Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, taskNow())
	root := squadnamespace.AMQRoot(project, "release", "s")
	agentDir := filepath.Join(root, "agents", "cto")
	seedThreadMessage(t, agentDir, "new", "msg-done", "worker", []string{"cto"}, "p2p/cto__worker", "DONE: t1", string(state.KindStatus), taskNow(), "completed t1")
	messagePath := filepath.Join(agentDir, "inbox", "new", "msg-done.md")
	messageBefore, _ := os.ReadFile(messagePath)
	defaultPath := filepath.Join(taskstore.DirForProfile(project, team.DefaultProfile, "s"), defaultTask.ID+".json")
	defaultBefore, _ := os.ReadFile(defaultPath)
	namedPath := filepath.Join(taskstore.DirForProfile(project, "release", "s"), namedTask.ID+".json")
	namedBefore, _ := os.ReadFile(namedPath)

	previewOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", "t1", "--evidence-id", "msg-done", "--project", project, "--profile", "release", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	previewEnv := decodeJSONEnvelope[taskReconcileEnvelopeData](t, previewOut)
	preview := previewEnv.Data.Completion
	if preview == nil || preview.Exact || preview.ProposedState != taskstore.StatusInProgress || !containsTestString(preview.Blockers, "missing_structured_lifecycle") || preview.BindingSHA256 == "" || preview.CurrentPath != messagePath {
		t.Fatalf("preview=%+v", preview)
	}
	assertFileBytes(t, namedPath, namedBefore)
	assertFileBytes(t, defaultPath, defaultBefore)
	assertFileBytes(t, messagePath, messageBefore)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", "t1", "--evidence-id", "msg-done", "--binding-digest", preview.BindingSHA256, "--apply", "--me", "cto", "--project", project, "--profile", "release", "--session", "s"})
	}); err == nil {
		t.Fatal("legacy prose DONE unexpectedly applied")
	}
	return
	for name, args := range map[string][]string{
		"missing binding": {"reconcile", "t1", "--evidence-id", "msg-done", "--apply", "--me", "cto", "--project", project, "--profile", "release", "--session", "s"},
		"wrong binding":   {"reconcile", "t1", "--evidence-id", "msg-done", "--binding-digest", "wrong", "--apply", "--me", "cto", "--project", project, "--profile", "release", "--session", "s"},
		"missing actor":   {"reconcile", "t1", "--evidence-id", "msg-done", "--binding-digest", preview.BindingSHA256, "--apply", "--project", project, "--profile", "release", "--session", "s"},
	} {
		if _, _, err := captureOutput(t, func() error { return runTask(args) }); err == nil {
			t.Fatalf("%s unexpectedly applied", name)
		}
		assertFileBytes(t, namedPath, namedBefore)
		assertFileBytes(t, messagePath, messageBefore)
	}

	applyArgs := []string{"reconcile", "t1", "--evidence-id", "msg-done", "--binding-digest", preview.BindingSHA256, "--apply", "--me", "cto", "--project", project, "--profile", "release", "--session", "s", "--json"}
	applyOut, _, err := captureOutput(t, func() error { return runTask(applyArgs) })
	if err != nil {
		t.Fatal(err)
	}
	applied := decodeJSONEnvelope[taskReconcileEnvelopeData](t, applyOut).Data.Applied
	if applied == nil || !applied.Changed || applied.Task.Status != taskstore.StatusCompleted || applied.Task.CompletionReconcile.CompletedEvidence.AppliedBy != "cto" {
		t.Fatalf("applied=%+v", applied)
	}
	assertFileBytes(t, defaultPath, defaultBefore)
	assertFileBytes(t, messagePath, messageBefore)
	repeatOut, _, err := captureOutput(t, func() error { return runTask(applyArgs) })
	if err != nil {
		t.Fatal(err)
	}
	repeat := decodeJSONEnvelope[taskReconcileEnvelopeData](t, repeatOut).Data.Applied
	if repeat == nil || repeat.Changed {
		t.Fatalf("repeat apply=%+v", repeat)
	}

	// Physical new->cur movement changes paths but not the canonical content or
	// stable binding identity.
	curDir := filepath.Join(agentDir, "inbox", "cur")
	if err := os.MkdirAll(curDir, 0o755); err != nil {
		t.Fatal(err)
	}
	curPath := filepath.Join(curDir, filepath.Base(messagePath))
	if err := os.Rename(messagePath, curPath); err != nil {
		t.Fatal(err)
	}
	movedOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", "t1", "--evidence-id", "msg-done", "--project", project, "--profile", "release", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	moved := decodeJSONEnvelope[taskReconcileEnvelopeData](t, movedOut).Data.Completion
	if moved.BindingSHA256 != preview.BindingSHA256 || moved.ContentSHA256 != preview.ContentSHA256 || moved.FirstPath != messagePath || moved.CurrentPath != curPath {
		t.Fatalf("movement changed identity: before=%+v after=%+v", preview, moved)
	}
	originalScanner := taskCompletionMessageScanner
	taskCompletionMessageScanner = func(string, func() time.Time) ([]state.Message, []state.Warning) { return nil, nil }
	warnings, err := statusTaskWarnings(project, "release", "s")
	taskCompletionMessageScanner = originalScanner
	if err != nil || findStatusWarning(warnings, "task_completion_evidence_stale") == nil {
		t.Fatalf("completed stale evidence not surfaced: warnings=%+v err=%v", warnings, err)
	}
}

func TestTaskCompletionEvidenceRawAuthorityMismatchAndTaskTokenBoundaries(t *testing.T) {
	for _, text := range []string{"DONE t10", "DONE t1_other", "DONE t1-other", "DONE xt1", "DONE t1x"} {
		if messageContainsExactTaskID(state.Message{Subject: text}, "t1") {
			t.Fatalf("false task token match: %q", text)
		}
	}
	if !messageContainsExactTaskID(state.Message{Subject: "DONE: t1"}, "t1") {
		t.Fatal("exact task token not recognized")
	}

	project := t.TempDir()
	chdir(t, project)
	project, _ = os.Getwd()
	withFixedTaskNow(t)
	seedTaskTwinProfiles(t, project, "s")
	defaultTask, _ := taskstore.AddForProfile(project, team.DefaultProfile, "s", taskstore.AddInput{Title: "default", AssignTo: "worker"}, taskNow())
	task, _ := taskstore.AddForProfile(project, "release", "s", taskstore.AddInput{Title: "named", AssignTo: "worker"}, taskNow())
	_, _ = taskstore.ClaimForProfile(project, "release", "s", task.ID, "worker", taskNow())
	_, _ = taskstore.LinkDispatchForProfile(project, "release", "s", task.ID, taskstore.Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, taskNow())
	root := squadnamespace.AMQRoot(project, "release", "s")
	agentDir := filepath.Join(root, "agents", "cto")
	seedThreadMessage(t, agentDir, "new", "wrong-sender", "other", []string{"cto"}, "p2p/cto__worker", "DONE: t1", string(state.KindStatus), taskNow())
	selected, err := readTaskSelection(project, "release", "s", "t1")
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := assessTaskCompletionEvidence(selected, "wrong-sender", taskNow())
	if err != nil || !containsTestString(wrong.Blockers, "sender_assignee_mismatch") || !containsTestString(wrong.Blockers, "missing_structured_lifecycle") || wrong.ProposedState != taskstore.StatusInProgress {
		t.Fatalf("wrong sender assessment=%+v err=%v", wrong, err)
	}
	defaultPath := filepath.Join(taskstore.DirForProfile(project, team.DefaultProfile, "s"), defaultTask.ID+".json")
	namedPath := filepath.Join(taskstore.DirForProfile(project, "release", "s"), task.ID+".json")
	messagePath := filepath.Join(agentDir, "inbox", "new", "wrong-sender.md")
	defaultBefore, _ := os.ReadFile(defaultPath)
	namedBefore, _ := os.ReadFile(namedPath)
	messageBefore, _ := os.ReadFile(messagePath)
	_, _, err = captureOutput(t, func() error {
		return runTask([]string{"reconcile", "t1", "--evidence-id", "wrong-sender", "--binding-digest", wrong.BindingSHA256, "--apply", "--me", "cto", "--project", project, "--profile", "release", "--session", "s", "--json"})
	})
	if err == nil {
		t.Fatal("unstructured sender mismatch unexpectedly entered pending reconcile")
	}
	assertFileBytes(t, defaultPath, defaultBefore)
	assertFileBytes(t, messagePath, messageBefore)
	assertFileBytes(t, namedPath, namedBefore)
	return
	missing, err := assessTaskCompletionEvidence(selected, "missing-id", taskNow())
	if err != nil || !containsTestString(missing.Blockers, "evidence_id_not_found") || missing.ProposedState == taskstore.StatusCompletedPendingReconcile {
		t.Fatalf("missing assessment=%+v err=%v", missing, err)
	}

	seedThreadMessage(t, agentDir, "new", "bad-thread", "worker", []string{"cto"}, "P2P/CTO__WORKER", "DONE: t1", string(state.KindStatus), taskNow())
	badThread, _ := assessTaskCompletionEvidence(selected, "bad-thread", taskNow())
	if !containsTestString(badThread.Blockers, "repaired_or_noncanonical_thread") || badThread.ProposedState == taskstore.StatusCompletedPendingReconcile {
		t.Fatalf("repaired raw thread was accepted: %+v", badThread)
	}
}

func TestTaskCompletionEvidenceNegativeAuthorityMatrix(t *testing.T) {
	project := t.TempDir()
	chdir(t, project)
	project, _ = os.Getwd()
	withFixedTaskNow(t)
	seedTaskTwinProfiles(t, project, "s")
	task, _ := taskstore.AddForProfile(project, "release", "s", taskstore.AddInput{Title: "named", AssignTo: "worker"}, taskNow())
	_, _ = taskstore.ClaimForProfile(project, "release", "s", task.ID, "worker", taskNow())
	_, _ = taskstore.LinkDispatchForProfile(project, "release", "s", task.ID, taskstore.Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, taskNow())
	selected, _ := readTaskSelection(project, "release", "s", "t1")
	root := squadnamespace.AMQRoot(project, "release", "s")
	base := state.Message{
		ID: "msg", From: "worker", To: []string{"cto"}, ToPresent: true, ToArrayValid: true, ToRaw: `["cto"]`,
		Thread: "p2p/cto__worker", RawThread: "p2p/cto__worker", Subject: "DONE: t1", RawSubject: "DONE: t1",
		Body: "completed t1", RawBody: "completed t1\n", Kind: state.KindStatus, Created: taskNow(), RawCreated: taskNow().Format(time.RFC3339Nano),
		Owner: "cto", Path: filepath.Join(root, "agents", "cto", "inbox", "new", "msg.md"), SchemaOK: true, AuthorityRaw: true,
	}
	tests := []struct {
		name, blocker string
		mutate        func(*state.Message)
		pending       bool
	}{
		{"wrong recipient", "recipient_mismatch", func(m *state.Message) { m.To, m.ToRaw = []string{"other"}, `["other"]` }, false},
		{"wrong owner", "owner_mismatch", func(m *state.Message) {
			m.Owner = "other"
			m.Path = filepath.Join(root, "agents", "other", "inbox", "new", "msg.md")
		}, false},
		{"wrong thread", "thread_mismatch", func(m *state.Message) { m.Thread, m.RawThread = "p2p/cto__other", "p2p/cto__other" }, false},
		{"degraded schema", "degraded_message_schema", func(m *state.Message) { m.SchemaOK = false }, false},
		{"no raw authority", "untrusted_raw_authority", func(m *state.Message) { m.AuthorityRaw = false }, false},
		{"recipient absent", "invalid_recipient_envelope", func(m *state.Message) { m.ToPresent = false }, false},
		{"recipient malformed", "invalid_recipient_envelope", func(m *state.Message) { m.ToArrayValid = false }, false},
		{"wrong task token", "missing_structured_lifecycle", func(m *state.Message) {
			m.Subject, m.RawSubject, m.Body, m.RawBody = "DONE: t10", "DONE: t10", "completed t10", "completed t10\n"
		}, false},
	}
	originalScanner := taskCompletionMessageScanner
	t.Cleanup(func() { taskCompletionMessageScanner = originalScanner })
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message := base
			tc.mutate(&message)
			taskCompletionMessageScanner = func(string, func() time.Time) ([]state.Message, []state.Warning) {
				return []state.Message{message}, nil
			}
			preview, err := assessTaskCompletionEvidence(selected, "msg", taskNow())
			if err != nil || !containsTestString(preview.Blockers, tc.blocker) {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			if tc.pending != (preview.ProposedState == taskstore.StatusCompletedPendingReconcile) {
				t.Fatalf("pending=%v preview=%+v", tc.pending, preview)
			}
		})
	}
	// Same ID with divergent raw content is a deterministic blocker.
	taskCompletionMessageScanner = func(string, func() time.Time) ([]state.Message, []state.Warning) {
		other := base
		other.RawBody, other.Body, other.Path = "different\n", "different", filepath.Join(root, "agents", "cto", "inbox", "cur", "msg.md")
		return []state.Message{base, other}, nil
	}
	conflict, _ := assessTaskCompletionEvidence(selected, "msg", taskNow())
	if !containsTestString(conflict.Blockers, "conflicting_same_id_content") {
		t.Fatalf("same-id conflict=%+v", conflict)
	}
	// Evidence in the default twin namespace is invisible to named reconcile.
	taskCompletionMessageScanner = originalScanner
	defaultRoot := squadnamespace.AMQRoot(project, team.DefaultProfile, "s")
	seedThreadMessage(t, filepath.Join(defaultRoot, "agents", "cto"), "new", "wrong-root", "worker", []string{"cto"}, "p2p/cto__worker", "DONE: t1", string(state.KindStatus), taskNow())
	wrongRoot, _ := assessTaskCompletionEvidence(selected, "wrong-root", taskNow())
	if !containsTestString(wrongRoot.Blockers, "evidence_id_not_found") {
		t.Fatalf("wrong namespace evidence leaked into named assessment: %+v", wrongRoot)
	}
}

func TestTaskCompletionReconcileExplicitDefaultTwinIsolation(t *testing.T) {
	project := t.TempDir()
	chdir(t, project)
	project, _ = os.Getwd()
	withFixedTaskNow(t)
	seedTaskTwinProfiles(t, project, "s")
	defaultTask, _ := taskstore.AddForProfile(project, team.DefaultProfile, "s", taskstore.AddInput{Title: "default", AssignTo: "worker"}, taskNow())
	namedTask, _ := taskstore.AddForProfile(project, "release", "s", taskstore.AddInput{Title: "named", AssignTo: "worker"}, taskNow())
	_, _ = taskstore.ClaimForProfile(project, team.DefaultProfile, "s", defaultTask.ID, "worker", taskNow())
	_, _ = taskstore.LinkDispatchForProfile(project, team.DefaultProfile, "s", defaultTask.ID, taskstore.Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, taskNow())
	root := squadnamespace.AMQRoot(project, team.DefaultProfile, "s")
	seedThreadMessage(t, filepath.Join(root, "agents", "cto"), "new", "default-done", "worker", []string{"cto"}, "p2p/cto__worker", "DONE: t1", string(state.KindStatus), taskNow())
	namedPath := filepath.Join(taskstore.DirForProfile(project, "release", "s"), namedTask.ID+".json")
	namedBefore, _ := os.ReadFile(namedPath)
	previewOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", "t1", "--evidence-id", "default-done", "--project", project, "--profile", "default", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := decodeJSONEnvelope[taskReconcileEnvelopeData](t, previewOut).Data.Completion
	if preview == nil || preview.Exact || !containsTestString(preview.Blockers, "missing_structured_lifecycle") {
		t.Fatalf("default preview=%+v", preview)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", "t1", "--evidence-id", "default-done", "--binding-digest", preview.BindingSHA256, "--apply", "--me", "cto", "--project", project, "--profile", "default", "--session", "s"})
	}); err == nil {
		t.Fatal("unstructured default-twin DONE unexpectedly applied")
	}
	assertFileBytes(t, namedPath, namedBefore)
	if got, _ := taskstore.ShowForProfile(project, team.DefaultProfile, "s", "t1"); got.Status != taskstore.StatusInProgress {
		t.Fatalf("unstructured reconcile changed default twin: %+v", got)
	}
	if _, err := os.Stat(squadnamespace.AMQRoot(project, "release", "s")); !os.IsNotExist(err) {
		t.Fatalf("default reconcile touched named AMQ root: %v", err)
	}
}

func TestDoctorCompletionPendingReconcileStatusAndLeaseGuidance(t *testing.T) {
	project := t.TempDir()
	chdir(t, project)
	project, _ = os.Getwd()
	withFixedTaskNow(t)
	seedTaskTwinProfiles(t, project, "s")
	task, _ := taskstore.AddForProfile(project, "release", "s", taskstore.AddInput{Title: "named", AssignTo: "worker"}, taskNow())
	_, _ = taskstore.ClaimForProfile(project, "release", "s", task.ID, "worker", taskNow())
	_, _ = taskstore.LinkDispatchForProfile(project, "release", "s", task.ID, taskstore.Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, taskNow())
	root := squadnamespace.AMQRoot(project, "release", "s")
	seedThreadMessage(t, filepath.Join(root, "agents", "cto"), "new", "wrong", "other", []string{"cto"}, "p2p/cto__worker", "DONE: t1", string(state.KindStatus), taskNow())
	selected, _ := readTaskSelection(project, "release", "s", task.ID)
	preview, _ := assessTaskCompletionEvidence(selected, "wrong", taskNow())
	if _, err := taskstore.ApplyCompletionEvidenceForProfile(project, "release", "s", task.ID, taskstore.CompletionEvidenceApply{
		ExpectedTaskSHA256: selected.FileSHA256, Evidence: completionEvidenceRecord(preview, "cto", taskNow()), Actor: "cto", Now: taskNow(),
	}); err != nil {
		t.Fatal(err)
	}
	warnings, err := statusTaskWarnings(project, "release", "s")
	if err != nil {
		t.Fatal(err)
	}
	mismatch := findStatusWarning(warnings, "task_completion_evidence_mismatch")
	stale := findStatusWarning(warnings, "task_completion_stale_lease")
	if mismatch == nil || stale == nil || !strings.Contains(mismatch.SuggestedCommand, "--project "+project+" --profile release --session s") ||
		!strings.Contains(stale.SuggestedCommand, "task renew t1 --me worker") {
		t.Fatalf("pending warnings=%+v", warnings)
	}
	for _, warning := range warnings {
		for _, forbidden := range []string{"task release", "task reset", "task cancel", "task fail", "task block", "task done"} {
			if strings.Contains(warning.Detail+" "+warning.SuggestedCommand, forbidden) {
				t.Fatalf("pending warning suggests %s: %+v", forbidden, warning)
			}
		}
	}
	doctor := doctorCheckTaskCompletionEvidence(doctorExecution{ProjectDir: project, Profile: "release"}, "s")
	if doctor.Status != doctorWarn || !strings.Contains(doctor.Detail, "next=amq-squad task reconcile t1") || !strings.Contains(doctor.Detail, "--profile release --session s") {
		t.Fatalf("doctor pending evidence=%+v", doctor)
	}

	path := filepath.Join(taskstore.DirForProfile(project, "release", "s"), task.ID+".json")
	b, _ := os.ReadFile(path)
	var persisted taskstore.Task
	if err := json.Unmarshal(b, &persisted); err != nil {
		t.Fatal(err)
	}
	persisted.Lease = nil
	b, _ = json.MarshalIndent(persisted, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err = statusTaskWarnings(project, "release", "s")
	if err != nil {
		t.Fatal(err)
	}
	legacy := findStatusWarning(warnings, "task_completion_legacy_unleased")
	if legacy == nil || !strings.Contains(legacy.SuggestedCommand, "task renew t1 --me worker") || strings.Contains(legacy.Detail+legacy.SuggestedCommand, "task release") {
		t.Fatalf("legacy pending warning=%+v", legacy)
	}
}

func seedTaskTwinProfiles(t *testing.T, project, session string) {
	t.Helper()
	for _, profile := range []string{team.DefaultProfile, "release"} {
		if err := team.WriteProfile(project, profile, team.Team{Project: project, Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: session},
			{Role: "worker", Handle: "worker", Binary: "codex", Session: session},
		}, Orchestrated: true, Lead: "cto"}); err != nil {
			t.Fatal(err)
		}
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("file changed unexpectedly: %s", path)
	}
}

func containsTestString(in []string, want string) bool {
	for _, item := range in {
		if item == want {
			return true
		}
	}
	return false
}
