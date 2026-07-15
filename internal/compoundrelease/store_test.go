package compoundrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

const mutableCrashEnv = "AMQ_SQUAD_RELEASE_MUTABLE_CRASH"

func testScope(t *testing.T) Scope {
	t.Helper()
	return Scope{ProjectDir: t.TempDir(), Profile: "default", Session: "issue-414", NamespaceGeneration: "none", ParentGate: "gate/release-414"}
}

func specForScope(scope Scope) operatorauth.ReleaseSpec {
	return operatorauth.ReleaseSpec{
		SchemaVersion: operatorauth.ReleaseSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Namespace:  operatorauth.NamespaceBinding{ProjectDir: scope.ProjectDir, Profile: scope.Profile, Session: scope.Session, NamespaceID: scope.Profile + "/" + scope.Session, Generation: scope.NamespaceGeneration},
		ParentGate: scope.ParentGate, RequesterHandle: "cto", OperatorHandle: "user",
		TagTarget: "v2.20.1", GitHubReleaseTarget: "release v2.20.1 from exact commit deadbeef",
		Note: operatorauth.ReleaseNote{Summary: "publish accepted artifacts"},
	}
}

func observedReceipts(scope Scope, prepared operatorauth.PreparedReleaseManifest, suffix string) map[string]operatorauth.ReleaseDeliveryReceiptTuple {
	result := map[string]operatorauth.ReleaseDeliveryReceiptTuple{}
	for _, child := range prepared.Children {
		result[child.Role] = operatorauth.ReleaseDeliveryReceiptTuple{
			AttemptID: child.Receipt.AttemptID, Kind: child.Receipt.Kind, Sender: child.Receipt.Sender,
			Recipients: []string{child.Receipt.Recipient}, Thread: child.Receipt.Thread, MessageID: "question-" + child.Role + suffix,
			Path: filepath.Join(scope.ProjectDir, ".amq-squad", "receipts", child.Role+suffix+".json"), Root: filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session),
			NamespaceID: child.Receipt.NamespaceID, TargetIdentity: child.Receipt.TargetIdentity, AdoptedGeneration: child.Receipt.MinimumGeneration,
		}
	}
	return result
}

func publishedChildRecordsForTest(prepared operatorauth.PreparedReleaseManifest, active operatorauth.ActiveReleaseManifest) []childPublicationRecord {
	records := newGenerationRecord("test-series", prepared, "").Children
	for i := range records {
		records[i].State = childPublicationPublished
		records[i].ClaimRevision = 1
		records[i].ClaimToken = "release-claim-v1-" + strings.Repeat(string(rune('a'+i)), 64)
		records[i].QuestionMessageID = active.Children[i].QuestionMessageID
		records[i].ReceiptPath = active.Children[i].Receipt.Path
		records[i].ReceiptSHA256, _ = operatorauth.ReleaseDeliveryReceiptSHA256(active.Children[i].Receipt)
		receipt := cloneDeliveryReceipt(active.Children[i].Receipt)
		records[i].Receipt = &receipt
	}
	return records
}

func openTestStore(t *testing.T, scope Scope) *Store {
	t.Helper()
	store, err := Open(scope, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func stagePublishedChildrenForTest(t *testing.T, store *Store, scope Scope, publishing Snapshot, suffix string) map[string]operatorauth.ReleaseDeliveryReceiptTuple {
	t.Helper()
	receipts := observedReceipts(scope, publishing.Prepared, suffix)
	for ordinal, role := range []string{operatorauth.ReleaseChildTag, operatorauth.ReleaseChildGitHubRelease} {
		record, err := store.readGeneration(publishing.Pointer.Generation)
		if err != nil {
			t.Fatal(err)
		}
		if record.Children[ordinal].State == childPublicationPlanned {
			if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, ordinal); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, ordinal, receipts[role]); err != nil {
			t.Fatal(err)
		}
	}
	return receipts
}

func TestStoreLifecycleAndIdempotency(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	spec := specForScope(scope)

	planned, err := store.Create(spec)
	if err != nil || planned.Pointer.State != operatorauth.ReleaseStatePlanned || planned.Pointer.Generation != 1 {
		t.Fatalf("Create()=(%+v,%v)", planned, err)
	}
	again, err := store.Create(spec)
	if err != nil || again.Pointer.Revision != planned.Pointer.Revision || again.Prepared.PreparedManifestID != planned.Prepared.PreparedManifestID {
		t.Fatalf("idempotent Create()=(%+v,%v)", again, err)
	}
	changed := spec
	changed.TagTarget = "v2.20.2"
	if _, err := store.Create(changed); !errors.Is(err, ErrSpecChangeRequiresSuccessor) {
		t.Fatalf("changed Create error=%v", err)
	}

	publishing, err := store.StartPublishing(planned.Pointer.GenerationID)
	if err != nil || publishing.Pointer.State != operatorauth.ReleaseStatePublishing {
		t.Fatalf("StartPublishing()=(%+v,%v)", publishing, err)
	}
	receipts := stagePublishedChildrenForTest(t, store, scope, publishing, "")
	activeManifest, err := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Activate(activeManifest)
	if err != nil || active.Pointer.State != operatorauth.ReleaseStateActive || active.Active == nil {
		t.Fatalf("Activate()=(%+v,%v)", active, err)
	}
	read, err := store.ReadCurrent()
	if err != nil || read.Active == nil || read.Active.ActiveManifestID != activeManifest.ActiveManifestID {
		t.Fatalf("ReadCurrent()=(%+v,%v)", read, err)
	}
	if retry, err := store.Activate(activeManifest); err != nil || retry.Pointer.Revision != active.Pointer.Revision {
		t.Fatalf("idempotent Activate()=(%+v,%v)", retry, err)
	}
}

func TestStoreActiveManifestFaultReusesSameGeneration(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	publishing, err := store.StartPublishing(planned.Pointer.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	receipts := stagePublishedChildrenForTest(t, store, scope, publishing, "")
	active, err := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
	if err != nil {
		t.Fatal(err)
	}
	oldFault := storeFault
	fired := false
	storeFault = func(stage string) error {
		if stage == "after_active_manifest_write" && !fired {
			fired = true
			return errors.New("crash after active manifest")
		}
		return nil
	}
	t.Cleanup(func() { storeFault = oldFault })
	if _, err := store.Activate(active); err == nil {
		t.Fatal("faulted activation unexpectedly succeeded")
	}
	storeFault = oldFault
	got, err := store.Activate(active)
	if err != nil || got.Active == nil || got.Active.ActiveManifestID != active.ActiveManifestID {
		t.Fatalf("activation recovery=(%+v,%v)", got, err)
	}

	other := active
	other.Children[0].QuestionMessageID = "other-question"
	other.Children[0].Receipt.MessageID = "other-question"
	// Re-derive a structurally valid but different immutable manifest.
	other, err = operatorauth.NewActiveRelease(publishing.Prepared, observedReceipts(scope, publishing.Prepared, "-other"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(other); err == nil {
		t.Fatal("second active manifest for one generation unexpectedly succeeded")
	}
}

func TestStoreSuccessorOrderingAndRecovery(t *testing.T) {
	for _, faultStage := range []string{"after_successor_prepared", "after_old_terminalized", "before_successor_pointer"} {
		t.Run(faultStage, func(t *testing.T) {
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
			active, _ := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
			if _, err := store.Activate(active); err != nil {
				t.Fatal(err)
			}
			nextSpec := specForScope(scope)
			nextSpec.TagTarget = "v2.20.2"
			nextSpec.GitHubReleaseTarget = "release v2.20.2 from exact commit cafe1234"

			oldFault := storeFault
			fired := false
			storeFault = func(stage string) error {
				if stage == faultStage && !fired {
					fired = true
					return errors.New("injected successor fault")
				}
				return nil
			}
			t.Cleanup(func() { storeFault = oldFault })
			if _, err := store.PrepareSuccessor(first.Pointer.GenerationID, nextSpec); err == nil {
				t.Fatal("faulted successor unexpectedly succeeded")
			}
			storeFault = oldFault
			recovered, err := store.PrepareSuccessor(first.Pointer.GenerationID, nextSpec)
			if err != nil || recovered.Pointer.Generation != 2 || recovered.Pointer.State != operatorauth.ReleaseStatePlanned {
				t.Fatalf("successor recovery=(%+v,%v)", recovered, err)
			}
			if recovered.Prepared.Spec.TagTarget != nextSpec.TagTarget {
				t.Fatal("recovered successor has wrong spec")
			}
		})
	}
}

func TestStoreConcurrentCreateHasOneIdentity(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	spec := specForScope(scope)
	const workers = 12
	results := make(chan Snapshot, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.Create(spec)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := ""
	for result := range results {
		if want == "" {
			want = result.Prepared.PreparedManifestID
		}
		if result.Prepared.PreparedManifestID != want || result.Pointer.Generation != 1 {
			t.Fatalf("concurrent result=%+v", result)
		}
	}
}

func TestStoreRejectsSymlinkAncestorAndSwap(t *testing.T) {
	t.Run("existing symlink", func(t *testing.T) {
		scope := testScope(t)
		outside := t.TempDir()
		parent := filepath.Join(scope.ProjectDir, teamDir(), "evidence", "default")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(parent, scope.Session)); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(scope, true); err == nil {
			t.Fatal("symlink ancestor unexpectedly accepted")
		}
	})

	t.Run("component swap", func(t *testing.T) {
		scope := testScope(t)
		outside := t.TempDir()
		oldHook := storeContainmentHook
		fired := false
		storeContainmentHook = func(stage, path string) error {
			if stage == "after_component_validation" && filepath.Base(path) == "evidence" && !fired {
				fired = true
				if err := os.Rename(path, path+".original"); err != nil {
					return err
				}
				return os.Symlink(outside, path)
			}
			return nil
		}
		t.Cleanup(func() { storeContainmentHook = oldHook })
		if _, err := Open(scope, true); err == nil {
			t.Fatal("swapped ancestor unexpectedly accepted")
		}
	})
}

func TestStoreRejectsHardlinkAndLeafSwap(t *testing.T) {
	t.Run("hardlink", func(t *testing.T) {
		scope := testScope(t)
		store := openTestStore(t, scope)
		if _, err := store.Create(specForScope(scope)); err != nil {
			t.Fatal(err)
		}
		current := filepath.Join(store.dirPath, "current.json")
		if err := os.Link(current, current+".alias"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadCurrent(); err == nil || !strings.Contains(err.Error(), "link count") {
			t.Fatalf("hardlink error=%v", err)
		}
	})

	t.Run("leaf swap", func(t *testing.T) {
		scope := testScope(t)
		store := openTestStore(t, scope)
		if _, err := store.Create(specForScope(scope)); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside.json")
		if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		oldHook := storeContainmentHook
		fired := false
		storeContainmentHook = func(stage, path string) error {
			if stage == "after_leaf_validation" && filepath.Base(path) == "current.json" && !fired {
				fired = true
				if err := os.Rename(path, path+".original"); err != nil {
					return err
				}
				return os.Symlink(outside, path)
			}
			return nil
		}
		t.Cleanup(func() { storeContainmentHook = oldHook })
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("swapped leaf unexpectedly accepted")
		}
	})
}

func TestStoreRejectsMutableTargetSwapBeforeRename(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	oldHook := storeContainmentHook
	fired := false
	storeContainmentHook = func(stage, path string) error {
		if stage == "before_mutable_rename" && filepath.Base(path) == "current.json" && !fired {
			fired = true
			if err := os.Rename(path, path+".original"); err != nil {
				return err
			}
			return os.Symlink(path+".original", path)
		}
		return nil
	}
	t.Cleanup(func() { storeContainmentHook = oldHook })
	if _, err := store.StartPublishing(planned.Pointer.GenerationID); err == nil {
		t.Fatal("target swap unexpectedly accepted")
	}
}

func TestStoreWriteFaultsAreRecoverable(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	spec := specForScope(scope)
	oldSync := storeFileSync
	fired := false
	storeFileSync = func(f *os.File) error {
		if !fired {
			fired = true
			return errors.New("injected file sync failure")
		}
		return oldSync(f)
	}
	t.Cleanup(func() { storeFileSync = oldSync })
	if _, err := store.Create(spec); err == nil {
		t.Fatal("sync fault unexpectedly succeeded")
	}
	storeFileSync = oldSync
	if result, err := store.Create(spec); err != nil || result.Pointer.State != operatorauth.ReleaseStatePlanned {
		t.Fatalf("recovery Create()=(%+v,%v)", result, err)
	}
}

func TestImmutablePostLinkCrashIsExactlyRecoverable(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	oldFault := storeFault
	fired := false
	storeFault = func(stage string) error {
		if stage == "after_immutable_link" && !fired {
			fired = true
			return errors.New("crash immediately after immutable link")
		}
		return nil
	}
	t.Cleanup(func() { storeFault = oldFault })
	if _, err := store.Create(specForScope(scope)); err == nil {
		t.Fatal("post-link fault unexpectedly succeeded")
	}
	prepared, err := operatorauth.DerivePreparedRelease(specForScope(scope), 1)
	if err != nil {
		t.Fatal(err)
	}
	target := store.preparedName(1)
	b, _ := json.MarshalIndent(prepared, "", "  ")
	b = append(b, '\n')
	temp := immutableTempName(target, b)
	targetInfo, err := store.dir.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	tempInfo, err := store.dir.Lstat(temp)
	if err != nil {
		t.Fatal("crash temp was incorrectly cleaned:", err)
	}
	if !os.SameFile(targetInfo, tempInfo) {
		t.Fatal("crash target/temp do not retain one inode")
	}
	if links, _ := fileLinkCount(targetInfo); links != 2 {
		t.Fatalf("crash link count=%d", links)
	}
	storeFault = oldFault
	recovered, err := store.Create(specForScope(scope))
	if err != nil || recovered.Pointer.State != operatorauth.ReleaseStatePlanned {
		t.Fatalf("recovery=(%+v,%v)", recovered, err)
	}
	if _, err := store.dir.Lstat(temp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trusted temp remains after recovery: %v", err)
	}
	targetInfo, err = store.dir.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if links, _ := fileLinkCount(targetInfo); links != 1 {
		t.Fatalf("recovered link count=%d", links)
	}
}

func TestImmutableRecoveryFailsClosedForAmbiguity(t *testing.T) {
	for _, name := range []string{"extra temp", "mismatched digest"} {
		t.Run(name, func(t *testing.T) {
			scope := testScope(t)
			store := openTestStore(t, scope)
			prepared, err := operatorauth.DerivePreparedRelease(specForScope(scope), 1)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.MarshalIndent(prepared, "", "  ")
			b = append(b, '\n')
			target := store.preparedName(1)
			switch name {
			case "extra temp":
				if err := os.WriteFile(filepath.Join(store.dirPath, ".release-immutable-unjournaled.tmp"), []byte("other"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "mismatched digest":
				temp := immutableTempName(target, b)
				if err := os.WriteFile(filepath.Join(store.dirPath, temp), []byte("wrong"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(store.dirPath, temp), filepath.Join(store.dirPath, target)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Create(specForScope(scope)); err == nil {
				t.Fatalf("%s unexpectedly recovered", name)
			}
		})
	}
}

func TestImmutableEqualReplaySyncsDirectory(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	created, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	oldSync := storeDirectorySync
	called := false
	storeDirectorySync = func(*os.File) error { called = true; return errors.New("directory sync proof") }
	t.Cleanup(func() { storeDirectorySync = oldSync })
	if err := store.writePrepared(created.Prepared); err == nil || !called {
		t.Fatalf("equal replay did not fsync directory: called=%t err=%v", called, err)
	}
}

func TestStoreActivateVersusSuccessorRace(t *testing.T) {
	for i := 0; i < 20; i++ {
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
		next := specForScope(scope)
		next.TagTarget, next.GitHubReleaseTarget = "v2.20.2", "release v2.20.2 from exact commit cafe1234"
		var wg sync.WaitGroup
		var activateErr, successorErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, activateErr = store.Activate(active) }()
		go func() { defer wg.Done(); _, successorErr = store.PrepareSuccessor(first.Pointer.GenerationID, next) }()
		wg.Wait()
		if successorErr != nil {
			t.Fatalf("successor lost race: %v (activate=%v)", successorErr, activateErr)
		}
		current, err := store.ReadCurrent()
		if err != nil || current.Pointer.Generation != 2 || current.Pointer.State != operatorauth.ReleaseStatePlanned {
			t.Fatalf("race current=(%+v,%v)", current, err)
		}
	}
}

func TestStoreStrictPointerDecode(t *testing.T) {
	for name, corrupt := range map[string]func(string) string{
		"unknown": func(s string) string {
			return strings.Replace(s, `"schema_version": 1`, `"schema_version": 1, "unknown": true`, 1)
		},
		"duplicate": func(s string) string { return strings.Replace(s, `"revision": 1`, `"revision": 1, "revision": 2`, 1) },
		"trailing":  func(s string) string { return s + `{}` },
	} {
		t.Run(name, func(t *testing.T) {
			scope := testScope(t)
			store := openTestStore(t, scope)
			if _, err := store.Create(specForScope(scope)); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.dirPath, "current.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(corrupt(string(b))), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReadCurrent(); err == nil {
				t.Fatalf("%s pointer corruption accepted", name)
			}
		})
	}
}

func TestStoreRejectsGenerationRecordDivergence(t *testing.T) {
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dirPath, store.generationName(1))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record generationRecord
	if err := operatorauth.DecodeStrictJSON(b, &record); err != nil {
		t.Fatal(err)
	}
	record.PreparedSHA256 = "sha256:" + strings.Repeat("0", 64)
	corrupt, _ := json.MarshalIndent(record, "", "  ")
	if err := os.WriteFile(path, append(corrupt, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartPublishing(planned.Pointer.GenerationID); err == nil {
		t.Fatal("corrupt generation record entered publishing")
	}
}

func overwriteStoreJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func storeAtGeneration(t *testing.T, generation uint64) (Scope, *Store, Snapshot) {
	t.Helper()
	scope := testScope(t)
	store := openTestStore(t, scope)
	current, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	for next := uint64(2); next <= generation; next++ {
		spec := specForScope(scope)
		spec.TagTarget = fmt.Sprintf("v2.20.%d", next)
		spec.GitHubReleaseTarget = fmt.Sprintf("release v2.20.%d from exact commit %08d", next, next)
		current, err = store.PrepareSuccessor(current.Pointer.GenerationID, spec)
		if err != nil {
			t.Fatalf("prepare generation %d: %v", next, err)
		}
	}
	return scope, store, current
}

func TestStoreExactImmediatePredecessorChain(t *testing.T) {
	t.Run("generation one has no predecessor record", func(t *testing.T) {
		_, store, current := storeAtGeneration(t, 1)
		if _, err := store.ReadCurrent(); err != nil {
			t.Fatalf("valid generation one: %v", err)
		}
		if err := os.WriteFile(filepath.Join(store.dirPath, store.generationName(0)), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatalf("generation one accepted prior record: %+v", current)
		}
	})

	t.Run("generation two reciprocal chain", func(t *testing.T) {
		_, store, current := storeAtGeneration(t, 2)
		read, err := store.ReadCurrent()
		if err != nil || read.Pointer.Generation != 2 || read.Pointer.PredecessorGenerationID == "" || read.Pointer.PredecessorGenerationID != current.Pointer.PredecessorGenerationID {
			t.Fatalf("valid generation two=(%+v,%v)", read, err)
		}
	})

	t.Run("multi generation reciprocal chain", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 5)
		read, err := store.ReadCurrent()
		if err != nil || read.Pointer.Generation != 5 {
			t.Fatalf("valid generation five=(%+v,%v)", read, err)
		}
		prior, err := store.readGeneration(4)
		if err != nil || read.Pointer.PredecessorGenerationID != prior.GenerationID || prior.SuccessorGenerationID != read.Pointer.GenerationID {
			t.Fatalf("generation five chain current=%+v prior=%+v err=%v", read.Pointer, prior, err)
		}
	})

	t.Run("missing prior record", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 2)
		if err := os.Remove(filepath.Join(store.dirPath, store.generationName(1))); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("generation two accepted missing predecessor")
		}
	})

	t.Run("missing prior prepared manifest", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 2)
		if err := os.Remove(filepath.Join(store.dirPath, store.preparedName(1))); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("generation two accepted missing predecessor prepared manifest")
		}
	})

	t.Run("corrupt prior record", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 2)
		prior, err := store.readGeneration(1)
		if err != nil {
			t.Fatal(err)
		}
		prior.PreparedSHA256 = "sha256:" + strings.Repeat("0", 64)
		overwriteStoreJSON(t, filepath.Join(store.dirPath, store.generationName(1)), prior)
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("generation two accepted corrupt predecessor")
		}
	})

	t.Run("corrupt prior active manifest", func(t *testing.T) {
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
		next := specForScope(scope)
		next.TagTarget, next.GitHubReleaseTarget = "v2.20.2", "release v2.20.2 from exact commit cafe1234"
		if _, err := store.PrepareSuccessor(first.Pointer.GenerationID, next); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.dirPath, store.activeName(1)), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("generation two accepted corrupt predecessor active manifest")
		}
	})

	t.Run("jointly forged current pointer and record", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 2)
		pointer, err := store.readPointer()
		if err != nil {
			t.Fatal(err)
		}
		current, err := store.readGeneration(2)
		if err != nil {
			t.Fatal(err)
		}
		pointer.PredecessorGenerationID = "release-generation-v1-" + strings.Repeat("f", 64)
		current.PredecessorGenerationID = pointer.PredecessorGenerationID
		overwriteStoreJSON(t, filepath.Join(store.dirPath, "current.json"), pointer)
		overwriteStoreJSON(t, filepath.Join(store.dirPath, store.generationName(2)), current)
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("jointly forged predecessor binding accepted")
		}
	})

	t.Run("forged prior reciprocal successor", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 2)
		prior, err := store.readGeneration(1)
		if err != nil {
			t.Fatal(err)
		}
		prior.SuccessorGenerationID = "release-generation-v1-" + strings.Repeat("e", 64)
		overwriteStoreJSON(t, filepath.Join(store.dirPath, store.generationName(1)), prior)
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("forged predecessor reciprocal binding accepted")
		}
	})

	t.Run("nonterminal prior", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 2)
		prior, err := store.readGeneration(1)
		if err != nil {
			t.Fatal(err)
		}
		prior.State = operatorauth.ReleaseStatePlanned
		prior.SuccessorGenerationID = ""
		overwriteStoreJSON(t, filepath.Join(store.dirPath, store.generationName(1)), prior)
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("nonterminal predecessor accepted")
		}
	})

	t.Run("skipped generation", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 3)
		pointer, err := store.readPointer()
		if err != nil {
			t.Fatal(err)
		}
		current, err := store.readGeneration(3)
		if err != nil {
			t.Fatal(err)
		}
		first, err := store.readGeneration(1)
		if err != nil {
			t.Fatal(err)
		}
		pointer.PredecessorGenerationID, current.PredecessorGenerationID = first.GenerationID, first.GenerationID
		overwriteStoreJSON(t, filepath.Join(store.dirPath, "current.json"), pointer)
		overwriteStoreJSON(t, filepath.Join(store.dirPath, store.generationName(3)), current)
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("skipped predecessor generation accepted")
		}
	})

	t.Run("cyclic current predecessor", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 2)
		pointer, err := store.readPointer()
		if err != nil {
			t.Fatal(err)
		}
		current, err := store.readGeneration(2)
		if err != nil {
			t.Fatal(err)
		}
		pointer.PredecessorGenerationID, current.PredecessorGenerationID = current.GenerationID, current.GenerationID
		overwriteStoreJSON(t, filepath.Join(store.dirPath, "current.json"), pointer)
		overwriteStoreJSON(t, filepath.Join(store.dirPath, store.generationName(2)), current)
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("cyclic predecessor binding accepted")
		}
	})

	t.Run("cyclic predecessor relation inside prior record", func(t *testing.T) {
		_, store, _ := storeAtGeneration(t, 3)
		prior, err := store.readGeneration(2)
		if err != nil {
			t.Fatal(err)
		}
		prior.PredecessorGenerationID = prior.GenerationID
		overwriteStoreJSON(t, filepath.Join(store.dirPath, store.generationName(2)), prior)
		if _, err := store.ReadCurrent(); err == nil {
			t.Fatal("cyclic predecessor relation inside actual prior record accepted")
		}
	})
}

func TestStoreConcurrentSuccessorPreservesExactChain(t *testing.T) {
	for iteration := 0; iteration < 10; iteration++ {
		scope, store, first := storeAtGeneration(t, 1)
		next := specForScope(scope)
		next.TagTarget, next.GitHubReleaseTarget = "v2.20.2", "release v2.20.2 from exact commit cafe1234"
		results := make(chan Snapshot, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for worker := 0; worker < 2; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, err := store.PrepareSuccessor(first.Pointer.GenerationID, next)
				results <- result
				errs <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("iteration %d concurrent successor: %v", iteration, err)
			}
		}
		for result := range results {
			if result.Pointer.Generation != 2 || result.Pointer.PredecessorGenerationID != first.Pointer.GenerationID {
				t.Fatalf("iteration %d concurrent result=%+v", iteration, result.Pointer)
			}
		}
		if _, err := store.ReadCurrent(); err != nil {
			t.Fatalf("iteration %d current chain: %v", iteration, err)
		}
	}
}

func TestReleaseLifecycleValidatorExhaustiveTopStates(t *testing.T) {
	scope := testScope(t)
	prepared, err := operatorauth.DerivePreparedRelease(specForScope(scope), 1)
	if err != nil {
		t.Fatal(err)
	}
	active, err := operatorauth.NewActiveRelease(prepared, observedReceipts(scope, prepared, ""))
	if err != nil {
		t.Fatal(err)
	}
	baseRecord := newGenerationRecord(seriesIdentity(scope), prepared, "")
	basePointer := pointerFromRecord(baseRecord, 1)
	tests := []struct {
		name       string
		state      string
		active     bool
		successor  bool
		childState []string
	}{
		{name: "planned", state: operatorauth.ReleaseStatePlanned, childState: []string{childPublicationPlanned, childPublicationPlanned}},
		{name: "publishing future substates", state: operatorauth.ReleaseStatePublishing, childState: []string{childPublicationSending, childPublicationPublished}},
		{name: "active", state: operatorauth.ReleaseStateActive, active: true, childState: []string{childPublicationAdopted, childPublicationAdopted}},
		{name: "superseded inactive", state: operatorauth.ReleaseStateSuperseded, successor: true, childState: []string{childPublicationPublished, childPublicationPlanned}},
		{name: "superseded active", state: operatorauth.ReleaseStateSuperseded, active: true, successor: true, childState: []string{childPublicationAdopted, childPublicationAdopted}},
		{name: "aborted", state: operatorauth.ReleaseStateAborted, childState: []string{childPublicationPublished, childPublicationPlanned}},
		{name: "conflict", state: operatorauth.ReleaseStateConflict, childState: []string{childPublicationConflict, childPublicationPublished}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record, pointer := baseRecord, basePointer
			record.Children = append([]childPublicationRecord(nil), baseRecord.Children...)
			record.State, pointer.State = tc.state, tc.state
			var activeArg *operatorauth.ActiveReleaseManifest
			if tc.active {
				record.ActiveManifestID, pointer.ActiveManifestID = active.ActiveManifestID, active.ActiveManifestID
				record.ActiveSHA256, pointer.ActiveSHA256 = operatorauth.ActiveReleaseSHA256(active), operatorauth.ActiveReleaseSHA256(active)
				record.Children = adoptedChildRecords(publishedChildRecordsForTest(prepared, active), prepared, active)
				activeArg = &active
			}
			if tc.successor {
				record.SuccessorGenerationID, pointer.SuccessorGenerationID = "release-generation-v1-"+strings.Repeat("a", 64), "release-generation-v1-"+strings.Repeat("a", 64)
			}
			for i, state := range tc.childState {
				record.Children[i].State = state
				if state == childPublicationSending || state == childPublicationPublished {
					record.Children[i].ClaimRevision = 1
					record.Children[i].ClaimToken = "release-claim-v1-" + strings.Repeat(string(rune('a'+i)), 64)
				}
				if state == childPublicationPublished {
					record.Children[i].QuestionMessageID = active.Children[i].QuestionMessageID
					record.Children[i].ReceiptPath = active.Children[i].Receipt.Path
					record.Children[i].ReceiptSHA256, _ = operatorauth.ReleaseDeliveryReceiptSHA256(active.Children[i].Receipt)
					receipt := cloneDeliveryReceipt(active.Children[i].Receipt)
					record.Children[i].Receipt = &receipt
				}
				if state == childPublicationConflict {
					record.Children[i].ConflictReason = "conflicting exact evidence"
					record.Children[i].ObservedMessageIDs = []string{"message-conflict"}
				}
			}
			if err := validateLifecycleExact(seriesIdentity(scope), pointer, record, prepared, activeArg); err != nil {
				t.Fatalf("valid %s lifecycle: %v", tc.name, err)
			}
		})
	}
}

func TestReleaseLifecycleValidatorRejectsAbsentOrDivergentBindings(t *testing.T) {
	scope := testScope(t)
	prepared, _ := operatorauth.DerivePreparedRelease(specForScope(scope), 1)
	active, _ := operatorauth.NewActiveRelease(prepared, observedReceipts(scope, prepared, ""))
	record := newGenerationRecord(seriesIdentity(scope), prepared, "")
	record.State = operatorauth.ReleaseStateActive
	record.ActiveManifestID, record.ActiveSHA256 = active.ActiveManifestID, operatorauth.ActiveReleaseSHA256(active)
	record.Children = adoptedChildRecords(publishedChildRecordsForTest(prepared, active), prepared, active)
	pointer := pointerFromRecord(record, 3)
	for name, mutate := range map[string]func(*Pointer, *generationRecord){
		"pointer schema":        func(p *Pointer, _ *generationRecord) { p.SchemaVersion++ },
		"record schema":         func(_ *Pointer, r *generationRecord) { r.SchemaVersion++ },
		"pointer series":        func(p *Pointer, _ *generationRecord) { p.SeriesID = "other" },
		"record series":         func(_ *Pointer, r *generationRecord) { r.SeriesID = "other" },
		"pointer generation":    func(p *Pointer, _ *generationRecord) { p.Generation++ },
		"record generation":     func(_ *Pointer, r *generationRecord) { r.Generation++ },
		"pointer generation id": func(p *Pointer, _ *generationRecord) { p.GenerationID = "" },
		"record generation id":  func(_ *Pointer, r *generationRecord) { r.GenerationID = "" },
		"pointer prepared id":   func(p *Pointer, _ *generationRecord) { p.PreparedManifestID = "" },
		"record prepared id":    func(_ *Pointer, r *generationRecord) { r.PreparedManifestID = "" },
		"pointer prepared hash": func(p *Pointer, _ *generationRecord) { p.PreparedSHA256 = "" },
		"record prepared hash":  func(_ *Pointer, r *generationRecord) { r.PreparedSHA256 = "" },
		"pointer state":         func(p *Pointer, _ *generationRecord) { p.State = operatorauth.ReleaseStatePublishing },
		"empty pointer active":  func(p *Pointer, _ *generationRecord) { p.ActiveManifestID = "" },
		"empty record active":   func(_ *Pointer, r *generationRecord) { r.ActiveSHA256 = "" },
		"wrong exact active": func(p *Pointer, r *generationRecord) {
			p.ActiveManifestID, r.ActiveManifestID = "wrong", "wrong"
		},
		"active successor": func(p *Pointer, r *generationRecord) {
			p.SuccessorGenerationID, r.SuccessorGenerationID = "next", "next"
		},
		"generation one predecessor": func(p *Pointer, r *generationRecord) {
			p.PredecessorGenerationID, r.PredecessorGenerationID = "previous", "previous"
		},
		"successor divergence": func(p *Pointer, r *generationRecord) {
			p.State, r.State = operatorauth.ReleaseStateSuperseded, operatorauth.ReleaseStateSuperseded
			p.SuccessorGenerationID = "next-a"
			r.SuccessorGenerationID = "next-b"
		},
		"child zero role":             func(_ *Pointer, r *generationRecord) { r.Children[0].Role = "other" },
		"child zero ordinal":          func(_ *Pointer, r *generationRecord) { r.Children[0].Ordinal++ },
		"child zero attempt":          func(_ *Pointer, r *generationRecord) { r.Children[0].AttemptID = "other" },
		"child zero state":            func(_ *Pointer, r *generationRecord) { r.Children[0].State = childPublicationPublished },
		"child zero question missing": func(_ *Pointer, r *generationRecord) { r.Children[0].QuestionMessageID = "" },
		"child zero receipt missing":  func(_ *Pointer, r *generationRecord) { r.Children[0].ReceiptPath = "" },
		"child one role":              func(_ *Pointer, r *generationRecord) { r.Children[1].Role = "other" },
		"child one ordinal":           func(_ *Pointer, r *generationRecord) { r.Children[1].Ordinal++ },
		"child one attempt":           func(_ *Pointer, r *generationRecord) { r.Children[1].AttemptID = "other" },
		"child one state":             func(_ *Pointer, r *generationRecord) { r.Children[1].State = childPublicationPublished },
		"child one question missing":  func(_ *Pointer, r *generationRecord) { r.Children[1].QuestionMessageID = "" },
		"child one receipt missing":   func(_ *Pointer, r *generationRecord) { r.Children[1].ReceiptPath = "" },
		"active child receipt digest": func(_ *Pointer, r *generationRecord) {
			r.Children[0].ReceiptSHA256 = "sha256:" + strings.Repeat("0", 64)
		},
		"active child receipt tuple": func(_ *Pointer, r *generationRecord) {
			r.Children[0].Receipt.Root += "-other"
		},
		"active child receipt tuple and digest": func(_ *Pointer, r *generationRecord) {
			r.Children[0].Receipt.Root += "-other"
			r.Children[0].ReceiptSHA256, _ = operatorauth.ReleaseDeliveryReceiptSHA256(*r.Children[0].Receipt)
		},
		"superseded active child receipt digest": func(p *Pointer, r *generationRecord) {
			p.State, r.State = operatorauth.ReleaseStateSuperseded, operatorauth.ReleaseStateSuperseded
			p.SuccessorGenerationID, r.SuccessorGenerationID = "next", "next"
			r.Children[1].ReceiptSHA256 = "sha256:" + strings.Repeat("0", 64)
		},
		"superseded active child receipt tuple and digest": func(p *Pointer, r *generationRecord) {
			p.State, r.State = operatorauth.ReleaseStateSuperseded, operatorauth.ReleaseStateSuperseded
			p.SuccessorGenerationID, r.SuccessorGenerationID = "next", "next"
			r.Children[1].Receipt.Root += "-other"
			r.Children[1].ReceiptSHA256, _ = operatorauth.ReleaseDeliveryReceiptSHA256(*r.Children[1].Receipt)
		},
		"child missing": func(_ *Pointer, r *generationRecord) { r.Children = r.Children[:1] },
		"child extra": func(_ *Pointer, r *generationRecord) {
			r.Children = append(r.Children, r.Children[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, r := pointer, record
			r.Children = cloneChildPublicationRecords(record.Children)
			mutate(&p, &r)
			if err := validateLifecycleExact(seriesIdentity(scope), p, r, prepared, &active); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}

	t.Run("generation two predecessor absent", func(t *testing.T) {
		preparedTwo, err := operatorauth.DerivePreparedRelease(specForScope(scope), 2)
		if err != nil {
			t.Fatal(err)
		}
		recordTwo := newGenerationRecord(seriesIdentity(scope), preparedTwo, "previous")
		pointerTwo := pointerFromRecord(recordTwo, 1)
		recordTwo.PredecessorGenerationID, pointerTwo.PredecessorGenerationID = "", ""
		if err := validateLifecycleExact(seriesIdentity(scope), pointerTwo, recordTwo, preparedTwo, nil); err == nil {
			t.Fatal("generation two without predecessor accepted")
		}
	})

	t.Run("superseded successor absent", func(t *testing.T) {
		recordSuperseded := newGenerationRecord(seriesIdentity(scope), prepared, "")
		recordSuperseded.State = operatorauth.ReleaseStateSuperseded
		pointerSuperseded := pointerFromRecord(recordSuperseded, 2)
		if err := validateLifecycleExact(seriesIdentity(scope), pointerSuperseded, recordSuperseded, prepared, nil); err == nil {
			t.Fatal("superseded lifecycle without successor accepted")
		}
	})
}

func cloneChildPublicationRecords(records []childPublicationRecord) []childPublicationRecord {
	cloned := append([]childPublicationRecord(nil), records...)
	for i := range cloned {
		if cloned[i].Receipt != nil {
			receipt := cloneDeliveryReceipt(*cloned[i].Receipt)
			cloned[i].Receipt = &receipt
		}
		cloned[i].ObservedMessageIDs = append([]string(nil), cloned[i].ObservedMessageIDs...)
	}
	return cloned
}

func TestPointerAheadSupersessionIsTheOnlyRepairException(t *testing.T) {
	scope := testScope(t)
	prepared, _ := operatorauth.DerivePreparedRelease(specForScope(scope), 1)
	active, _ := operatorauth.NewActiveRelease(prepared, observedReceipts(scope, prepared, ""))
	record := newGenerationRecord(seriesIdentity(scope), prepared, "")
	record.State = operatorauth.ReleaseStateActive
	record.ActiveManifestID, record.ActiveSHA256 = active.ActiveManifestID, operatorauth.ActiveReleaseSHA256(active)
	record.Children = adoptedChildRecords(publishedChildRecordsForTest(prepared, active), prepared, active)
	pointer := pointerFromRecord(record, 3)
	pointer.State = operatorauth.ReleaseStateSuperseded
	pointer.SuccessorGenerationID = "release-generation-v1-" + strings.Repeat("b", 64)
	if err := validateLifecycleExact(seriesIdentity(scope), pointer, record, prepared, &active); err == nil {
		t.Fatal("pointer-ahead snapshot accepted by exact validator")
	}
	if err := validatePointerAheadSupersession(seriesIdentity(scope), pointer, record, prepared, &active); err != nil {
		t.Fatalf("single pointer-ahead repair rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Pointer, *generationRecord){
		"missing pointer successor": func(p *Pointer, _ *generationRecord) { p.SuccessorGenerationID = "" },
		"record already successor":  func(_ *Pointer, r *generationRecord) { r.SuccessorGenerationID = "different" },
		"record already superseded": func(_ *Pointer, r *generationRecord) { r.State = operatorauth.ReleaseStateSuperseded },
		"active receipt digest": func(_ *Pointer, r *generationRecord) {
			r.Children[0].ReceiptSHA256 = "sha256:" + strings.Repeat("0", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, r := pointer, record
			r.Children = append([]childPublicationRecord(nil), record.Children...)
			mutate(&p, &r)
			if err := validatePointerAheadSupersession(seriesIdentity(scope), p, r, prepared, &active); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

func TestMutableCrashSubprocessHelper(t *testing.T) {
	phase := os.Getenv(mutableCrashEnv)
	if phase == "" {
		return
	}
	scope := Scope{ProjectDir: os.Getenv(mutableCrashEnv + "_PROJECT"), Profile: "default", Session: "issue-414", NamespaceGeneration: "none", ParentGate: "gate/release-414"}
	store, err := Open(scope, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seenCurrent := 0
	storeFault = func(stage string) error {
		match := false
		switch phase {
		case "planned", "publishing_generation", "active_generation", "successor_old_record":
			match = stage == "after_mutable_file_sync:"+store.generationName(1)
		case "successor_new_record":
			match = stage == "after_mutable_file_sync:"+store.generationName(2)
		case "successor_pointer_published":
			match = stage == "after_mutable_rename:current.json"
		case "publishing_pointer", "active_pointer", "successor_pointer_first", "successor_pointer_second":
			if stage == "after_mutable_file_sync:current.json" {
				seenCurrent++
			}
			want := 1
			if phase == "successor_pointer_second" {
				want = 2
			}
			match = seenCurrent == want
		}
		if match {
			os.Exit(91)
		}
		return nil
	}
	firstSpec := specForScope(scope)
	switch phase {
	case "planned":
		_, err = store.Create(firstSpec)
	case "publishing_generation", "publishing_pointer":
		prepared, _ := operatorauth.DerivePreparedRelease(firstSpec, 1)
		_, err = store.StartPublishing(prepared.GenerationID)
	case "active_generation", "active_pointer":
		current, readErr := store.ReadCurrent()
		if readErr != nil {
			t.Fatal(readErr)
		}
		active, buildErr := operatorauth.NewActiveRelease(current.Prepared, observedReceipts(scope, current.Prepared, ""))
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		_, err = store.Activate(active)
	default:
		next := firstSpec
		next.TagTarget, next.GitHubReleaseTarget = "v2.20.2", "release v2.20.2 from exact commit cafe1234"
		prepared, _ := operatorauth.DerivePreparedRelease(firstSpec, 1)
		_, err = store.PrepareSuccessor(prepared.GenerationID, next)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash hook did not terminate subprocess")
}

func TestMutableRealCrashRecoveryAcrossLifecycle(t *testing.T) {
	phases := []string{"planned", "publishing_generation", "publishing_pointer", "active_generation", "active_pointer", "successor_pointer_first", "successor_pointer_published", "successor_old_record", "successor_new_record", "successor_pointer_second"}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			scope := testScope(t)
			store := openTestStore(t, scope)
			firstSpec := specForScope(scope)
			prepared, _ := operatorauth.DerivePreparedRelease(firstSpec, 1)
			if phase != "planned" {
				if _, err := store.Create(firstSpec); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(phase, "active_") || strings.HasPrefix(phase, "successor_") {
				publishing, err := store.StartPublishing(prepared.GenerationID)
				if err != nil {
					t.Fatal(err)
				}
				receipts := stagePublishedChildrenForTest(t, store, scope, publishing, "")
				if strings.HasPrefix(phase, "successor_") {
					active, _ := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
					if _, err := store.Activate(active); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestMutableCrashSubprocessHelper$")
			cmd.Env = append(os.Environ(), mutableCrashEnv+"="+phase, mutableCrashEnv+"_PROJECT="+scope.ProjectDir)
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 91 {
				t.Fatalf("crash subprocess error=%v", err)
			}
			store, err = Open(scope, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			switch {
			case phase == "planned":
				if _, err := store.Create(firstSpec); err != nil {
					t.Fatal(err)
				}
			case strings.HasPrefix(phase, "publishing_"):
				if _, err := store.StartPublishing(prepared.GenerationID); err != nil {
					t.Fatal(err)
				}
			case strings.HasPrefix(phase, "active_"):
				preparedCurrent, err := store.readPrepared(1)
				if err != nil {
					t.Fatal(err)
				}
				active, _ := operatorauth.NewActiveRelease(preparedCurrent, observedReceipts(scope, preparedCurrent, ""))
				if _, err := store.Activate(active); err != nil {
					t.Fatal(err)
				}
			default:
				next := firstSpec
				next.TagTarget, next.GitHubReleaseTarget = "v2.20.2", "release v2.20.2 from exact commit cafe1234"
				if _, err := store.PrepareSuccessor(prepared.GenerationID, next); err != nil {
					t.Fatal(err)
				}
			}
			if temps, err := store.mutableTemps(); err != nil || len(temps) != 0 {
				t.Fatalf("mutable temps after recovery=%v err=%v", temps, err)
			}
		})
	}
}

func teamDir() string { return ".amq-squad" }
