package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestDurableSendTimeoutPreservesIDAndRestartLookupDrainsAsRecipient(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	messageID := "2026-07-14T14-12-54.653Z_pid10503_d009f5c1"
	previous := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		return []byte(`warning before JSON
{"id":"` + messageID + `","thread":"p2p/alice__bob","to":["bob"],"wait":{"event":"timeout","stage":"drained","timeout":"20ms"}}
`), errors.New("exit status 4: send --wait-for drained timed out after 20ms")
	}
	t.Cleanup(func() { runAMQCommand = previous })
	out, receipt, err := runOwnedDurableSend(durableSendOptions{ProjectDir: dir, Profile: team.DefaultProfile, Session: "issue-96", Kind: "test"}, amqCommandRequest{Dir: dir, Arg: []string{"send", "--root", filepath.Join(dir, ".agent-mail", "issue-96"), "--me", "alice", "--to", "bob", "--thread", "p2p/alice__bob"}})
	if err == nil || !strings.Contains(err.Error(), messageID) || parseSentMessageID(string(out)) != messageID {
		t.Fatalf("timeout must preserve id in output/error: id=%q err=%v out=%s", receipt.MessageID, err, out)
	}
	if receipt.DeliveryState != deliveryStateDeliveredNotDrained || receipt.Recipient != "bob" || receipt.Path == "" {
		t.Fatalf("timeout receipt=%+v", receipt)
	}
	persisted, err := readDeliveryReceipt(receipt.Path)
	if err != nil || persisted.MessageID != messageID || persisted.DeliveryState != deliveryStateDeliveredNotDrained {
		t.Fatalf("restart projection=%+v err=%v", persisted, err)
	}

	var lookupMe string
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		lookupMe = amqFlagValue(req.Arg, "me")
		return []byte(`{"count":1,"receipts":[{"schema":1,"msg_id":"` + messageID + `","sender":"alice","consumer":"bob","stage":"drained","emitted_at":"2026-07-14T14:12:55.706833Z"}]}`), nil
	}
	stdout, _, showErr := captureOutput(t, func() error {
		return runReceiptShow([]string{messageID, "--project", dir, "--session", "issue-96", "--json"})
	})
	if showErr != nil || lookupMe != "bob" || !strings.Contains(stdout, `"delivery_state": "drained"`) || !strings.Contains(stdout, messageID) {
		t.Fatalf("restart lookup stdout=%s me=%q err=%v", stdout, lookupMe, showErr)
	}
	refreshed, _ := readDeliveryReceipt(receipt.Path)
	if refreshed.DeliveryState != deliveryStateDrained || refreshed.DrainedAt == nil || refreshed.LastCheckedAt == nil {
		t.Fatalf("refreshed receipt=%+v", refreshed)
	}
}

func TestRecipientAggregationIsMonotonicAndConflictsFailClosed(t *testing.T) {
	r := newDeliveryReceipt(t.TempDir(), team.DefaultProfile, "s", "", "", "", "test")
	r.MessageID = "msg-multi"
	r.Recipients = []string{"a", "b"}
	r.Consumers = []deliveryConsumerState{{Consumer: "a", State: deliveryStateDeliveredNotDrained}, {Consumer: "b", State: deliveryStateDeliveredNotDrained}}
	r.DeliveryState = deliveryStateDeliveredNotDrained
	applyNativeReceipt(&r, nativeAMQReceipt{MsgID: r.MessageID, Consumer: "a", Stage: "drained", EmittedAt: "2026-07-14T14:00:00Z"})
	if r.DeliveryState != deliveryStatePartiallyDrained {
		t.Fatalf("one of two drained state=%s", r.DeliveryState)
	}
	applyNativeReceipt(&r, nativeAMQReceipt{MsgID: r.MessageID, Consumer: "b", Stage: "drained", EmittedAt: "2026-07-14T14:01:00Z"})
	if r.DeliveryState != deliveryStateDrained || r.DrainedAt == nil || r.DrainedAt.Format(time.RFC3339) != "2026-07-14T14:01:00Z" {
		t.Fatalf("all drained receipt=%+v", r)
	}
	applyNativeReceipt(&r, nativeAMQReceipt{MsgID: r.MessageID, Consumer: "a", Stage: "dlq", EmittedAt: "2026-07-14T14:02:00Z"})
	if r.DeliveryState != deliveryStateAmbiguousUnknown || !strings.Contains(r.LastCheckError, "conflicting") {
		t.Fatalf("conflicting terminal evidence must fail closed: %+v", r)
	}
}

func TestReceiptShowRejectsDuplicateCorruptAndSymlinkRecords(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	for _, session := range []string{"one", "two"} {
		r := newDeliveryReceipt(dir, team.DefaultProfile, session, "", "qa", "", "test")
		r.MessageID = "duplicate-id"
		r.Root = filepath.Join(dir, ".agent-mail", session)
		r.Sender = "lead"
		r.Thread = receiptCanonicalP2P("lead", "qa")
		r.Recipients = []string{"qa"}
		r.Consumers = []deliveryConsumerState{{Consumer: "qa", State: deliveryStateDeliveredNotDrained}}
		if err := writeDeliveryReceipt(dir, team.DefaultProfile, session, &r); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := captureOutput(t, func() error { return runReceiptShow([]string{"duplicate-id", "--project", dir}) }); err == nil || !strings.Contains(err.Error(), "matching records") {
		t.Fatalf("duplicate lookup err=%v", err)
	}

	corruptDir := deliveryReceiptDir(dir, team.DefaultProfile, "corrupt")
	if err := os.MkdirAll(corruptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "future.json"), []byte(`{"schema_version":999,"message_id":"future"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runReceiptShow([]string{"future", "--project", dir, "--session", "corrupt"}) }); err == nil || !strings.Contains(err.Error(), "unsupported delivery receipt schema") {
		t.Fatalf("unknown schema err=%v", err)
	}

	symlinkDir := deliveryReceiptDir(dir, team.DefaultProfile, "links")
	if err := os.MkdirAll(symlinkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(corruptDir, "future.json"), filepath.Join(symlinkDir, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runReceiptShow([]string{"future", "--project", dir, "--session", "links"}) }); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink receipt err=%v", err)
	}
}

func TestOwnedSendExitZeroWithoutIDIsAmbiguousAndNonzero(t *testing.T) {
	dir := t.TempDir()
	previous := runAMQCommand
	runAMQCommand = func(amqCommandRequest) ([]byte, error) { return []byte("ok without id\n"), nil }
	t.Cleanup(func() { runAMQCommand = previous })
	_, receipt, err := runOwnedDurableSend(durableSendOptions{ProjectDir: dir, Profile: team.DefaultProfile, Session: "s", Kind: "test"}, amqCommandRequest{Dir: dir, Arg: []string{"send", "--root", filepath.Join(dir, ".agent-mail", "s"), "--me", "a", "--to", "b"}})
	if err == nil || receipt.DeliveryState != deliveryStateAmbiguousUnknown || !strings.Contains(err.Error(), receipt.AttemptID) || !strings.Contains(err.Error(), receipt.Path) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestConcurrentReceiptRefreshCannotDowngradeDrained(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	r := newDeliveryReceipt(dir, team.DefaultProfile, "issue-96", "", "bob", "", "test")
	r.MessageID, r.Sender = "msg-concurrent", "alice"
	r.Root = filepath.Join(dir, ".agent-mail", "issue-96")
	r.Thread = receiptCanonicalP2P("alice", "bob")
	r.Recipients = []string{"bob"}
	r.Consumers = []deliveryConsumerState{{Consumer: "bob", State: deliveryStateDeliveredNotDrained}}
	r.DeliveryState = deliveryStateDeliveredNotDrained
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "issue-96", &r); err != nil {
		t.Fatal(err)
	}
	previous := runAMQCommand
	var mu sync.Mutex
	calls := 0
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []byte(`{"count":1,"receipts":[{"msg_id":"msg-concurrent","consumer":"bob","stage":"drained","emitted_at":"2026-07-14T14:00:00Z"}]}`), nil
		}
		return []byte(`{"count":0,"receipts":[]}`), nil
	}
	t.Cleanup(func() { runAMQCommand = previous })
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := updateDeliveryReceiptLocked(dir, team.DefaultProfile, "issue-96", r.AttemptID, func(current *deliveryReceiptData) error {
				return refreshDeliveryReceipt(current, dir, team.DefaultProfile, "issue-96")
			})
			errCh <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	final, err := readDeliveryReceipt(r.Path)
	if err != nil || final.DeliveryState != deliveryStateDrained || final.DrainedAt == nil {
		t.Fatalf("concurrent final=%+v err=%v", final, err)
	}
}

func TestStaleWriterMonotonicMergeCannotOverwriteDrained(t *testing.T) {
	dir := t.TempDir()
	r := newDeliveryReceipt(dir, team.DefaultProfile, "s", "", "bob", "", "test")
	r.MessageID, r.Sender = "msg-stale", "alice"
	r.Root = filepath.Join(dir, ".agent-mail", "s")
	r.Thread = receiptCanonicalP2P("alice", "bob")
	r.Recipients = []string{"bob"}
	r.Consumers = []deliveryConsumerState{{Consumer: "bob", State: deliveryStateDeliveredNotDrained}}
	r.DeliveryState = deliveryStateDeliveredNotDrained
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &r); err != nil {
		t.Fatal(err)
	}
	stale := r
	_, err := updateDeliveryReceiptLocked(dir, team.DefaultProfile, "s", r.AttemptID, func(current *deliveryReceiptData) error {
		current.Status = dispatchSubmitConfirmed
		current.Method = "durable_amq+wake"
		current.Detail = "newer confirmed pane evidence"
		current.Acknowledged = true
		current.Fallback = true
		current.AMQInvoked = true
		current.PaneID = "%7"
		current.addStage(dispatchSubmitConfirmed, current.Detail)
		return applyNativeReceipt(current, nativeAMQReceipt{MsgID: r.MessageID, Consumer: "bob", Stage: "drained", EmittedAt: "2026-07-14T14:00:00Z"})
	})
	if err != nil {
		t.Fatal(err)
	}
	stale.Status = "queued"
	stale.Method = "stale_method"
	stale.Detail = "stale detail"
	stale.addStage("queued", "later wall-clock write from stale process image")
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &stale); err != nil {
		t.Fatal(err)
	}
	final, err := readDeliveryReceipt(r.Path)
	if err != nil || final.DeliveryState != deliveryStateDrained || final.DrainedAt == nil || final.Status != dispatchSubmitConfirmed || final.Method != "durable_amq+wake" || final.Detail != "newer confirmed pane evidence" || !final.Acknowledged || !final.Fallback || !final.AMQInvoked || final.NativeStage != "drained" || final.PaneID != "%7" || !receiptHasStage(&final, dispatchSubmitConfirmed) {
		t.Fatalf("monotonic merge final=%+v err=%v", final, err)
	}
}

func TestReceiptMergeRejectsImmutableIdentityAndProvenanceChanges(t *testing.T) {
	dir := t.TempDir()
	base := newDeliveryReceipt(dir, team.DefaultProfile, "s", "qa", "bob", "project_team", "test")
	base.MessageID, base.Sender = "msg-identity", "alice"
	base.Root = filepath.Join(dir, ".agent-mail", "s")
	base.Thread = receiptCanonicalP2P("alice", "bob")
	base.Recipients = []string{"bob"}
	base.Consumers = []deliveryConsumerState{{Consumer: "bob", State: deliveryStateDeliveredNotDrained}}
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &base); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*deliveryReceiptData){
		"target":     func(r *deliveryReceiptData) { r.Target.Session = "other" },
		"sender":     func(r *deliveryReceiptData) { r.Sender = "mallory" },
		"recipients": func(r *deliveryReceiptData) { r.Recipients = []string{"eve"} },
		"root":       func(r *deliveryReceiptData) { r.Root = filepath.Join(dir, ".agent-mail", "other") },
		"thread":     func(r *deliveryReceiptData) { r.Thread = "p2p/eve__mallory" },
		"kind":       func(r *deliveryReceiptData) { r.Kind = "other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &changed); err == nil || !strings.Contains(err.Error(), "receipt_corrupt") {
				t.Fatalf("immutable %s mutation err=%v", name, err)
			}
		})
	}
}

func TestReceiptRefreshCorruptTimestampAndCrossProfileInjectionFailClosed(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	r := newDeliveryReceipt(dir, team.DefaultProfile, "issue-96", "", "bob", "", "test")
	r.MessageID, r.Sender = "msg-corrupt-time", "alice"
	r.Root = filepath.Join(dir, ".agent-mail", "issue-96")
	r.Thread = receiptCanonicalP2P("alice", "bob")
	r.Recipients = []string{"bob"}
	r.Consumers = []deliveryConsumerState{{Consumer: "bob", State: deliveryStateDeliveredNotDrained}}
	r.DeliveryState = deliveryStateDeliveredNotDrained
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "issue-96", &r); err != nil {
		t.Fatal(err)
	}
	previous := runAMQCommand
	queryCalls := 0
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		queryCalls++
		return []byte(`{"count":1,"receipts":[{"msg_id":"msg-corrupt-time","consumer":"bob","stage":"drained","emitted_at":"not-a-time"}]}`), nil
	}
	t.Cleanup(func() { runAMQCommand = previous })
	stdout, _, err := captureOutput(t, func() error {
		return runReceiptShow([]string{"msg-corrupt-time", "--project", dir, "--session", "issue-96", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "receipt_corrupt") || strings.TrimSpace(stdout) != "" {
		t.Fatalf("corrupt timestamp stdout=%q err=%v", stdout, err)
	}
	after, _ := readDeliveryReceipt(r.Path)
	if after.DeliveryState != deliveryStateDeliveredNotDrained || after.DrainedAt != nil || !strings.Contains(after.LastCheckError, "receipt_corrupt") {
		t.Fatalf("corrupt timestamp projection=%+v", after)
	}

	injected := newDeliveryReceipt(dir, "named", "injected", "", "bob", "", "test")
	injected.MessageID, injected.Sender = "msg-injected", "alice"
	injected.Root = filepath.Join(dir, ".agent-mail", "named", "injected")
	injected.Thread = receiptCanonicalP2P("alice", "bob")
	injected.Recipients = []string{"bob"}
	injected.Consumers = []deliveryConsumerState{{Consumer: "bob", State: deliveryStateDeliveredNotDrained}}
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "injected", &injected); err != nil {
		t.Fatal(err)
	}
	beforeQueries := queryCalls
	if _, _, err := captureOutput(t, func() error {
		return runReceiptShow([]string{"msg-injected", "--project", dir, "--session", "injected"})
	}); err == nil || !strings.Contains(err.Error(), "namespace provenance") {
		t.Fatalf("cross-profile injection err=%v", err)
	}
	if queryCalls != beforeQueries {
		t.Fatal("cross-profile injected record reached native AMQ query")
	}
}

func TestDefaultProfileLookupPrunesRegisteredNamedReceiptRoots(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	teamsDir := filepath.Join(dir, team.DirName, team.TeamsDirName)
	if err := os.MkdirAll(teamsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamsDir, "review.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	namedRoot := filepath.Join(dir, team.DirName, "receipts", "review")
	if err := os.MkdirAll(filepath.Join(namedRoot, "named-session"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedRoot, "named-session", "corrupt.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if matches, err := findScopedDeliveryReceipts(dir, team.DefaultProfile, "", "missing"); err != nil || len(matches) != 0 {
		t.Fatalf("default lookup entered named receipt root: matches=%+v err=%v", matches, err)
	}
}

func TestDefaultNoSessionLookupIsolatesOrphanNamedRootsButExplicitScopeInspectsThem(t *testing.T) {
	dir := t.TempDir()
	orphanSession := filepath.Join(dir, team.DirName, "receipts", "orphan", "named-session")
	if err := os.MkdirAll(orphanSession, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanSession, "corrupt.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if matches, err := findScopedDeliveryReceipts(dir, team.DefaultProfile, "", "missing"); err != nil || len(matches) != 0 {
		t.Fatalf("default lookup entered orphan named root: matches=%+v err=%v", matches, err)
	}
	if _, err := findScopedDeliveryReceipts(dir, "orphan", "", "missing"); err == nil {
		t.Fatal("explicit orphan profile scope did not inspect its corrupt receipt")
	}

	defaultSession := filepath.Join(dir, team.DirName, "receipts", "default-session")
	if err := os.MkdirAll(defaultSession, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultSession, "corrupt.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findScopedDeliveryReceipts(dir, team.DefaultProfile, "", "missing"); err == nil {
		t.Fatal("selected default-profile corruption was silently ignored")
	}
}

func TestReceiptFilesystemContainmentRejectsAncestorSymlinkAndOpenSwap(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, team.DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, team.DirName, "receipts")); err != nil {
		t.Fatal(err)
	}
	r := newDeliveryReceipt(dir, team.DefaultProfile, "s", "", "bob", "", "test")
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &r); err == nil {
		t.Fatal("ancestor symlink escaping the project was accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("ancestor symlink wrote outside project: entries=%v err=%v", entries, err)
	}

	dir = t.TempDir()
	r = newDeliveryReceipt(dir, team.DefaultProfile, "s", "", "bob", "", "test")
	r.MessageID, r.Sender = "msg-swap", "alice"
	r.Root, r.Thread = filepath.Join(dir, ".agent-mail", "s"), receiptCanonicalP2P("alice", "bob")
	r.Recipients = []string{"bob"}
	r.Consumers = []deliveryConsumerState{{Consumer: "bob", State: deliveryStateDeliveredNotDrained}}
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &r); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "sentinel.json")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	original := r.Path + ".original"
	previousOpenHook := receiptBeforeSecureOpen
	receiptBeforeSecureOpen = func() {
		receiptBeforeSecureOpen = func() {}
		_ = os.Rename(r.Path, original)
		_ = os.Symlink(sentinel, r.Path)
	}
	t.Cleanup(func() { receiptBeforeSecureOpen = previousOpenHook })
	if _, _, err := captureOutput(t, func() error {
		return runReceiptShow([]string{"msg-swap", "--project", dir, "--session", "s"})
	}); err == nil {
		t.Fatal("record swap between lstat and open was accepted")
	}
}

func TestReceiptFilesystemRejectsInProjectAncestorAliasAndAncestorSwap(t *testing.T) {
	for _, mode := range []string{"existing_alias", "validation_open_swap", "same_inode_swap"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			alias := filepath.Join(dir, "alias-receipts")
			if err := os.MkdirAll(alias, 0o755); err != nil {
				t.Fatal(err)
			}
			managed := filepath.Join(dir, team.DirName)
			if err := os.MkdirAll(managed, 0o755); err != nil {
				t.Fatal(err)
			}
			receipts := filepath.Join(managed, "receipts")
			writeTarget := alias
			if mode == "existing_alias" {
				if err := os.Symlink(alias, receipts); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(receipts, 0o755); err != nil {
					t.Fatal(err)
				}
				previous := receiptBeforeRootOpen
				receiptBeforeRootOpen = func(component string) {
					if component != "receipts" {
						return
					}
					receiptBeforeRootOpen = func(string) {}
					_ = os.Rename(receipts, receipts+".original")
					if mode == "same_inode_swap" {
						_ = os.Symlink("receipts.original", receipts)
					} else {
						_ = os.Symlink(alias, receipts)
					}
				}
				t.Cleanup(func() { receiptBeforeRootOpen = previous })
				if mode == "same_inode_swap" {
					writeTarget = receipts + ".original"
				}
			}
			r := newDeliveryReceipt(dir, team.DefaultProfile, "s", "", "bob", "", "test")
			if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &r); err == nil {
				t.Fatalf("in-project ancestor %s was accepted", mode)
			}
			if entries, err := os.ReadDir(writeTarget); err != nil || len(entries) != 0 {
				t.Fatalf("ancestor %s wrote through alias: entries=%v err=%v", mode, entries, err)
			}
		})
	}
}

func TestReceiptAtomicRenameCannotFollowSwappedTarget(t *testing.T) {
	dir := t.TempDir()
	r := newDeliveryReceipt(dir, team.DefaultProfile, "s", "", "bob", "", "test")
	r.MessageID, r.Sender = "msg-rename", "alice"
	r.Root, r.Thread = filepath.Join(dir, ".agent-mail", "s"), receiptCanonicalP2P("alice", "bob")
	r.Recipients = []string{"bob"}
	r.Consumers = []deliveryConsumerState{{Consumer: "bob", State: deliveryStateDeliveredNotDrained}}
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &r); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "sentinel.json")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	previousRenameHook := receiptBeforeSecureRename
	receiptBeforeSecureRename = func() {
		receiptBeforeSecureRename = func() {}
		_ = os.Remove(r.Path)
		_ = os.Symlink(sentinel, r.Path)
	}
	t.Cleanup(func() { receiptBeforeSecureRename = previousRenameHook })
	r.Status = dispatchSubmitConfirmed
	r.addStage(dispatchSubmitConfirmed, "confirmed")
	if err := writeDeliveryReceipt(dir, team.DefaultProfile, "s", &r); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "outside" {
		t.Fatalf("atomic rename followed swapped target: %q err=%v", b, err)
	}
	if info, err := os.Lstat(r.Path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("receipt target after atomic rename=%v err=%v", info, err)
	}
}
