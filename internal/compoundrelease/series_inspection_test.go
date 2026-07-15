package compoundrelease

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

func sessionScope(scope Scope) SessionScope {
	return SessionScope{ProjectDir: scope.ProjectDir, Profile: scope.Profile, Session: scope.Session, NamespaceGeneration: scope.NamespaceGeneration}
}

func makeSeriesDirectorySet(t *testing.T, count int) SessionScope {
	t.Helper()
	scope := testScope(t)
	session := sessionScope(scope)
	if count == 0 {
		return session
	}
	root, _, err := openContainedRoot(scope.ProjectDir, filepath.Join(".amq-squad", "evidence", scope.Profile, scope.Session, "compound-release"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for i := 0; i < count; i++ {
		name := seriesIDPrefix + fmt.Sprintf("%064x", i)
		if err := root.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

func TestEnumerateSessionSeriesBounded(t *testing.T) {
	for _, count := range []int{0, 1, 64} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			scope := makeSeriesDirectorySet(t, count)
			root, _, series, err := enumerateSessionSeries(scope)
			if root != nil {
				defer root.Close()
			}
			defer closeEnumerated(series)
			if err != nil || len(series) != count {
				t.Fatalf("series=%d err=%v", len(series), err)
			}
		})
	}
	t.Run("65", func(t *testing.T) {
		scope := makeSeriesDirectorySet(t, 65)
		if _, _, _, err := enumerateSessionSeries(scope); err == nil || !strings.Contains(err.Error(), "exceeds cap") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestEnumerateSessionSeriesRejectsNameAndType(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(*testing.T, *os.Root)
	}{
		{"name", func(t *testing.T, root *os.Root) { t.Helper(); _ = root.Mkdir("release-series-v1-BAD", 0o700) }},
		{"file", func(t *testing.T, root *os.Root) {
			t.Helper()
			f, err := root.Create(seriesIDPrefix + strings.Repeat("a", 64))
			if err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := testScope(t)
			root, _, err := openContainedRoot(scope.ProjectDir, filepath.Join(".amq-squad", "evidence", scope.Profile, scope.Session, "compound-release"), true)
			if err != nil {
				t.Fatal(err)
			}
			tc.make(t, root)
			root.Close()
			if _, _, _, err := enumerateSessionSeries(sessionScope(scope)); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}

func TestEnumerateSessionSeriesRejectsSymlinkAndSwap(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		scope := testScope(t)
		base := filepath.Join(scope.ProjectDir, ".amq-squad", "evidence", scope.Profile, scope.Session, "compound-release")
		if err := os.MkdirAll(base, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(scope.ProjectDir, filepath.Join(base, seriesIDPrefix+strings.Repeat("a", 64))); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := enumerateSessionSeries(sessionScope(scope)); err == nil {
			t.Fatal("series symlink accepted")
		}
	})
	t.Run("swap", func(t *testing.T) {
		scope := makeSeriesDirectorySet(t, 1)
		oldHook := storeContainmentHook
		defer func() { storeContainmentHook = oldHook }()
		fired := false
		storeContainmentHook = func(stage, path string) error {
			if stage == "after_series_validation" && !fired {
				fired = true
				if err := os.Rename(path, path+".old"); err != nil {
					return err
				}
				return os.Mkdir(path, 0o700)
			}
			return nil
		}
		if _, _, _, err := enumerateSessionSeries(scope); err == nil || !fired {
			t.Fatalf("swap err=%v fired=%v", err, fired)
		}
	})
}

func TestSessionScopeProfileNormalization(t *testing.T) {
	defaultScope := testScope(t)
	defaultStore := openTestStore(t, defaultScope)
	if _, err := defaultStore.Create(specForScope(defaultScope)); err != nil {
		t.Fatal(err)
	}
	alias := sessionScope(defaultScope)
	alias.Profile = ""
	if got, err := InspectSessionSeries(alias); err != nil || len(got) != 1 {
		t.Fatalf("default alias=%+v err=%v", got, err)
	}

	named := testScope(t)
	named.Profile = "named-profile"
	namedStore := openTestStore(t, named)
	if _, err := namedStore.Create(specForScope(named)); err != nil {
		t.Fatal(err)
	}
	if got, err := InspectSessionSeries(sessionScope(named)); err != nil || len(got) != 1 {
		t.Fatalf("named=%+v err=%v", got, err)
	}
}

func TestInspectSessionSeriesUsesPreexistingNonblockingLock(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	if _, err := store.Create(specForScope(scope)); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(store.dirPath, "store.lock")
	if err := os.WriteFile(lockPath, []byte("sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockPath, 0o400); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	inspections, err := InspectSessionSeries(sessionScope(scope))
	if err != nil || len(inspections) != 1 || inspections[0].Snapshot.Pointer.State != "planned" {
		t.Fatalf("inspections=%+v err=%v", inspections, err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(lockPath)
	if string(raw) != "sentinel\n" || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("ordinary inspection mutated store.lock")
	}
	if err := os.Chmod(lockPath, 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := store.openLeaf("store.lock", os.O_RDWR, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := flock.AcquireExclusiveFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSessionSeries(sessionScope(scope)); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("busy err=%v", err)
	}
	_ = lease.Close()
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSessionSeries(sessionScope(scope)); err == nil {
		t.Fatal("missing preexisting lock accepted")
	}
}

func activeInspectionFixture(t *testing.T) (*Store, Snapshot, *fakeReconcileAdapter, ResolveQuery, Resolution) {
	t.Helper()
	store, publishing, adapter := reconcileFixture(t)
	stageAllReconcileEvidence(t, store, publishing, adapter)
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil {
		t.Fatal(err)
	}
	child := result.Snapshot.Prepared.Children[0]
	query := ResolveQuery{MessageID: result.Snapshot.Active.Children[0].QuestionMessageID, Gate: child.Thread, Action: child.Action, Target: child.Target}
	resolution, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || resolution.Claim == nil || resolution.Disposition != ResolutionEligible {
		t.Fatalf("resolution=%+v err=%v", resolution, err)
	}
	return store, result.Snapshot, adapter, query, resolution
}

func addActiveSeries(t *testing.T, first *Store, adapter *fakeReconcileAdapter, gate, suffix string) (*Store, Snapshot) {
	t.Helper()
	scope := first.scope
	scope.ParentGate = gate
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	publishing, err := store.StartPublishing(planned.Pointer.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	for i, child := range publishing.Prepared.Children {
		path := filepath.Join(scope.ProjectDir, ".amq-squad", "receipts", child.Receipt.AttemptID+suffix+".json")
		adapter.receiptPaths[child.Receipt.AttemptID] = path
		id := "question-" + child.Role + suffix
		raw := rawReleaseReceipt(t, scope, child, path, adapter.root, id)
		bound, decodeErr := decodeBoundReleaseReceiptV2(raw, scope, child, path, adapter.root)
		if decodeErr != nil || bound.Tuple == nil {
			t.Fatalf("bound child %d: %v", i, decodeErr)
		}
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, i); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, i, *bound.Tuple); err != nil {
			t.Fatal(err)
		}
		writeReleaseQuestion(t, adapter.root, child.Receipt.Recipient, "new", id, fmt.Sprintf("2026-07-15T02:00:0%dZ", i), child, nil)
		adapter.receipts[path] = raw
	}
	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil {
		t.Fatal(err)
	}
	return store, result.Snapshot
}

func TestResolveSeriesIndependentLeavesAndReceiptGenerationStability(t *testing.T) {
	store, snapshot, adapter, query, first := activeInspectionFixture(t)
	if len(first.Leaves) != 1 || len(first.Leaves[0].Children) != 2 || !first.Leaves[0].Children[0].Eligible || !first.Leaves[0].Children[1].Eligible || len(first.Recovery) != 1 || !first.Recovery[0].Cleared {
		t.Fatalf("leaves=%+v", first.Leaves)
	}
	child := snapshot.Prepared.Children[0]
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	adapter.receipts[path] = receiptAtGeneration(t, adapter.receipts[path], 9)
	second, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || second.Claim == nil || second.Claim.Token != first.Claim.Token {
		t.Fatalf("live generation changed immutable token: first=%+v second=%+v err=%v", first.Claim, second.Claim, err)
	}
	withoutID := query
	withoutID.MessageID = ""
	noID, err := ResolveSessionSeries(sessionScope(store.scope), withoutID, adapter)
	if err != nil || noID.Claim != nil || noID.Disposition != ResolutionSuppressed {
		t.Fatalf("message-id-free query selected authority: result=%+v err=%v", noID, err)
	}
	adapter.receipts[path] = []byte(strings.Replace(string(adapter.receipts[path]), `"sender":"cto"`, `"sender":"other"`, 1))
	drifted, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || drifted.Claim != nil || len(drifted.Recovery) != 1 {
		t.Fatalf("immutable receipt drift remained eligible: result=%+v err=%v", drifted, err)
	}
}

func TestResolveSeriesBrokenSiblingIsIsolatedWithOneRecovery(t *testing.T) {
	store, snapshot, adapter, query, _ := activeInspectionFixture(t)
	broken := snapshot.Prepared.Children[1]
	delete(adapter.receipts, adapter.receiptPaths[broken.Receipt.AttemptID])
	result, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || result.Claim == nil || len(result.Recovery) != 1 || len(result.Leaves) != 1 || !result.Leaves[0].Children[0].Eligible || result.Leaves[0].Children[1].Eligible {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRecoveryKeyStableFingerprintTracksClearRecoveryClear(t *testing.T) {
	store, snapshot, adapter, query, clear := activeInspectionFixture(t)
	if len(clear.Recovery) != 1 || !clear.Recovery[0].Cleared || clear.Recovery[0].Reason != RecoveryReasonHealthyClear {
		t.Fatalf("initial clear=%+v", clear.Recovery)
	}
	initial := clear.Recovery[0]
	child := snapshot.Prepared.Children[0]
	path := adapter.receiptPaths[child.Receipt.AttemptID]
	raw := append([]byte(nil), adapter.receipts[path]...)
	delete(adapter.receipts, path)

	recovery, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || len(recovery.Recovery) != 1 || recovery.Recovery[0].Cleared || recovery.Recovery[0].Reason != RecoveryReasonActiveEvidence {
		t.Fatalf("recovery=%+v err=%v", recovery, err)
	}
	degraded := recovery.Recovery[0]
	if degraded.Key != initial.Key || degraded.Fingerprint == initial.Fingerprint {
		t.Fatalf("key/fingerprint initial=%+v recovery=%+v", initial, degraded)
	}
	adapter.receipts[path] = raw

	clearedAgain, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || len(clearedAgain.Recovery) != 1 || !clearedAgain.Recovery[0].Cleared {
		t.Fatalf("cleared again=%+v err=%v", clearedAgain, err)
	}
	final := clearedAgain.Recovery[0]
	if final.Key != initial.Key || final.Fingerprint != initial.Fingerprint || final.Fingerprint == degraded.Fingerprint {
		t.Fatalf("initial=%+v recovery=%+v final=%+v", initial, degraded, final)
	}
}

func TestSuccessorRecoveryKeepsStableKeyAndUpdatesFingerprint(t *testing.T) {
	store, snapshot, adapter, query, clear := activeInspectionFixture(t)
	if len(clear.Recovery) != 1 {
		t.Fatalf("initial recovery=%+v", clear.Recovery)
	}
	initial := clear.Recovery[0]
	next, err := store.PrepareSuccessor(snapshot.Pointer.GenerationID, successorSpec(store.scope, "-next"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || len(result.Recovery) != 1 || result.Recovery[0].Kind != "resume_planned_release" || result.Recovery[0].Reason != RecoveryReasonPlanned || result.Recovery[0].Cleared {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	successor := result.Recovery[0]
	if successor.Key != initial.Key || successor.Fingerprint == initial.Fingerprint {
		t.Fatalf("initial=%+v successor=%+v", initial, successor)
	}
	if successor.Scope.ParentGate != next.Prepared.Spec.ParentGate || successor.SeriesID != store.seriesID || successor.GenerationID != next.Prepared.GenerationID || successor.PreparedManifestID != next.Prepared.PreparedManifestID {
		t.Fatalf("successor recovery metadata=%+v", successor)
	}
}

func TestCloseArtifactFailureReplacesSeriesRecovery(t *testing.T) {
	store, snapshot, adapter, query, clear := activeInspectionFixture(t)
	if len(clear.Recovery) != 1 {
		t.Fatalf("initial recovery=%+v", clear.Recovery)
	}
	initial := clear.Recovery[0]
	oldHook := storeContainmentHook
	defer func() { storeContainmentHook = oldHook }()
	lockOpens := 0
	closeFault := errors.New("close-time lock artifact unavailable")
	storeContainmentHook = func(stage, path string) error {
		if stage == "after_leaf_validation" && filepath.Base(path) == "store.lock" {
			lockOpens++
			if lockOpens == 2 {
				return closeFault
			}
		}
		return nil
	}

	result, err := ResolveSessionSeries(sessionScope(store.scope), query, adapter)
	if err != nil || result.Claim != nil || len(result.Recovery) != 1 || lockOpens != 2 {
		t.Fatalf("result=%+v err=%v lockOpens=%d", result, err, lockOpens)
	}
	replacement := result.Recovery[0]
	if replacement.Kind != "inspect_lock_artifact" || replacement.Reason != RecoveryReasonLockArtifactChanged || replacement.Cleared || replacement.Key != initial.Key || replacement.Fingerprint == initial.Fingerprint {
		t.Fatalf("initial=%+v replacement=%+v", initial, replacement)
	}
	if replacement.Scope.ParentGate != snapshot.Prepared.Spec.ParentGate || replacement.SeriesID != store.seriesID || replacement.GenerationID != snapshot.Prepared.GenerationID || replacement.PreparedManifestID != snapshot.Prepared.PreparedManifestID {
		t.Fatalf("replacement metadata=%+v", replacement)
	}
	if len(result.Leaves) != 1 || result.Leaves[0].State != ProjectionStateUnknown || result.Leaves[0].Reason != ProjectionReasonLockArtifactChanged {
		t.Fatalf("replacement leaf=%+v", result.Leaves)
	}
}

func TestResolveIdentifiableOtherSeriesFailuresAreIsolated(t *testing.T) {
	first, _, adapter, query, _ := activeInspectionFixture(t)
	second, secondSnapshot := addActiveSeries(t, first, adapter, "gate/release-other", "-other")
	broken := secondSnapshot.Prepared.Children[0]
	adapter.receiptErrs[adapter.receiptPaths[broken.Receipt.AttemptID]] = errors.New("read denied")
	result, err := ResolveSessionSeries(sessionScope(first.scope), query, adapter)
	if err != nil || result.Claim == nil || result.Claim.SeriesID != first.seriesID || len(result.Recovery) != 2 {
		t.Fatalf("read-isolation result=%+v err=%v", result, err)
	}
	delete(adapter.receiptErrs, adapter.receiptPaths[broken.Receipt.AttemptID])
	adapter.receiptPathErrs[broken.Receipt.AttemptID] = errors.New("path denied")
	result, err = ResolveSessionSeries(sessionScope(first.scope), query, adapter)
	if err != nil || result.Claim == nil || result.Claim.SeriesID != first.seriesID || len(result.Recovery) != 2 {
		t.Fatalf("path-isolation result=%+v err=%v", result, err)
	}
	delete(adapter.receiptPathErrs, broken.Receipt.AttemptID)
	if err := os.WriteFile(filepath.Join(second.dirPath, second.generationName(secondSnapshot.Pointer.Generation)), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = ResolveSessionSeries(sessionScope(first.scope), query, adapter)
	if err != nil || result.Claim == nil || result.Claim.SeriesID != first.seriesID || len(result.Recovery) != 2 {
		t.Fatalf("lifecycle-isolation result=%+v err=%v", result, err)
	}
	for _, child := range secondSnapshot.Prepared.Children {
		if !slices.Contains(result.Suppression.Threads, child.Thread) {
			t.Fatalf("identifiable corrupt series did not reserve %s", child.Thread)
		}
	}
	var corruptLeaf *SeriesLeaf
	for i := range result.Leaves {
		if result.Leaves[i].SeriesID == second.seriesID {
			corruptLeaf = &result.Leaves[i]
		}
	}
	if corruptLeaf == nil || corruptLeaf.State != ProjectionStateUnknown || corruptLeaf.Reason != ProjectionReasonCorruptLifecycle {
		t.Fatalf("corrupt pointer state leaked: %+v", corruptLeaf)
	}
	var corruptRecovery *RecoveryProjection
	for i := range result.Recovery {
		if result.Recovery[i].SeriesID == second.seriesID {
			corruptRecovery = &result.Recovery[i]
		}
	}
	if corruptRecovery == nil || corruptRecovery.Scope.ParentGate != secondSnapshot.Prepared.Spec.ParentGate || corruptRecovery.GenerationID != secondSnapshot.Prepared.GenerationID || corruptRecovery.PreparedManifestID != secondSnapshot.Prepared.PreparedManifestID || corruptRecovery.Reason != RecoveryReasonCorruptLifecycle {
		t.Fatalf("corrupt recovery identity=%+v", corruptRecovery)
	}
}

func TestUnidentifiableMissingLockRevokesExactSibling(t *testing.T) {
	first, firstSnapshot, adapter, _, _ := activeInspectionFixture(t)
	second, secondSnapshot := addActiveSeries(t, first, adapter, "gate/release-other", "-other")
	if err := os.Remove(filepath.Join(second.dirPath, "store.lock")); err != nil {
		t.Fatal(err)
	}
	adapter.scans = 0
	result, err := ResolveSessionSeries(sessionScope(first.scope), structuralOrdinaryQuery(), markerInspectionAdapter{InspectionAdapter: adapter, markers: structuralMarkerMessages()})
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Claim != nil || result.Degradation == nil || result.Degradation.Code != DegradationLockUnavailable || adapter.scans != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertStructuralClaims(t, result)
	for _, child := range firstSnapshot.Prepared.Children {
		if !slices.Contains(result.Suppression.Threads, child.Thread) {
			t.Fatalf("valid sibling did not reserve prepared thread %q", child.Thread)
		}
	}
	for _, child := range secondSnapshot.Prepared.Children {
		if slices.Contains(result.Suppression.Threads, child.Thread) {
			t.Fatalf("untrusted missing-lock series reserved thread %q", child.Thread)
		}
	}
}

func TestBusyStoreStillScansOnceAndClaimsPhysicalIDs(t *testing.T) {
	store, snapshot, adapter, _, _ := activeInspectionFixture(t)
	f, err := store.openLeaf("store.lock", os.O_RDONLY, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := flock.AcquireExclusiveFile(f)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	adapter.scans = 0
	adapter.resolved = 0
	result, err := ResolveSessionSeries(sessionScope(store.scope), structuralOrdinaryQuery(), markerInspectionAdapter{InspectionAdapter: adapter, markers: structuralMarkerMessages()})
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Degradation == nil || result.Degradation.Code != DegradationStoreBusy || result.Claim != nil || adapter.resolved != 1 || adapter.scans != 1 {
		t.Fatalf("result=%+v err=%v resolved=%d scans=%d", result, err, adapter.resolved, adapter.scans)
	}
	assertStructuralClaims(t, result)
	for _, child := range snapshot.Prepared.Children {
		if slices.Contains(result.Suppression.Threads, child.Thread) {
			t.Fatalf("untrusted busy series reserved thread %q", child.Thread)
		}
	}
}

func TestCorruptPreparedIdentityIsCommonBarrierWithoutThreadTrust(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.dirPath, store.preparedName(planned.Pointer.Generation)), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	scans := 0
	result, err := ResolveSessionSeries(sessionScope(scope), structuralOrdinaryQuery(), staticInspectionAdapter{root: root, scanCalls: &scans, messages: structuralMarkerMessages()})
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Degradation == nil || result.Degradation.Code != DegradationIdentityUnavailable || scans != 1 || len(result.Suppression.Threads) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertStructuralClaims(t, result)
}

func structuralMarkerMessages() []state.Message {
	return []state.Message{
		{ID: "malformed", RawThread: "gate/orphan", Context: map[string]any{"release_child": "bad"}},
		{ID: "v1", RawThread: "gate/orphan", Context: map[string]any{"release_child": map[string]any{"schema_version": 1}}},
		{ID: "orphan", RawThread: "gate/orphan", Context: map[string]any{"release_child": map[string]any{"schema_version": 2}}},
		{ID: "ordinary", RawThread: "gate/orphan", Context: map[string]any{}},
	}
}

func structuralOrdinaryQuery() ResolveQuery {
	return ResolveQuery{MessageID: "ordinary", Gate: "gate/orphan"}
}

func assertStructuralClaims(t *testing.T, result Resolution) {
	t.Helper()
	for _, id := range []string{"malformed", "orphan", "v1"} {
		if !slices.Contains(result.Suppression.MessageIDs, id) {
			t.Fatalf("physical release-child group %q was not claimed: %+v", id, result)
		}
	}
	if slices.Contains(result.Suppression.MessageIDs, "ordinary") {
		t.Fatalf("ordinary same-thread group was claimed: %+v", result)
	}
}

type markerInspectionAdapter struct {
	InspectionAdapter
	markers []state.Message
}

func (m markerInspectionAdapter) ScanSessionMessages(root string, now func() time.Time) ([]state.Message, []state.Warning) {
	messages, warnings := m.InspectionAdapter.ScanSessionMessages(root, now)
	return append(messages, m.markers...), warnings
}

type warningAdapter struct{ *fakeReconcileAdapter }

func (w warningAdapter) ScanSessionMessages(string, func() time.Time) ([]state.Message, []state.Warning) {
	return nil, []state.Warning{{Path: "mail", Reason: "torn"}}
}

type staticInspectionAdapter struct {
	root         string
	rootErr      error
	messages     []state.Message
	warnings     []state.Warning
	resolveCalls *int
	scanCalls    *int
}

func (s staticInspectionAdapter) ResolveSessionRoot(Scope) (string, error) {
	if s.resolveCalls != nil {
		*s.resolveCalls++
	}
	return s.root, s.rootErr
}
func (s staticInspectionAdapter) ExpectedReceiptPath(Scope, string) (string, error) {
	return "", os.ErrNotExist
}
func (s staticInspectionAdapter) ReadReceipt(string) ([]byte, error) { return nil, os.ErrNotExist }
func (s staticInspectionAdapter) ScanSessionMessages(string, func() time.Time) ([]state.Message, []state.Warning) {
	if s.scanCalls != nil {
		*s.scanCalls++
	}
	return s.messages, s.warnings
}

func TestResolveCleanZeroStoreScansOnceAndIsNotApplicable(t *testing.T) {
	scope := testScope(t)
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, scanned := 0, 0
	result, err := ResolveSessionSeries(sessionScope(scope), ResolveQuery{}, staticInspectionAdapter{root: root, resolveCalls: &resolved, scanCalls: &scanned})
	if err != nil || result.Disposition != ResolutionNotApplicable || result.Degradation != nil || resolved != 1 || scanned != 1 {
		t.Fatalf("result=%+v err=%v resolved=%d scanned=%d", result, err, resolved, scanned)
	}
}

func TestResolveRootUnavailableIsTypedBarrier(t *testing.T) {
	scope := testScope(t)
	resolved, scanned := 0, 0
	result, err := ResolveSessionSeries(sessionScope(scope), ResolveQuery{}, staticInspectionAdapter{rootErr: os.ErrNotExist, resolveCalls: &resolved, scanCalls: &scanned})
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Degradation == nil || result.Degradation.Code != DegradationRootUnavailable || resolved != 1 || scanned != 0 {
		t.Fatalf("result=%+v err=%v resolved=%d scanned=%d", result, err, resolved, scanned)
	}
}

func TestResolveUnexpectedEntryStillScansAndClaimsPhysicalIDs(t *testing.T) {
	scope := testScope(t)
	storeRoot := filepath.Join(scope.ProjectDir, ".amq-squad", "evidence", scope.Profile, scope.Session, "compound-release")
	if err := os.MkdirAll(filepath.Join(storeRoot, "unexpected"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, scanned := 0, 0
	adapter := staticInspectionAdapter{
		root: root, resolveCalls: &resolved, scanCalls: &scanned,
		messages: structuralMarkerMessages(),
	}
	result, err := ResolveSessionSeries(sessionScope(scope), structuralOrdinaryQuery(), adapter)
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Claim != nil || result.Degradation == nil || result.Degradation.Code != DegradationEnumerationFailed || resolved != 1 || scanned != 1 || len(result.Suppression.Threads) != 0 {
		t.Fatalf("result=%+v err=%v resolved=%d scanned=%d", result, err, resolved, scanned)
	}
	assertStructuralClaims(t, result)
}

func TestResolveZeroSeriesClaimsReleaseChildIDsOnly(t *testing.T) {
	scope := testScope(t)
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	messages := []state.Message{
		{ID: "malformed", RawThread: "gate/orphan", Context: map[string]any{"release_child": "bad"}},
		{ID: "v1", RawThread: "gate/orphan", Context: map[string]any{"release_child": map[string]any{"schema_version": 1}}},
		{ID: "orphan", RawThread: "gate/orphan", Context: map[string]any{"release_child": map[string]any{"schema_version": 2}}},
		{ID: "ordinary", RawThread: "gate/orphan", Context: map[string]any{}},
	}
	adapter := staticInspectionAdapter{root: root, messages: messages}
	result, err := ResolveSessionSeries(sessionScope(scope), ResolveQuery{MessageID: "v1", Gate: "gate/orphan"}, adapter)
	if err != nil || result.Disposition != ResolutionSuppressed || !slices.Equal(result.Suppression.MessageIDs, []string{"malformed", "orphan", "v1"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	ordinary, err := ResolveSessionSeries(sessionScope(scope), ResolveQuery{MessageID: "ordinary", Gate: "gate/orphan"}, adapter)
	if err != nil || ordinary.Disposition != ResolutionNotApplicable {
		t.Fatalf("ordinary=%+v err=%v", ordinary, err)
	}
}

func TestResolveZeroSeriesWarningIsCommonBarrierWithDiagnostics(t *testing.T) {
	scope := testScope(t)
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	adapter := staticInspectionAdapter{
		root:     root,
		messages: []state.Message{{ID: "orphan", Context: map[string]any{"release_child": "malformed"}}},
		warnings: []state.Warning{{Path: "mail", Reason: "torn"}},
	}
	result, err := ResolveSessionSeries(sessionScope(scope), ResolveQuery{MessageID: "ordinary"}, adapter)
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Degradation == nil || result.Degradation.Code != DegradationScanWarning || result.Claim != nil || len(result.Leaves) != 0 || !slices.Contains(result.Suppression.MessageIDs, "orphan") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestResolveWarningBarrierProjectsEverySeries(t *testing.T) {
	store, _, adapter, query, _ := activeInspectionFixture(t)
	result, err := ResolveSessionSeries(sessionScope(store.scope), query, warningAdapter{adapter})
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Claim != nil || len(result.Leaves) != 1 || len(result.Recovery) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, child := range result.Leaves[0].Children {
		if child.Eligible {
			t.Fatal("warning barrier left eligible child")
		}
	}
}

func TestResolveWarningBarrierRevokesTwoExactSeries(t *testing.T) {
	first, _, adapter, query, _ := activeInspectionFixture(t)
	addActiveSeries(t, first, adapter, "gate/release-other", "-other")
	result, err := ResolveSessionSeries(sessionScope(first.scope), query, warningAdapter{adapter})
	if err != nil || result.Disposition != ResolutionCommonBarrier || result.Claim != nil || len(result.Leaves) != 2 || len(result.Recovery) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

type artifactImage struct {
	Raw     string
	Mode    os.FileMode
	ModTime int64
}

func captureArtifacts(t *testing.T, store *Store, names []string) map[string]artifactImage {
	t.Helper()
	result := make(map[string]artifactImage, len(names))
	for _, name := range names {
		path := filepath.Join(store.dirPath, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = artifactImage{Raw: string(raw), Mode: info.Mode(), ModTime: info.ModTime().UnixNano()}
	}
	return result
}

func TestRecordAheadInspectionIsRecoveryOnlyAndNonMutating(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	stageAllReconcileEvidence(t, store, publishing, adapter)
	record, err := store.readGeneration(publishing.Pointer.Generation)
	if err != nil {
		t.Fatal(err)
	}
	receipts := make(map[string]operatorauth.ReleaseDeliveryReceiptTuple)
	for _, child := range record.Children {
		receipts[child.Role] = *child.Receipt
	}
	active, err := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop before pointer")
	if _, err := store.activate(active, func(stage string) error {
		if stage == "pointer-update" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("record-ahead setup: %v", err)
	}
	names := []string{"store.lock", "current.json", store.generationName(publishing.Pointer.Generation), store.preparedName(publishing.Pointer.Generation), store.activeName(publishing.Pointer.Generation)}
	before := captureArtifacts(t, store, names)
	child := publishing.Prepared.Children[0]
	result, err := ResolveSessionSeries(sessionScope(store.scope), ResolveQuery{MessageID: "question-" + child.Role, Gate: child.Thread, Action: child.Action, Target: child.Target}, adapter)
	if err != nil || result.Claim != nil || len(result.Recovery) != 1 || len(result.Leaves) != 1 || !result.Leaves[0].RecordAhead {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, preparedChild := range publishing.Prepared.Children {
		if !slices.Contains(result.Suppression.Threads, preparedChild.Thread) {
			t.Fatalf("record-ahead did not reserve %s", preparedChild.Thread)
		}
	}
	after := captureArtifacts(t, store, names)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("record-ahead inspection mutated artifacts\nbefore=%+v\nafter=%+v", before, after)
	}
}

func forceTerminalLifecycle(t *testing.T, state string, withActive bool) (*Store, staticInspectionAdapter) {
	t.Helper()
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.readGeneration(planned.Pointer.Generation)
	if err != nil {
		t.Fatal(err)
	}
	record.State = state
	var active operatorauth.ActiveReleaseManifest
	if withActive {
		active, err = operatorauth.NewActiveRelease(planned.Prepared, observedReceipts(scope, planned.Prepared, "-terminal"))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.writeActive(active); err != nil {
			t.Fatal(err)
		}
		record.ActiveManifestID = active.ActiveManifestID
		record.ActiveSHA256 = operatorauth.ActiveReleaseSHA256(active)
		record.Children = adoptedChildRecords(publishedChildRecordsForTest(planned.Prepared, active), planned.Prepared, active)
	}
	if state == operatorauth.ReleaseStateSuperseded {
		successor, successorErr := operatorauth.DerivePreparedRelease(successorSpec(scope, "-terminal-successor"), planned.Pointer.Generation+1)
		if successorErr != nil {
			t.Fatal(successorErr)
		}
		if err := store.writePrepared(successor); err != nil {
			t.Fatal(err)
		}
		record.SuccessorGenerationID = successor.GenerationID
	}
	if state == operatorauth.ReleaseStateConflict {
		record.Children[0].State = childPublicationConflict
		record.Children[0].ConflictReason = "conflicting exact evidence"
		record.Children[0].ObservedMessageIDs = []string{"message-conflict"}
	}
	if err := store.writeGeneration(record); err != nil {
		t.Fatal(err)
	}
	pointer := pointerFromRecord(record, planned.Pointer.Revision+1)
	if err := store.writePointer(pointer); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return store, staticInspectionAdapter{root: root}
}

func TestTerminalRecoveryProjectionTable(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		withActive bool
		recovery   int
	}{
		{"planned", operatorauth.ReleaseStatePlanned, false, 1},
		{"publishing", operatorauth.ReleaseStatePublishing, false, 1},
		{"conflict", operatorauth.ReleaseStateConflict, false, 1},
		{"aborted", operatorauth.ReleaseStateAborted, false, 1},
		{"superseded-inactive", operatorauth.ReleaseStateSuperseded, false, 1},
		{"superseded-active", operatorauth.ReleaseStateSuperseded, true, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, adapter := forceTerminalLifecycle(t, tc.state, tc.withActive)
			inspected, err := InspectSessionSeries(sessionScope(store.scope))
			if err != nil || len(inspected) != 1 || inspected[0].Snapshot.Pointer.State != tc.state || tc.withActive && inspected[0].Snapshot.Active == nil {
				t.Fatalf("inspection=%+v err=%v", inspected, err)
			}
			adapter.messages = []state.Message{{ID: "later-chatter", RawThread: inspected[0].Snapshot.Prepared.Children[0].Thread, Context: map[string]any{}}}
			result, err := ResolveSessionSeries(sessionScope(store.scope), ResolveQuery{}, adapter)
			if err != nil || result.Claim != nil || len(result.Recovery) != tc.recovery {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			for _, child := range inspected[0].Snapshot.Prepared.Children {
				if !slices.Contains(result.Suppression.Threads, child.Thread) {
					t.Fatalf("state %s did not reserve %s", tc.state, child.Thread)
				}
			}
			if slices.Contains(result.Suppression.MessageIDs, "later-chatter") {
				t.Fatal("ordinary later chatter was claimed by id")
			}
			if tc.state == operatorauth.ReleaseStateAborted && !result.Recovery[0].Cleared {
				t.Fatal("aborted recovery was not explicitly cleared")
			}
		})
	}
}

func TestPointerAheadSuccessorInspectionIsNonMutating(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	first, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	oldFault := storeFault
	defer func() { storeFault = oldFault }()
	crash := errors.New("after old terminalized")
	storeFault = func(stage string) error {
		if stage == "after_old_terminalized" {
			return crash
		}
		return nil
	}
	if _, err := store.PrepareSuccessor(first.Pointer.GenerationID, successorSpec(scope, "-crash")); !errors.Is(err, crash) {
		t.Fatalf("fault=%v", err)
	}
	storeFault = oldFault
	pointer, err := store.readPointer()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"store.lock", "current.json", store.generationName(first.Pointer.Generation), store.preparedName(first.Pointer.Generation), store.preparedName(first.Pointer.Generation + 1)}
	before := captureArtifacts(t, store, names)
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := ResolveSessionSeries(sessionScope(scope), ResolveQuery{MessageID: "ordinary", Gate: first.Prepared.Children[0].Thread}, staticInspectionAdapter{root: root})
	if err != nil || result.Claim != nil || len(result.Recovery) != 1 || result.Recovery[0].Kind != "complete_successor" || !slices.Contains(result.Suppression.Threads, first.Prepared.Children[0].Thread) || pointer.State != operatorauth.ReleaseStateSuperseded {
		t.Fatalf("pointer=%+v result=%+v err=%v", pointer, result, err)
	}
	after := captureArtifacts(t, store, names)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("successor inspection mutated artifacts")
	}
}

func TestPointerAheadSuccessorRealCrashInspection(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	first, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	publishing, err := store.StartPublishing(first.Pointer.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	receipts := stagePublishedChildrenForTest(t, store, scope, publishing, "")
	active, err := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(active); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMutableCrashSubprocessHelper$")
	cmd.Env = append(os.Environ(), mutableCrashEnv+"=successor_pointer_published", mutableCrashEnv+"_PROJECT="+scope.ProjectDir)
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 91 {
		t.Fatalf("crash subprocess=%v", err)
	}
	store, err = Open(scope, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	names := []string{"store.lock", "current.json", store.generationName(1), store.preparedName(1), store.activeName(1), store.preparedName(2)}
	before := captureArtifacts(t, store, names)
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := ResolveSessionSeries(sessionScope(scope), ResolveQuery{MessageID: "ordinary", Gate: first.Prepared.Children[0].Thread}, staticInspectionAdapter{root: root})
	if err != nil || result.Claim != nil || len(result.Recovery) != 1 || result.Recovery[0].Kind != "complete_successor" || !slices.Contains(result.Suppression.Threads, first.Prepared.Children[0].Thread) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if after := captureArtifacts(t, store, names); !reflect.DeepEqual(before, after) {
		t.Fatal("real-crash inspection mutated artifacts")
	}
}

func TestInvocationGuardSingleUsePanicReleaseAndRootValidation(t *testing.T) {
	store, _, adapter, query, resolution := activeInspectionFixture(t)
	guard, err := NewInvocationGuard(sessionScope(store.scope), query, *resolution.Claim, adapter)
	if err != nil {
		t.Fatal(err)
	}
	panicked := false
	func() {
		defer func() { panicked = recover() == "boom" }()
		_ = guard.Run(func() error { panic("boom") })
	}()
	if !panicked {
		t.Fatal("callback panic was not propagated")
	}
	if err := guard.Run(func() error { return nil }); err == nil {
		t.Fatal("guard reused")
	}
	if _, err := InspectSessionSeries(sessionScope(store.scope)); err != nil {
		t.Fatalf("lock not released: %v", err)
	}

	store2, _, adapter2, query2, resolution2 := activeInspectionFixture(t)
	adapter2.root = "relative/root"
	guard2, err := NewInvocationGuard(sessionScope(store2.scope), query2, *resolution2.Claim, adapter2)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if runErr := guard2.Run(func() error { called = true; return nil }); runErr == nil || called {
		t.Fatalf("runErr=%v called=%v", runErr, called)
	}
	if _, err := InspectSessionSeries(sessionScope(store2.scope)); err != nil {
		t.Fatalf("root-validation lock leak: %v", err)
	}
}

func TestInvocationGuardRetainsExclusiveLease(t *testing.T) {
	store, snapshot, adapter, query, resolution := activeInspectionFixture(t)
	guard, err := NewInvocationGuard(sessionScope(store.scope), query, *resolution.Claim, adapter)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	transitionDone := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := guard.Run(func() error {
			close(entered)
			go func() {
				_, transitionErr := store.PrepareSuccessor(snapshot.Pointer.GenerationID, successorSpec(store.scope, "-guard-wins"))
				transitionDone <- transitionErr
			}()
			select {
			case err := <-transitionDone:
				return fmt.Errorf("transition escaped retained guard: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			<-release
			return nil
		}); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	if _, err := InspectSessionSeries(sessionScope(store.scope)); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("guard did not retain exclusive lease: %v", err)
	}
	close(release)
	wg.Wait()
	if err := <-transitionDone; err != nil {
		t.Fatalf("post-guard transition: %v", err)
	}
}

func successorSpec(scope Scope, suffix string) operatorauth.ReleaseSpec {
	spec := specForScope(scope)
	spec.TagTarget += suffix
	spec.GitHubReleaseTarget += suffix
	spec.Note.Summary += suffix
	return spec
}

func TestInvocationGuardSuccessorWinsAndDifferentSeriesDoesNotRevoke(t *testing.T) {
	t.Run("successor-wins", func(t *testing.T) {
		store, snapshot, adapter, query, resolution := activeInspectionFixture(t)
		guard, err := NewInvocationGuard(sessionScope(store.scope), query, *resolution.Claim, adapter)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PrepareSuccessor(snapshot.Pointer.GenerationID, successorSpec(store.scope, "-next")); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := guard.Run(func() error { called = true; return nil }); err == nil || called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})
	t.Run("different-series", func(t *testing.T) {
		first, _, adapter, query, resolution := activeInspectionFixture(t)
		second, secondSnapshot := addActiveSeries(t, first, adapter, "gate/release-other", "-other")
		guard, err := NewInvocationGuard(sessionScope(first.scope), query, *resolution.Claim, adapter)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := second.PrepareSuccessor(secondSnapshot.Pointer.GenerationID, successorSpec(second.scope, "-next")); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := guard.Run(func() error { called = true; return nil }); err != nil || !called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})
}

func TestInvocationGuardRejectsExactMessageDrift(t *testing.T) {
	store, snapshot, adapter, query, resolution := activeInspectionFixture(t)
	guard, err := NewInvocationGuard(sessionScope(store.scope), query, *resolution.Claim, adapter)
	if err != nil {
		t.Fatal(err)
	}
	child := snapshot.Prepared.Children[0]
	path := filepath.Join(adapter.root, "agents", child.Receipt.Recipient, "inbox", "new", query.MessageID+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), child.Body, child.Body+" drift", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := guard.Run(func() error { called = true; return nil }); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestInvocationGuardCallbackAndReleaseErrorsUnlock(t *testing.T) {
	t.Run("callback", func(t *testing.T) {
		store, _, adapter, query, resolution := activeInspectionFixture(t)
		guard, err := NewInvocationGuard(sessionScope(store.scope), query, *resolution.Claim, adapter)
		if err != nil {
			t.Fatal(err)
		}
		callbackErr := errors.New("callback failed")
		if err := guard.Run(func() error { return callbackErr }); !errors.Is(err, callbackErr) {
			t.Fatalf("err=%v", err)
		}
		if _, err := InspectSessionSeries(sessionScope(store.scope)); err != nil {
			t.Fatalf("callback lock leak: %v", err)
		}
	})
	t.Run("release-fault", func(t *testing.T) {
		store, _, adapter, query, resolution := activeInspectionFixture(t)
		guard, err := NewInvocationGuard(sessionScope(store.scope), query, *resolution.Claim, adapter)
		if err != nil {
			t.Fatal(err)
		}
		oldFault := invocationGuardFault
		defer func() { invocationGuardFault = oldFault }()
		fault := errors.New("release fault")
		invocationGuardFault = func(stage string) error {
			if stage == "after_release" {
				return fault
			}
			return nil
		}
		callbackErr := errors.New("combined callback fault")
		if err := guard.Run(func() error { return callbackErr }); !errors.Is(err, fault) || !errors.Is(err, callbackErr) {
			t.Fatalf("err=%v", err)
		}
		if _, err := InspectSessionSeries(sessionScope(store.scope)); err != nil {
			t.Fatalf("release-fault lock leak: %v", err)
		}
	})
}
