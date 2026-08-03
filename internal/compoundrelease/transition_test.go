package compoundrelease

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

func publishingStore(t *testing.T) (*Store, Snapshot) {
	t.Helper()
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
	return store, publishing
}

func TestClaimChildSendPersistsExactLiveCapability(t *testing.T) {
	store, publishing := publishingStore(t)
	claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Role != operatorauth.ReleaseChildTag || claim.Ordinal != 0 || claim.AttemptID != publishing.Prepared.Children[0].ReleaseChild.AttemptID || claim.Revision != 1 || !validClaimToken(claim.Token) {
		t.Fatalf("claim=%+v", claim)
	}
	record, err := store.readGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	if record.Children[0].State != childPublicationSending || record.Children[0].ClaimRevision != claim.Revision || record.Children[0].ClaimToken != claim.Token {
		t.Fatalf("durable claim=%+v", record.Children[0])
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err == nil {
		t.Fatal("sending child was claimed twice")
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err == nil {
		t.Fatal("github release child was claimed before stable tag evidence")
	}
}

func TestChildSendRollbackRequiresMatchingLivePreProcessProof(t *testing.T) {
	store, publishing := publishingStore(t)
	first, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.rollbackChildSend(first, noInvocationEvidence{claimToken: first.Token, processStarted: true}); err == nil {
		t.Fatal("process-started claim rolled back")
	}
	if err := store.rollbackChildSend(first, noInvocationEvidence{claimToken: first.Token, invocationBegan: true}); err == nil {
		t.Fatal("invocation-begun claim rolled back")
	}
	if err := store.rollbackChildSend(first, noInvocationEvidence{}); err == nil {
		t.Fatal("persisted state without live proof rolled back")
	}
	if err := store.rollbackChildSend(first, definitelyUninvokedEvidence(first)); err != nil {
		t.Fatalf("live pre-process rollback: %v", err)
	}
	record, _ := store.readGeneration(1)
	if record.Children[0].State != childPublicationPlanned || record.Children[0].ClaimToken != "" || record.Children[0].ClaimRevision != 1 {
		t.Fatalf("rolled back child=%+v", record.Children[0])
	}
	second, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.Token == first.Token {
		t.Fatalf("second claim=%+v first=%+v", second, first)
	}
	if err := store.rollbackChildSend(first, definitelyUninvokedEvidence(first)); err == nil {
		t.Fatal("stale live capability rolled back newer claim")
	}
}

func TestConcurrentChildClaimHasSingleWinner(t *testing.T) {
	store, publishing := publishingStore(t)
	const workers = 12
	var wg sync.WaitGroup
	results := make(chan ChildSendClaim, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
			results <- claim
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	winners := 0
	for err := range errs {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d", winners)
	}
}

func TestTerminalConflictPersistsBoundedSortedEvidence(t *testing.T) {
	store, publishing := publishingStore(t)
	claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "multiple exact mailbox messages", []string{"m-2", "m-1", "m-2"}); err != nil {
		t.Fatal(err)
	}
	pointer, err := store.readPointer()
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.readGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	child := record.Children[0]
	if pointer.State != operatorauth.ReleaseStateConflict || record.State != operatorauth.ReleaseStateConflict || child.State != childPublicationConflict || child.ClaimToken != claim.Token || child.ConflictReason != "multiple exact mailbox messages" || len(child.ObservedMessageIDs) != 2 || child.ObservedMessageIDs[0] != "m-1" || child.ObservedMessageIDs[1] != "m-2" {
		t.Fatalf("pointer=%+v child=%+v", pointer, child)
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "multiple exact mailbox messages", []string{"m-1", "m-2"}); err != nil {
		t.Fatalf("idempotent conflict: %v", err)
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "different conflict", []string{"m-1", "m-2"}); err == nil {
		t.Fatal("different conflict reason accepted after terminalization")
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 1, "multiple exact mailbox messages", []string{"m-1", "m-2"}); err == nil {
		t.Fatal("different conflict ordinal accepted after terminalization")
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "multiple exact mailbox messages", []string{"m-3"}); err == nil {
		t.Fatal("different observed IDs accepted after terminalization")
	}
}

func TestTerminalConflictPreservesPublicationAndRollbackHistory(t *testing.T) {
	t.Run("published provenance", func(t *testing.T) {
		store, publishing := publishingStore(t)
		messageID := observedReceipts(store.scope, publishing.Prepared, "")[operatorauth.ReleaseChildTag]
		claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, messageID); err != nil {
			t.Fatal(err)
		}
		if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "duplicate exact publication", []string{"m-2", "m-1"}); err != nil {
			t.Fatal(err)
		}
		record, err := store.readGeneration(1)
		if err != nil {
			t.Fatal(err)
		}
		child := record.Children[0]
		if child.State != childPublicationConflict || child.ClaimRevision != claim.Revision || child.ClaimToken != claim.Token || child.QuestionMessageID != messageID {
			t.Fatalf("published conflict lost exact provenance: %+v", child)
		}
		if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "duplicate exact publication", []string{"m-1", "m-2"}); err != nil {
			t.Fatalf("exact conflict replay: %v", err)
		}
	})

	t.Run("rolled back revision", func(t *testing.T) {
		store, publishing := publishingStore(t)
		claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.rollbackChildSend(claim, definitelyUninvokedEvidence(claim)); err != nil {
			t.Fatal(err)
		}
		if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "ambiguous recovery scan", []string{"m-1"}); err != nil {
			t.Fatal(err)
		}
		record, err := store.readGeneration(1)
		if err != nil {
			t.Fatal(err)
		}
		child := record.Children[0]
		if child.State != childPublicationConflict || child.ClaimRevision != claim.Revision || child.ClaimToken != "" {
			t.Fatalf("rolled-back conflict history: %+v", child)
		}
	})
}

func TestAdoptionEnforcesFixedOrderAndExactClaim(t *testing.T) {
	store, publishing := publishingStore(t)
	scope := store.scope
	receipts := observedReceipts(scope, publishing.Prepared, "")
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, receipts[operatorauth.ReleaseChildTag]); err == nil {
		t.Fatal("unclaimed tag evidence adopted")
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err == nil {
		t.Fatal("github release claimed before tag")
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, receipts[operatorauth.ReleaseChildTag]); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, receipts[operatorauth.ReleaseChildTag]); err != nil {
		t.Fatalf("identical message identity replay: %v", err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, "different-question"); err == nil {
		t.Fatal("changed message identity replay accepted")
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err != nil {
		t.Fatalf("github release claim after stable tag: %v", err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 1, receipts[operatorauth.ReleaseChildGitHubRelease]); err != nil {
		t.Fatal(err)
	}
	record, _ := store.readGeneration(1)
	if record.Children[0].State != childPublicationPublished || record.Children[1].State != childPublicationPublished {
		t.Fatalf("children=%+v", record.Children)
	}
}

func TestActivateRequiresTwoExactPublishedChildrenBeforeArtifact(t *testing.T) {
	for _, stage := range []string{"planned", "sending", "one-published", "mismatched-provenance"} {
		t.Run(stage, func(t *testing.T) {
			store, publishing := publishingStore(t)
			receipts := observedReceipts(store.scope, publishing.Prepared, "")
			switch stage {
			case "sending":
				if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
					t.Fatal(err)
				}
			case "one-published", "mismatched-provenance":
				if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
					t.Fatal(err)
				}
				if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, receipts[operatorauth.ReleaseChildTag]); err != nil {
					t.Fatal(err)
				}
				if stage == "mismatched-provenance" {
					if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err != nil {
						t.Fatal(err)
					}
					if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 1, receipts[operatorauth.ReleaseChildGitHubRelease]); err != nil {
						t.Fatal(err)
					}
					receipts[operatorauth.ReleaseChildTag] = "different-question"
				}
			}
			active, err := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Activate(active); err == nil {
				t.Fatalf("%s activation unexpectedly succeeded", stage)
			}
			if _, err := os.Stat(filepath.Join(store.dirPath, store.activeName(1))); !os.IsNotExist(err) {
				t.Fatalf("%s activation wrote active artifact: %v", stage, err)
			}
		})
	}
}

func TestActivateAfterTwoExactPublicationsPreservesClaims(t *testing.T) {
	store, publishing := publishingStore(t)
	receipts := observedReceipts(store.scope, publishing.Prepared, "")
	claims := make([]ChildSendClaim, 2)
	for ordinal, role := range []string{operatorauth.ReleaseChildTag, operatorauth.ReleaseChildGitHubRelease} {
		claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, ordinal)
		if err != nil {
			t.Fatal(err)
		}
		claims[ordinal] = claim
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, ordinal, receipts[role]); err != nil {
			t.Fatal(err)
		}
	}
	active, err := operatorauth.NewActiveRelease(publishing.Prepared, receipts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Activate(active)
	if err != nil || result.Pointer.State != operatorauth.ReleaseStateActive {
		t.Fatalf("Activate()=(%+v,%v)", result, err)
	}
	record, _ := store.readGeneration(1)
	for i, child := range record.Children {
		if child.State != childPublicationAdopted || child.ClaimToken != claims[i].Token || child.ClaimRevision != claims[i].Revision {
			t.Fatalf("adopted child[%d]=%+v", i, child)
		}
	}
}

func TestActivateRejectsChangedMessageIdentity(t *testing.T) {
	store, publishing := publishingStore(t)
	messageIDs := observedReceipts(store.scope, publishing.Prepared, "")
	for ordinal, role := range []string{operatorauth.ReleaseChildTag, operatorauth.ReleaseChildGitHubRelease} {
		if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, ordinal); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, ordinal, messageIDs[role]); err != nil {
			t.Fatal(err)
		}
	}
	messageIDs[operatorauth.ReleaseChildTag] = "different-question"
	active, err := operatorauth.NewActiveRelease(publishing.Prepared, messageIDs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(active); err == nil {
		t.Fatal("changed message identity activated")
	}
	if _, err := os.Stat(filepath.Join(store.dirPath, store.activeName(1))); !os.IsNotExist(err) {
		t.Fatalf("changed message identity wrote active artifact: %v", err)
	}
}

func TestTerminalConflictRejectsUnsafeReasonBeforeMutation(t *testing.T) {
	store, publishing := publishingStore(t)
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "bad\nreason", nil); err == nil {
		t.Fatal("control-bearing conflict reason accepted")
	}
	current, err := store.ReadCurrent()
	if err != nil || current.Pointer.State != operatorauth.ReleaseStatePublishing {
		t.Fatalf("current=(%+v,%v)", current, err)
	}
}

func TestConflictRecordAheadRecoveryRequiresExactEvidence(t *testing.T) {
	store, publishing := publishingStore(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	oldFault := storeFault
	fired := false
	storeFault = func(stage string) error {
		if stage == "after_conflict_record_write" && !fired {
			fired = true
			return errors.New("crash after conflict record")
		}
		return nil
	}
	t.Cleanup(func() { storeFault = oldFault })
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "duplicate messages", []string{"m-1", "m-2"}); err == nil {
		t.Fatal("faulted conflict unexpectedly succeeded")
	}
	storeFault = oldFault
	pointer, _ := store.readPointer()
	record, _ := store.readGeneration(1)
	if pointer.State != operatorauth.ReleaseStatePublishing || record.State != operatorauth.ReleaseStateConflict {
		t.Fatalf("record-ahead pointer=%+v record=%+v", pointer, record)
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "different", []string{"m-1", "m-2"}); err == nil {
		t.Fatal("different record-ahead conflict evidence accepted")
	}
	pointer, _ = store.readPointer()
	if pointer.State != operatorauth.ReleaseStatePublishing {
		t.Fatalf("mismatched recovery advanced pointer: %+v", pointer)
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "duplicate messages", []string{"m-2", "m-1"}); err != nil {
		t.Fatalf("exact record-ahead recovery: %v", err)
	}
	pointer, _ = store.readPointer()
	if pointer.State != operatorauth.ReleaseStateConflict {
		t.Fatalf("exact recovery pointer=%+v", pointer)
	}
}

func TestPublishedConflictRecordAheadRecoveryPreservesMessageIdentity(t *testing.T) {
	store, publishing := publishingStore(t)
	messageID := observedReceipts(store.scope, publishing.Prepared, "")[operatorauth.ReleaseChildTag]
	claim, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, messageID); err != nil {
		t.Fatal(err)
	}

	oldFault := storeFault
	fired := false
	storeFault = func(stage string) error {
		if stage == "after_conflict_record_write" && !fired {
			fired = true
			return errors.New("crash after published conflict record")
		}
		return nil
	}
	t.Cleanup(func() { storeFault = oldFault })
	reason, ids := "duplicate published messages", []string{"m-2", "m-1"}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, reason, ids); err == nil {
		t.Fatal("faulted published conflict unexpectedly succeeded")
	}
	storeFault = oldFault

	pointer, err := store.readPointer()
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.readGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	child := record.Children[0]
	if pointer.State != operatorauth.ReleaseStatePublishing || record.State != operatorauth.ReleaseStateConflict || child.State != childPublicationConflict || child.QuestionMessageID != messageID || child.ClaimRevision != claim.Revision || child.ClaimToken != claim.Token || child.ConflictReason != reason || !slices.Equal(child.ObservedMessageIDs, []string{"m-1", "m-2"}) {
		t.Fatalf("record-ahead published conflict lost exact evidence: pointer=%+v child=%+v", pointer, child)
	}
	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, "different conflict", ids); err == nil {
		t.Fatal("divergent record-ahead conflict replay accepted")
	}

	if err := store.TerminalizeChildConflict(publishing.Pointer.GenerationID, 0, reason, ids); err != nil {
		t.Fatalf("exact published conflict recovery: %v", err)
	}
	current, err := store.ReadCurrent()
	if err != nil {
		t.Fatalf("strict reread after pointer repair: %v", err)
	}
	child = mustGenerationRecord(t, store, 1).Children[0]
	if current.Pointer.State != operatorauth.ReleaseStateConflict || child.QuestionMessageID != messageID || child.ClaimToken != claim.Token || child.ConflictReason != reason || !slices.Equal(child.ObservedMessageIDs, []string{"m-1", "m-2"}) {
		t.Fatalf("repaired conflict reread lost exact evidence: current=%+v child=%+v", current.Pointer, child)
	}
}

func mustGenerationRecord(t *testing.T, store *Store, generation uint64) generationRecord {
	t.Helper()
	record, err := store.readGeneration(generation)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
