package compoundrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/state"
)

const (
	ReconcilePublished = "published"
	ReconcileAmbiguous = "ambiguous"
	ReconcileActivated = "activated"
	ReconcileConflict  = "conflict"
)

// ReconcileAdapter is the publication seam of the 3.0 release gate flow
// (brief 3-2): a child's question is the typed gate the Go core writes (3-1's
// gate store: the record first, then the question to the owner's thread), not
// a mailbox message. ChildGate raises that gate, or finds it when a crash
// came between the raise and the adoption, and returns its id; the id is
// derived from the child's attempt identity, so a retry returns the same gate
// and the store never holds two questions for one child. A gate that exists
// under the id but binds another action or target is ErrChildConflict.
type ReconcileAdapter interface {
	ChildGate(child operatorauth.ReleaseChildPlan) (string, error)
}

// ErrChildConflict marks a child whose gate id is taken by another binding.
var ErrChildConflict = errors.New("release child gate conflicts with its plan")

type ReconcileResult struct {
	Disposition string
	Role        string
	Snapshot    Snapshot
}

var reconcileFault = func(string) error { return nil }

// Reconcile drives a release generation to active: planned enters publishing,
// each child in order is claimed, its gate raised (or found) and adopted, and
// the generation activates once both are published. It is the recovery path
// as well: a child left sending by a crash adopts the gate its id names.
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
	case operatorauth.ReleaseStatePlanned:
		if current, err = s.StartPublishing(expectedGenerationID); err != nil {
			return ReconcileResult{}, err
		}
	case operatorauth.ReleaseStatePublishing:
	default:
		return ReconcileResult{}, fmt.Errorf("current release is %s", current.Pointer.State)
	}
	if recordAheadActive != nil {
		repaired, err := s.activate(*recordAheadActive, reconcileFault)
		if err != nil {
			return ReconcileResult{}, err
		}
		return ReconcileResult{Disposition: ReconcileActivated, Snapshot: repaired}, nil
	}
	for i, child := range current.Prepared.Children {
		record, err := s.readGeneration(current.Pointer.Generation)
		if err != nil {
			return ReconcileResult{}, err
		}
		switch record.Children[i].State {
		case childPublicationPublished:
			continue
		case childPublicationPlanned:
			if _, err := s.ClaimChildSend(expectedGenerationID, i); err != nil {
				return ReconcileResult{}, err
			}
		case childPublicationSending:
		default:
			return ReconcileResult{Disposition: ReconcileConflict, Role: child.Role, Snapshot: current}, fmt.Errorf("release child %s is %s", child.Role, record.Children[i].State)
		}
		if err := reconcileFault("after_claim:" + strconv.Itoa(i)); err != nil {
			return ReconcileResult{}, err
		}
		id, err := adapter.ChildGate(child)
		if errors.Is(err, ErrChildConflict) {
			return s.terminalReconcileConflict(current, i, err.Error(), []string{id})
		}
		if err != nil {
			// the child stays sending: the next reconcile asks for the same gate id
			return ReconcileResult{Disposition: ReconcileAmbiguous, Role: child.Role, Snapshot: current}, fmt.Errorf("release child %s gate: %w", child.Role, err)
		}
		if err := reconcileFault("after_gate:" + strconv.Itoa(i)); err != nil {
			return ReconcileResult{}, err
		}
		if err := s.AdoptChildPublication(expectedGenerationID, i, id); err != nil {
			return ReconcileResult{}, err
		}
	}
	updated, err := s.ReadCurrent()
	if err != nil {
		return ReconcileResult{}, err
	}
	record, err := s.readGeneration(updated.Pointer.Generation)
	if err != nil {
		return ReconcileResult{}, err
	}
	return s.activateReconciled(updated, record)
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

func (s *Store) activateReconciled(current Snapshot, record generationRecord) (ReconcileResult, error) {
	gateIDs := make(map[string]string, len(record.Children))
	for _, child := range record.Children {
		if child.State != childPublicationPublished || child.QuestionMessageID == "" {
			return ReconcileResult{Disposition: ReconcileAmbiguous, Snapshot: current}, fmt.Errorf("release children are not both durably published")
		}
		gateIDs[child.Role] = child.QuestionMessageID
	}
	active, err := operatorauth.NewActiveRelease(current.Prepared, gateIDs)
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

// The mailbox readers below serve the v2 operator path that still reads
// compound releases from mail: the eligibility projection (eligibility.go) and
// the invocation guard (guard.go). A 3.0 release adopts gate ids, not mail
// (Reconcile above), so they find no counterpart for it; their rewrite belongs
// with the operator session (4-3).

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

func classifyReleaseMessageGroups(groups []releaseMessageGroup, child operatorauth.ReleaseChildPlan, spec operatorauth.ReleaseSpec) (exact []releaseMessageGroup, near []string, uncertain bool) {
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
		if !slices.Contains(group.Owners, spec.OperatorHandle) {
			uncertain = true
			continue
		}
		if group.Equal && exactReleaseMessage(group, child, spec) {
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

func exactReleaseMessage(group releaseMessageGroup, child operatorauth.ReleaseChildPlan, spec operatorauth.ReleaseSpec) bool {
	m := group.Message
	created, err := time.Parse(time.RFC3339Nano, m.RawCreated)
	if err != nil || created.Format(time.RFC3339Nano) != m.RawCreated {
		return false
	}
	if !m.AuthorityRaw || !m.SchemaOK || m.Priority != state.PriorityNormal || m.Kind != state.KindQuestion || m.From != spec.RequesterHandle || !slices.Equal(m.To, []string{spec.OperatorHandle}) || !slices.Contains(group.Owners, spec.OperatorHandle) || m.RawThread != child.Thread || m.Thread != child.Thread || m.RawSubject != child.Subject || m.RawBody != child.Body || m.ReplyTo != "" || len(m.Labels) != 0 || m.Orchestrator != "" || m.FromProject != "" || m.ReplyProject != "" || m.OrchestratorEvent != "" || m.ExternalTaskID != "" {
		return false
	}
	for _, owner := range group.Owners {
		if owner != spec.RequesterHandle && owner != spec.OperatorHandle {
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
