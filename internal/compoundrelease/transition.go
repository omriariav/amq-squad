package compoundrelease

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

// ChildSendClaim is the in-process capability returned by ClaimChildSend. Its
// nonce is deliberately not persisted or exported: a restarted reconciler can
// observe the durable token but cannot manufacture the live capability needed
// to roll a sending child back to planned.
type ChildSendClaim struct {
	GenerationID string
	Role         string
	Ordinal      int
	AttemptID    string
	Revision     uint64
	Token        string
	nonce        [32]byte
}

// noInvocationEvidence is constructed only by the synchronous transport path
// before process start. A persisted AMQInvoked=false receipt is not this proof.
type noInvocationEvidence struct {
	claimToken      string
	processStarted  bool
	invocationBegan bool
}

func definitelyUninvokedEvidence(claim ChildSendClaim) noInvocationEvidence {
	return noInvocationEvidence{claimToken: claim.Token}
}

func (s *Store) ClaimChildSend(expectedGenerationID string, ordinal int) (ChildSendClaim, error) {
	var claim ChildSendClaim
	if _, err := rand.Read(claim.nonce[:]); err != nil {
		return ChildSendClaim{}, fmt.Errorf("generate child send claim: %w", err)
	}
	err := s.withLock(func() error {
		pointer, prepared, record, err := s.readPublishingLifecycleForTransition(expectedGenerationID)
		if err != nil {
			return err
		}
		if ordinal < 0 || ordinal >= len(record.Children) {
			return fmt.Errorf("publication child ordinal is invalid")
		}
		if ordinal == 1 && record.Children[0].State != childPublicationPublished {
			return fmt.Errorf("github release child cannot start before tag evidence is stable")
		}
		child := record.Children[ordinal]
		if child.State != childPublicationPlanned || child.ClaimToken != "" {
			return fmt.Errorf("publication child is %s, not definitely uninvoked", child.State)
		}
		claim.GenerationID = pointer.GenerationID
		claim.Role = child.Role
		claim.Ordinal = child.Ordinal
		claim.AttemptID = child.AttemptID
		claim.Revision = child.ClaimRevision + 1
		claim.Token = childClaimToken(claim, claim.nonce)
		record.Revision++
		record.Children[ordinal].State = childPublicationSending
		record.Children[ordinal].ClaimRevision = claim.Revision
		record.Children[ordinal].ClaimToken = claim.Token
		if err := s.validateLifecycleSnapshot(pointer, record, prepared, nil); err != nil {
			return err
		}
		return s.writeGeneration(record)
	})
	if err != nil {
		return ChildSendClaim{}, err
	}
	return claim, nil
}

func (s *Store) rollbackChildSend(claim ChildSendClaim, evidence noInvocationEvidence) error {
	if evidence.processStarted || evidence.invocationBegan || evidence.claimToken == "" || evidence.claimToken != claim.Token || childClaimToken(claim, claim.nonce) != claim.Token {
		return fmt.Errorf("child send rollback lacks matching live definitely-uninvoked evidence")
	}
	return s.withLock(func() error {
		pointer, prepared, record, err := s.readPublishingLifecycleForTransition(claim.GenerationID)
		if err != nil {
			return err
		}
		if claim.Ordinal < 0 || claim.Ordinal >= len(record.Children) {
			return fmt.Errorf("publication child ordinal is invalid")
		}
		child := record.Children[claim.Ordinal]
		if child.Role != claim.Role || child.Ordinal != claim.Ordinal || child.AttemptID != claim.AttemptID || child.State != childPublicationSending || child.ClaimRevision != claim.Revision || child.ClaimToken != claim.Token {
			return fmt.Errorf("child send claim no longer matches durable state")
		}
		record.Revision++
		record.Children[claim.Ordinal].State = childPublicationPlanned
		record.Children[claim.Ordinal].ClaimToken = ""
		if err := s.validateLifecycleSnapshot(pointer, record, prepared, nil); err != nil {
			return err
		}
		return s.writeGeneration(record)
	})
}

func (s *Store) AdoptChildPublication(expectedGenerationID string, ordinal int, receipt operatorauth.ReleaseDeliveryReceiptTuple) error {
	return s.withLock(func() error {
		pointer, prepared, record, err := s.readPublishingLifecycleForTransition(expectedGenerationID)
		if err != nil {
			return err
		}
		if ordinal < 0 || ordinal >= len(record.Children) {
			return fmt.Errorf("publication child ordinal is invalid")
		}
		if err := validateChildReceipt(prepared.Children[ordinal], receipt); err != nil {
			return err
		}
		if ordinal == 1 && record.Children[0].State != childPublicationPublished {
			return fmt.Errorf("github release child cannot be adopted before tag evidence is stable")
		}
		current := record.Children[ordinal]
		receiptSHA, err := operatorauth.ReleaseDeliveryReceiptSHA256(receipt)
		if err != nil {
			return err
		}
		if current.State == childPublicationPublished {
			if current.QuestionMessageID != receipt.MessageID || current.ReceiptPath != receipt.Path || current.ReceiptSHA256 != receiptSHA || current.Receipt == nil || !deliveryReceiptTupleEqual(*current.Receipt, receipt) {
				return fmt.Errorf("published child evidence changed")
			}
			return nil
		}
		if current.State != childPublicationSending || current.ClaimRevision == 0 || !validClaimToken(current.ClaimToken) {
			return fmt.Errorf("publication child is not an exact claimed send")
		}
		for i, other := range record.Children {
			if i != ordinal && (other.QuestionMessageID == receipt.MessageID || other.ReceiptPath == receipt.Path || other.AttemptID == receipt.AttemptID) {
				return fmt.Errorf("publication child provenance is not distinct")
			}
		}
		record.Revision++
		record.Children[ordinal].State = childPublicationPublished
		record.Children[ordinal].QuestionMessageID = receipt.MessageID
		record.Children[ordinal].ReceiptPath = receipt.Path
		record.Children[ordinal].ReceiptSHA256 = receiptSHA
		storedReceipt := cloneDeliveryReceipt(receipt)
		record.Children[ordinal].Receipt = &storedReceipt
		if err := s.validateLifecycleSnapshot(pointer, record, prepared, nil); err != nil {
			return err
		}
		return s.writeGeneration(record)
	})
}

func (s *Store) TerminalizeChildConflict(expectedGenerationID string, ordinal int, reason string, observedMessageIDs []string) error {
	reason = strings.TrimSpace(reason)
	ids := append([]string(nil), observedMessageIDs...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	probe := childPublicationRecord{State: childPublicationConflict, ConflictReason: reason, ObservedMessageIDs: ids}
	if err := validateChildConflict(probe); err != nil {
		return err
	}
	return s.withLock(func() error {
		pointer, err := s.readPointer()
		if err != nil {
			return err
		}
		if pointer.GenerationID != expectedGenerationID {
			return fmt.Errorf("current release generation changed")
		}
		prepared, err := s.readPrepared(pointer.Generation)
		if err != nil {
			return err
		}
		record, err := s.readGeneration(pointer.Generation)
		if err != nil {
			return err
		}
		if pointer.State == operatorauth.ReleaseStateConflict && record.State == operatorauth.ReleaseStateConflict {
			if !persistedConflictEquals(record, ordinal, reason, ids) {
				return fmt.Errorf("requested conflict differs from durable terminal conflict")
			}
			return s.validateLifecycleSnapshot(pointer, record, prepared, nil)
		}
		if record.State == operatorauth.ReleaseStateConflict && pointer.State == operatorauth.ReleaseStatePublishing {
			if !persistedConflictEquals(record, ordinal, reason, ids) {
				return fmt.Errorf("requested conflict differs from record-ahead terminal conflict")
			}
			ahead := pointer
			ahead.State = operatorauth.ReleaseStateConflict
			if err := s.validateLifecycleSnapshot(ahead, record, prepared, nil); err != nil {
				return fmt.Errorf("conflict record-ahead recovery: %w", err)
			}
		} else {
			if err := s.validateLifecycleSnapshot(pointer, record, prepared, nil); err != nil {
				return err
			}
			if pointer.State != operatorauth.ReleaseStatePublishing || ordinal < 0 || ordinal >= len(record.Children) {
				return fmt.Errorf("release cannot enter publication conflict")
			}
			record.Revision++
			record.State = operatorauth.ReleaseStateConflict
			record.Children[ordinal].State = childPublicationConflict
			record.Children[ordinal].ConflictReason = reason
			record.Children[ordinal].ObservedMessageIDs = ids
			if err := s.validateLifecycleSnapshot(pointerFromRecord(record, pointer.Revision), record, prepared, nil); err != nil {
				return err
			}
			if err := s.writeGeneration(record); err != nil {
				return err
			}
			if err := storeFault("after_conflict_record_write"); err != nil {
				return err
			}
		}
		pointer.Revision++
		pointer.State = operatorauth.ReleaseStateConflict
		if err := s.writePointer(pointer); err != nil {
			return err
		}
		return s.validateLifecycleSnapshot(pointer, record, prepared, nil)
	})
}

func validateChildReceipt(child operatorauth.ReleaseChildPlan, receipt operatorauth.ReleaseDeliveryReceiptTuple) error {
	if receipt.AttemptID != child.Receipt.AttemptID || receipt.AttemptID != child.ReleaseChild.AttemptID || receipt.Kind != child.Receipt.Kind || receipt.Sender != child.Receipt.Sender || !slices.Equal(receipt.Recipients, []string{child.Receipt.Recipient}) || receipt.Thread != child.Receipt.Thread || receipt.MessageID == "" || receipt.NamespaceID != child.Receipt.NamespaceID || receipt.TargetIdentity != child.Receipt.TargetIdentity || receipt.AdoptedGeneration < child.Receipt.MinimumGeneration {
		return fmt.Errorf("observed receipt does not exactly bind publication child")
	}
	for name, value := range map[string]string{"message id": receipt.MessageID, "receipt path": receipt.Path, "receipt root": receipt.Root} {
		if value == "" {
			return fmt.Errorf("%s is required", name)
		}
		if name == "message id" {
			if err := operatorauth.ValidateCanonicalSingleLineField(name, value, true); err != nil {
				return err
			}
		} else if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("%s must be canonical absolute", name)
		}
	}
	_, err := operatorauth.ReleaseDeliveryReceiptSHA256(receipt)
	return err
}

func persistedConflictEquals(record generationRecord, ordinal int, reason string, ids []string) bool {
	if ordinal < 0 || ordinal >= len(record.Children) {
		return false
	}
	child := record.Children[ordinal]
	return child.State == childPublicationConflict && child.ConflictReason == reason && slices.Equal(child.ObservedMessageIDs, ids)
}

func (s *Store) readPublishingLifecycleForTransition(expectedGenerationID string) (Pointer, operatorauth.PreparedReleaseManifest, generationRecord, error) {
	pointer, err := s.readPointer()
	if err != nil {
		return Pointer{}, operatorauth.PreparedReleaseManifest{}, generationRecord{}, err
	}
	if pointer.GenerationID != expectedGenerationID || pointer.State != operatorauth.ReleaseStatePublishing {
		return Pointer{}, operatorauth.PreparedReleaseManifest{}, generationRecord{}, fmt.Errorf("current release is not the expected publishing generation")
	}
	prepared, err := s.readPrepared(pointer.Generation)
	if err != nil {
		return Pointer{}, operatorauth.PreparedReleaseManifest{}, generationRecord{}, err
	}
	record, err := s.readGeneration(pointer.Generation)
	if err != nil {
		return Pointer{}, operatorauth.PreparedReleaseManifest{}, generationRecord{}, err
	}
	if err := s.validateLifecycleSnapshot(pointer, record, prepared, nil); err != nil {
		return Pointer{}, operatorauth.PreparedReleaseManifest{}, generationRecord{}, err
	}
	return pointer, prepared, record, nil
}

func childClaimToken(claim ChildSendClaim, nonce [32]byte) string {
	h := sha256.New()
	for _, part := range []string{claim.GenerationID, claim.Role, strconv.Itoa(claim.Ordinal), claim.AttemptID, strconv.FormatUint(claim.Revision, 10)} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write(nonce[:])
	return "release-claim-v1-" + hex.EncodeToString(h.Sum(nil))
}
