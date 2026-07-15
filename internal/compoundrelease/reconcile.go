package compoundrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

const (
	ReconcilePublished = "published"
	ReconcileInvoked   = "invoked"
	ReconcileAmbiguous = "ambiguous"
	ReconcileActivated = "activated"
	ReconcileConflict  = "conflict"
)

// ReconcileAdapter injects the existing CLI-owned root, receipt-path,
// receipt-read, and durable-send seams. compoundrelease validates their exact
// outputs; it deliberately owns no competing resolver or transport path.
type ReconcileAdapter interface {
	ResolveSessionRoot(Scope) (string, error)
	ExpectedReceiptPath(Scope, string) (string, error)
	ReadReceipt(string) ([]byte, error)
	ScanSessionMessages(string, func() time.Time) ([]state.Message, []state.Warning)
	InvokeReleaseChild(ReleaseChildInvocation) ReleaseChildInvokeOutcome
}

type ReleaseChildInvocation struct {
	GenerationID         string
	Role                 string
	Ordinal              int
	AttemptID            string
	Root                 string
	ReceiptPath          string
	Kind                 string
	Sender               string
	Recipient            string
	Thread               string
	Subject              string
	Body                 string
	AuthorizationRequest operatorauth.GateRequestContext
	ReleaseChild         operatorauth.ReleaseChildContext
}

type ReleaseChildInvokeOutcome struct {
	Err             error
	ProcessStarted  bool
	InvocationBegan bool
}

type ReconcileResult struct {
	Disposition string
	Role        string
	Snapshot    Snapshot
}

var (
	reconcileNow   = time.Now
	reconcileFault = func(string) error { return nil }
)

type reconcileReceiptObservation struct {
	Path    string
	Present bool
	Bound   boundReleaseReceiptV2
	Err     error
	ReadErr error
}

type reconcileChildAssessment struct {
	Kind    string
	Reason  string
	IDs     []string
	Receipt *operatorauth.ReleaseDeliveryReceiptTuple
}

const (
	assessmentStable    = "stable"
	assessmentAdopt     = "adopt"
	assessmentInvoke    = "invoke"
	assessmentAmbiguous = "ambiguous"
	assessmentConflict  = "conflict"
)

func (s *Store) Reconcile(expectedGenerationID string, adapter ReconcileAdapter) (ReconcileResult, error) {
	if adapter == nil {
		return ReconcileResult{}, fmt.Errorf("release reconcile adapter is required")
	}
	current, recordAheadActive, err := s.readReconcileCurrent(expectedGenerationID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if current.Pointer.GenerationID != expectedGenerationID {
		return ReconcileResult{}, fmt.Errorf("current release generation changed")
	}
	switch current.Pointer.State {
	case operatorauth.ReleaseStateActive:
		return ReconcileResult{Disposition: ReconcileActivated, Snapshot: current}, nil
	case operatorauth.ReleaseStateConflict:
		return ReconcileResult{Disposition: ReconcileConflict, Snapshot: current}, nil
	case operatorauth.ReleaseStatePublishing:
	default:
		return ReconcileResult{}, fmt.Errorf("current release is not publishing")
	}

	root, err := adapter.ResolveSessionRoot(s.scope)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("resolve fresh session root: %w", err)
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return ReconcileResult{}, fmt.Errorf("resolved session root is not canonical absolute")
	}

	// Both canonical receipts are read before the single complete mailbox scan.
	receipts := make([]reconcileReceiptObservation, len(current.Prepared.Children))
	for i, child := range current.Prepared.Children {
		path, pathErr := adapter.ExpectedReceiptPath(s.scope, child.Receipt.AttemptID)
		if pathErr != nil {
			return ReconcileResult{}, fmt.Errorf("resolve child %d canonical receipt path: %w", i, pathErr)
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ReconcileResult{}, fmt.Errorf("child %d receipt path is not canonical absolute", i)
		}
		receipts[i].Path = path
		raw, readErr := adapter.ReadReceipt(path)
		switch {
		case readErr == nil:
			receipts[i].Present = true
			receipts[i].Bound, receipts[i].Err = decodeBoundReleaseReceiptV2(raw, s.scope, child, path, root)
		case errors.Is(readErr, os.ErrNotExist):
		default:
			receipts[i].ReadErr = readErr
		}
		if err := reconcileFault("after_receipt_read:" + strconv.Itoa(i)); err != nil {
			return ReconcileResult{}, err
		}
	}
	for i, receipt := range receipts {
		if receipt.ReadErr != nil {
			return ReconcileResult{Disposition: ReconcileAmbiguous, Role: current.Prepared.Children[i].Role, Snapshot: current}, fmt.Errorf("read child %d delivery receipt: %w", i, receipt.ReadErr)
		}
	}

	messages, warnings := adapter.ScanSessionMessages(root, reconcileNow)
	if len(warnings) != 0 {
		return ReconcileResult{Disposition: ReconcileAmbiguous, Snapshot: current}, fmt.Errorf("release mailbox scan produced %d warning(s)", len(warnings))
	}
	if err := reconcileFault("after_complete_scan"); err != nil {
		return ReconcileResult{}, err
	}
	groups := groupReleaseMessages(messages)
	record, err := s.readGeneration(current.Pointer.Generation)
	if err != nil {
		return ReconcileResult{}, err
	}
	assessments := make([]reconcileChildAssessment, len(current.Prepared.Children))
	for i, child := range current.Prepared.Children {
		assessments[i] = assessReleaseChild(record.Children[i], child, groups, receipts[i])
	}
	for i, assessment := range assessments {
		if assessment.Kind == assessmentConflict {
			if recordAheadActive != nil {
				return ReconcileResult{Disposition: ReconcileAmbiguous, Role: current.Prepared.Children[i].Role, Snapshot: current}, fmt.Errorf("active record-ahead evidence conflicts: %s", assessment.Reason)
			}
			return s.terminalReconcileConflict(current, i, assessment.Reason, assessment.IDs)
		}
	}
	if recordAheadActive != nil {
		for _, assessment := range assessments {
			if assessment.Kind != assessmentStable {
				return ReconcileResult{Disposition: ReconcileAmbiguous, Snapshot: current}, fmt.Errorf("active record-ahead evidence is not fully stable")
			}
		}
		repaired, repairErr := s.activate(*recordAheadActive, reconcileFault)
		if repairErr != nil {
			return ReconcileResult{}, repairErr
		}
		return ReconcileResult{Disposition: ReconcileActivated, Snapshot: repaired}, nil
	}
	for i, assessment := range assessments {
		if assessment.Kind == assessmentAmbiguous {
			return ReconcileResult{Disposition: ReconcileAmbiguous, Role: current.Prepared.Children[i].Role, Snapshot: current}, fmt.Errorf("release evidence remains delivery-uncertain")
		}
	}

	adoptedRole := ""
	for i, assessment := range assessments {
		if assessment.Kind != assessmentAdopt {
			continue
		}
		if err := s.AdoptChildPublication(expectedGenerationID, i, *assessment.Receipt); err != nil {
			return ReconcileResult{}, err
		}
		adoptedRole = current.Prepared.Children[i].Role
		if err := reconcileFault("after_child_adoption:" + strconv.Itoa(i)); err != nil {
			return ReconcileResult{}, err
		}
	}

	updated, err := s.ReadCurrent()
	if err != nil {
		return ReconcileResult{}, err
	}
	updatedRecord, err := s.readGeneration(updated.Pointer.Generation)
	if err != nil {
		return ReconcileResult{}, err
	}
	if allReleaseChildrenPublished(updatedRecord) {
		return s.activateReconciled(updated, updatedRecord)
	}
	for i, assessment := range assessments {
		if assessment.Kind != assessmentInvoke {
			continue
		}
		if i == 1 && updatedRecord.Children[0].State != childPublicationPublished {
			continue
		}
		return s.invokeReconciledChild(updated, current.Prepared.Children[i], root, receipts[i].Path, adapter)
	}
	if adoptedRole != "" {
		return ReconcileResult{Disposition: ReconcilePublished, Role: adoptedRole, Snapshot: updated}, nil
	}
	return ReconcileResult{Disposition: ReconcileAmbiguous, Snapshot: updated}, fmt.Errorf("release reconcile made no safe progress")
}

func (s *Store) readReconcileCurrent(expectedGenerationID string) (Snapshot, *operatorauth.ActiveReleaseManifest, error) {
	current, err := s.ReadCurrent()
	if err == nil {
		return current, nil, nil
	}
	pointer, pointerErr := s.readPointer()
	if pointerErr != nil || pointer.GenerationID != expectedGenerationID || pointer.State != operatorauth.ReleaseStatePublishing {
		return Snapshot{}, nil, err
	}
	prepared, preparedErr := s.readPrepared(pointer.Generation)
	if preparedErr != nil {
		return Snapshot{}, nil, err
	}
	record, recordErr := s.readGeneration(pointer.Generation)
	if recordErr != nil || record.State != operatorauth.ReleaseStateActive {
		return Snapshot{}, nil, err
	}
	active, activeErr := s.readActive(pointer.Generation, prepared)
	if activeErr != nil {
		return Snapshot{}, nil, err
	}
	ahead := pointer
	ahead.State = operatorauth.ReleaseStateActive
	ahead.ActiveManifestID = record.ActiveManifestID
	ahead.ActiveSHA256 = record.ActiveSHA256
	if validationErr := s.validateLifecycleSnapshot(ahead, record, prepared, &active); validationErr != nil {
		return Snapshot{}, nil, fmt.Errorf("active record-ahead recovery validation: %w", validationErr)
	}
	return Snapshot{Pointer: pointer, Prepared: prepared}, &active, nil
}

func assessReleaseChild(record childPublicationRecord, child operatorauth.ReleaseChildPlan, groups []releaseMessageGroup, receipt reconcileReceiptObservation) reconcileChildAssessment {
	exact, near, uncertain := classifyReleaseMessageGroups(groups, child)
	if len(near) != 0 {
		return reconcileChildAssessment{Kind: assessmentConflict, Reason: "message evidence targets release child but is not exact", IDs: near}
	}
	if receipt.Err != nil {
		return reconcileChildAssessment{Kind: assessmentConflict, Reason: "canonical delivery receipt is malformed or mismatched", IDs: releaseMessageIDs(exact)}
	}
	if len(exact) > 1 {
		return reconcileChildAssessment{Kind: assessmentConflict, Reason: "multiple distinct exact release child messages", IDs: releaseMessageIDs(exact)}
	}
	var tuple *operatorauth.ReleaseDeliveryReceiptTuple
	if receipt.Present {
		tuple = receipt.Bound.Tuple
	}
	if len(exact) == 1 && tuple != nil && tuple.MessageID != exact[0].Message.ID {
		return reconcileChildAssessment{Kind: assessmentConflict, Reason: "delivery receipt id diverges from sole exact message", IDs: []string{exact[0].Message.ID, tuple.MessageID}}
	}

	switch record.State {
	case childPublicationPublished, childPublicationAdopted:
		if len(exact) == 0 {
			return reconcileChildAssessment{Kind: assessmentAmbiguous}
		}
		if !receipt.Present {
			return reconcileChildAssessment{Kind: assessmentAmbiguous}
		}
		if tuple == nil {
			return reconcileChildAssessment{Kind: assessmentConflict, Reason: "published child receipt is missing or no longer invoked", IDs: releaseMessageIDs(exact)}
		}
		if record.Receipt == nil || record.QuestionMessageID != exact[0].Message.ID || record.ReceiptSHA256 == "" || tuple.AdoptedGeneration < record.Receipt.AdoptedGeneration || !deliveryReceiptStableEqual(*record.Receipt, *tuple) {
			return reconcileChildAssessment{Kind: assessmentConflict, Reason: "published child evidence disappeared or diverged", IDs: releaseMessageIDs(exact)}
		}
		digest, err := operatorauth.ReleaseDeliveryReceiptSHA256(*record.Receipt)
		if err != nil || digest != record.ReceiptSHA256 {
			return reconcileChildAssessment{Kind: assessmentConflict, Reason: "published child receipt digest diverged", IDs: releaseMessageIDs(exact)}
		}
		return reconcileChildAssessment{Kind: assessmentStable, Receipt: tuple}
	case childPublicationSending:
		if len(exact) == 1 && tuple != nil {
			return reconcileChildAssessment{Kind: assessmentAdopt, Receipt: tuple}
		}
		return reconcileChildAssessment{Kind: assessmentAmbiguous}
	case childPublicationPlanned:
		if len(exact) != 0 {
			return reconcileChildAssessment{Kind: assessmentConflict, Reason: "exact message exists before durable child claim", IDs: releaseMessageIDs(exact)}
		}
		if receipt.Present {
			return reconcileChildAssessment{Kind: assessmentConflict, Reason: "canonical delivery receipt exists before durable child claim"}
		}
		if uncertain {
			return reconcileChildAssessment{Kind: assessmentAmbiguous}
		}
		return reconcileChildAssessment{Kind: assessmentInvoke}
	default:
		return reconcileChildAssessment{Kind: assessmentConflict, Reason: "unsupported publishing child state"}
	}
}

func deliveryReceiptStableEqual(a, b operatorauth.ReleaseDeliveryReceiptTuple) bool {
	a.AdoptedGeneration = b.AdoptedGeneration
	return deliveryReceiptTupleEqual(a, b)
}

func (s *Store) invokeReconciledChild(current Snapshot, child operatorauth.ReleaseChildPlan, root, receiptPath string, adapter ReconcileAdapter) (ReconcileResult, error) {
	claim, err := s.ClaimChildSend(current.Pointer.GenerationID, child.Ordinal)
	if err != nil {
		return ReconcileResult{}, err
	}
	if err := reconcileFault("pre_invoke:" + strconv.Itoa(child.Ordinal)); err != nil {
		return ReconcileResult{}, err
	}
	outcome := adapter.InvokeReleaseChild(ReleaseChildInvocation{
		GenerationID: current.Pointer.GenerationID, Role: child.Role, Ordinal: child.Ordinal,
		AttemptID: child.Receipt.AttemptID, Root: root, ReceiptPath: receiptPath,
		Kind: child.Receipt.Kind, Sender: child.Receipt.Sender, Recipient: child.Receipt.Recipient,
		Thread: child.Thread, Subject: child.Subject, Body: child.Body,
		AuthorizationRequest: child.AuthorizationRequest, ReleaseChild: child.ReleaseChild,
	})
	if err := reconcileFault("after_send_return:" + strconv.Itoa(child.Ordinal)); err != nil {
		return ReconcileResult{}, err
	}
	if outcome.Err != nil && !outcome.ProcessStarted && !outcome.InvocationBegan {
		rollbackErr := s.rollbackChildSend(claim, noInvocationEvidence{claimToken: claim.Token})
		if rollbackErr != nil {
			return ReconcileResult{}, fmt.Errorf("transport failed before invocation (%v), rollback failed: %w", outcome.Err, rollbackErr)
		}
		updated, readErr := s.ReadCurrent()
		if readErr != nil {
			return ReconcileResult{}, readErr
		}
		return ReconcileResult{Disposition: ReconcileAmbiguous, Role: child.Role, Snapshot: updated}, fmt.Errorf("release child transport failed before invocation: %w", outcome.Err)
	}
	if outcome.Err == nil && !outcome.InvocationBegan {
		return ReconcileResult{Disposition: ReconcileAmbiguous, Role: child.Role, Snapshot: current}, fmt.Errorf("release transport returned success without invocation-boundary evidence")
	}
	updated, readErr := s.ReadCurrent()
	if readErr != nil {
		return ReconcileResult{}, readErr
	}
	if outcome.Err != nil {
		return ReconcileResult{Disposition: ReconcileAmbiguous, Role: child.Role, Snapshot: updated}, fmt.Errorf("release child invocation is delivery-uncertain: %w", outcome.Err)
	}
	return ReconcileResult{Disposition: ReconcileInvoked, Role: child.Role, Snapshot: updated}, nil
}

func (s *Store) activateReconciled(current Snapshot, record generationRecord) (ReconcileResult, error) {
	receipts := make(map[string]operatorauth.ReleaseDeliveryReceiptTuple, len(record.Children))
	for _, child := range record.Children {
		if child.State != childPublicationPublished || child.Receipt == nil {
			return ReconcileResult{Disposition: ReconcileAmbiguous, Snapshot: current}, fmt.Errorf("release children are not both durably published")
		}
		receipts[child.Role] = cloneDeliveryReceipt(*child.Receipt)
	}
	active, err := operatorauth.NewActiveRelease(current.Prepared, receipts)
	if err != nil {
		return ReconcileResult{}, err
	}
	activated, err := s.activate(active, reconcileFault)
	if err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{Disposition: ReconcileActivated, Snapshot: activated}, nil
}

func (s *Store) terminalReconcileConflict(current Snapshot, ordinal int, reason string, ids []string) (ReconcileResult, error) {
	if err := s.TerminalizeChildConflict(current.Pointer.GenerationID, ordinal, reason, ids); err != nil {
		return ReconcileResult{}, err
	}
	updated, err := s.ReadCurrent()
	if err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{Disposition: ReconcileConflict, Role: current.Prepared.Children[ordinal].Role, Snapshot: updated}, nil
}

func allReleaseChildrenPublished(record generationRecord) bool {
	return len(record.Children) == 2 && record.Children[0].State == childPublicationPublished && record.Children[1].State == childPublicationPublished
}

type releaseMessageGroup struct {
	Message state.Message
	Copies  []state.Message
	Owners  []string
	Equal   bool
}

func groupReleaseMessages(messages []state.Message) []releaseMessageGroup {
	byID := make(map[string][]state.Message)
	for _, message := range messages {
		if message.ID != "" {
			byID[message.ID] = append(byID[message.ID], message)
		}
	}
	groups := make([]releaseMessageGroup, 0, len(byID))
	for _, copies := range byID {
		sort.Slice(copies, func(i, j int) bool { return copies[i].Path < copies[j].Path })
		group := releaseMessageGroup{Message: copies[0], Copies: copies, Equal: true}
		for _, copy := range copies {
			if !slices.Contains(group.Owners, copy.Owner) {
				group.Owners = append(group.Owners, copy.Owner)
			}
			if !releasePhysicalMessageEqual(copies[0], copy) {
				group.Equal = false
			}
		}
		sort.Strings(group.Owners)
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Message.ID < groups[j].Message.ID })
	return groups
}

func releasePhysicalMessageEqual(a, b state.Message) bool {
	if a.ID != b.ID || a.From != b.From || !slices.Equal(a.To, b.To) || a.RawThread != b.RawThread || a.Thread != b.Thread || a.RawSubject != b.RawSubject || a.RawCreated != b.RawCreated || a.Priority != b.Priority || a.Kind != b.Kind || a.ReplyTo != b.ReplyTo || !slices.Equal(a.Labels, b.Labels) || a.Orchestrator != b.Orchestrator || a.FromProject != b.FromProject || a.ReplyProject != b.ReplyProject || a.OrchestratorEvent != b.OrchestratorEvent || a.ExternalTaskID != b.ExternalTaskID || a.RawBody != b.RawBody || a.AuthorityRaw != b.AuthorityRaw || a.SchemaOK != b.SchemaOK {
		return false
	}
	aContext, aErr := json.Marshal(a.Context)
	bContext, bErr := json.Marshal(b.Context)
	return aErr == nil && bErr == nil && slices.Equal(aContext, bContext)
}

func classifyReleaseMessageGroups(groups []releaseMessageGroup, child operatorauth.ReleaseChildPlan) (exact []releaseMessageGroup, near []string, uncertain bool) {
	for _, group := range groups {
		relevant := false
		for _, copy := range group.Copies {
			if looseReleaseMessageTargets(copy, child) {
				relevant = true
				break
			}
		}
		if !relevant {
			continue
		}
		if !slices.Contains(group.Owners, child.Receipt.Recipient) {
			uncertain = true
			continue
		}
		if group.Equal && exactReleaseMessage(group, child) {
			exact = append(exact, group)
		} else {
			near = append(near, group.Message.ID)
		}
	}
	sort.Strings(near)
	return exact, slices.Compact(near), uncertain
}

func looseReleaseMessageTargets(message state.Message, child operatorauth.ReleaseChildPlan) bool {
	if message.RawThread == child.Thread || message.Thread == child.Thread || message.RawSubject == child.Subject {
		return true
	}
	raw, ok := message.Context["release_child"]
	if !ok {
		return false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	var marker struct {
		ReleaseID          string `json:"release_id"`
		GenerationID       string `json:"generation_id"`
		Generation         uint64 `json:"generation"`
		PreparedManifestID string `json:"prepared_manifest_id"`
		Role               string `json:"role"`
		Ordinal            int    `json:"ordinal"`
		Thread             string `json:"thread"`
		AttemptID          string `json:"attempt_id"`
	}
	if json.Unmarshal(b, &marker) != nil {
		return false
	}
	want := child.ReleaseChild
	role := marker.Role == want.Role && marker.Ordinal == want.Ordinal
	return marker.AttemptID == want.AttemptID || marker.Thread == want.Thread || role && (marker.GenerationID == want.GenerationID || marker.PreparedManifestID == want.PreparedManifestID || marker.ReleaseID == want.ReleaseID && marker.Generation == want.Generation)
}

func exactReleaseMessage(group releaseMessageGroup, child operatorauth.ReleaseChildPlan) bool {
	m := group.Message
	created, err := time.Parse(time.RFC3339Nano, m.RawCreated)
	if err != nil || created.Format(time.RFC3339Nano) != m.RawCreated {
		return false
	}
	if !m.AuthorityRaw || !m.SchemaOK || m.Priority != state.PriorityNormal || m.Kind != state.KindQuestion || m.From != child.Receipt.Sender || !slices.Equal(m.To, []string{child.Receipt.Recipient}) || !slices.Contains(group.Owners, child.Receipt.Recipient) || m.RawThread != child.Thread || m.Thread != child.Thread || m.RawSubject != child.Subject || m.RawBody != child.Body || m.ReplyTo != "" || len(m.Labels) != 0 || m.Orchestrator != "" || m.FromProject != "" || m.ReplyProject != "" || m.OrchestratorEvent != "" || m.ExternalTaskID != "" {
		return false
	}
	for _, owner := range group.Owners {
		if owner != child.Receipt.Sender && owner != child.Receipt.Recipient {
			return false
		}
	}
	if len(m.Context) != 2 {
		return false
	}
	rawRequest, requestOK := m.Context["authorization_request"]
	rawChild, childOK := m.Context["release_child"]
	if !requestOK || !childOK {
		return false
	}
	request, err := operatorauth.DecodeGateRequest(rawRequest)
	if err != nil || request != child.AuthorizationRequest {
		return false
	}
	marker, err := operatorauth.DecodeReleaseChild(rawChild)
	return err == nil && marker == child.ReleaseChild
}

func releaseMessageIDs(groups []releaseMessageGroup) []string {
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.Message.ID)
	}
	sort.Strings(ids)
	return slices.Compact(ids)
}
