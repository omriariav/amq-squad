package compoundrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

const eligibilityTokenPrefix = "release-eligibility-v1-"

// InspectionAdapter is intentionally read-only. ReconcileAdapter satisfies it,
// but resolving eligibility cannot reach its invocation method.
//
// Lock order is deterministic: store -> receipt/mailbox reads -> invocation
// callback. Neither callers nor adapters may acquire a store lock while
// holding a receipt or mailbox lock.
type InspectionAdapter interface {
	ResolveSessionRoot(Scope) (string, error)
	ExpectedReceiptPath(Scope, string) (string, error)
	ReadReceipt(string) ([]byte, error)
	ScanSessionMessages(string, func() time.Time) ([]state.Message, []state.Warning)
}

type ResolveQuery struct {
	MessageID string
	Gate      string
	Action    string
	Target    string
}

type DegradationCode string

const (
	DegradationRootUnavailable     DegradationCode = "root_unavailable"
	DegradationEnumerationFailed   DegradationCode = "series_enumeration_failed"
	DegradationLockUnavailable     DegradationCode = "store_lock_unavailable"
	DegradationStoreBusy           DegradationCode = "store_busy"
	DegradationIdentityUnavailable DegradationCode = "series_identity_unavailable"
	DegradationScanWarning         DegradationCode = "mailbox_scan_warning"
	DegradationLockChanged         DegradationCode = "store_lock_changed"
)

type Degradation struct {
	Code   DegradationCode
	Reason string
}

type ProjectionState string

const (
	ProjectionStateUnknown    ProjectionState = "unknown"
	ProjectionStatePlanned    ProjectionState = ProjectionState(operatorauth.ReleaseStatePlanned)
	ProjectionStatePublishing ProjectionState = ProjectionState(operatorauth.ReleaseStatePublishing)
	ProjectionStateActive     ProjectionState = ProjectionState(operatorauth.ReleaseStateActive)
	ProjectionStateConflict   ProjectionState = ProjectionState(operatorauth.ReleaseStateConflict)
	ProjectionStateSuperseded ProjectionState = ProjectionState(operatorauth.ReleaseStateSuperseded)
	ProjectionStateAborted    ProjectionState = ProjectionState(operatorauth.ReleaseStateAborted)
)

type ProjectionReason string

const (
	ProjectionReasonNone                ProjectionReason = ""
	ProjectionReasonRecordAhead         ProjectionReason = "record_ahead"
	ProjectionReasonInactive            ProjectionReason = "inactive"
	ProjectionReasonCorruptLifecycle    ProjectionReason = "corrupt_lifecycle"
	ProjectionReasonCommonBarrier       ProjectionReason = "common_barrier"
	ProjectionReasonResolutionFailed    ProjectionReason = "resolution_failed"
	ProjectionReasonLockArtifactChanged ProjectionReason = "lock_artifact_changed"
)

type RecoveryReason string

const (
	RecoveryReasonRecordAhead         RecoveryReason = "record_ahead"
	RecoveryReasonPlanned             RecoveryReason = "planned"
	RecoveryReasonPublishing          RecoveryReason = "publishing"
	RecoveryReasonConflict            RecoveryReason = "conflict"
	RecoveryReasonSuperseded          RecoveryReason = "superseded"
	RecoveryReasonTerminalClear       RecoveryReason = "terminal_clear"
	RecoveryReasonActiveEvidence      RecoveryReason = "active_evidence"
	RecoveryReasonHealthyClear        RecoveryReason = "healthy_clear"
	RecoveryReasonCorruptLifecycle    RecoveryReason = "corrupt_lifecycle"
	RecoveryReasonCommonBarrier       RecoveryReason = "common_barrier"
	RecoveryReasonResolutionFailed    RecoveryReason = "resolution_failed"
	RecoveryReasonLockArtifactChanged RecoveryReason = "lock_artifact_changed"
)

type RecoveryProjection struct {
	Key                string
	Scope              Scope
	SeriesID           string
	GenerationID       string
	PreparedManifestID string
	Fingerprint        string
	Kind               string
	State              ProjectionState
	Reason             RecoveryReason
	Cleared            bool
}

type SuppressionProjection struct {
	MessageIDs []string
	Threads    []string
}

// EligibilityClaim is the complete immutable authority observation checked a
// second time by InvocationGuard. Live receipt generation is intentionally not
// stored: it is the sole mutable field excluded by the token contract.
type EligibilityClaim struct {
	SeriesID           string
	Scope              Scope
	GenerationID       string
	PreparedManifestID string
	PreparedSHA256     string
	ActiveManifestID   string
	ActiveSHA256       string
	Role               string
	Ordinal            int
	Gate               string
	Action             string
	Target             string
	QuestionMessageID  string
	ReceiptSHA256      string
	Receipt            operatorauth.ReleaseDeliveryReceiptTuple
	Token              string
}

type ChildLeaf struct {
	Role              string
	Ordinal           int
	Thread            string
	QuestionMessageID string
	Eligible          bool
	Reason            string
}

type SeriesLeaf struct {
	Scope              Scope
	SeriesID           string
	GenerationID       string
	PreparedManifestID string
	State              ProjectionState
	Reason             ProjectionReason
	RecordAhead        bool
	Children           []ChildLeaf
}

type Resolution struct {
	Disposition string
	Reason      string
	Degradation *Degradation
	Claim       *EligibilityClaim
	Leaves      []SeriesLeaf
	Suppression SuppressionProjection
	Recovery    []RecoveryProjection
}

const (
	ResolutionEligible      = "eligible"
	ResolutionSuppressed    = "suppressed"
	ResolutionIneligible    = "ineligible"
	ResolutionNotApplicable = "not_applicable"
	ResolutionCommonBarrier = "common_barrier"
	ResolutionDegraded      = ResolutionCommonBarrier
)

type lockedEvidence struct {
	root    string
	groups  []releaseMessageGroup
	adapter InspectionAdapter
}

// ResolveSessionSeries is the single session-level eligibility projection. It
// acquires stores in canonical order before any receipt or mailbox read.
func ResolveSessionSeries(scope SessionScope, query ResolveQuery, adapter InspectionAdapter) (Resolution, error) {
	if adapter == nil {
		return Resolution{}, fmt.Errorf("release inspection adapter is required")
	}
	normalized, err := normalizeSessionScope(scope)
	if err != nil {
		return Resolution{}, err
	}
	resolveScope := Scope{ProjectDir: normalized.ProjectDir, Profile: normalized.Profile, Session: normalized.Session, NamespaceGeneration: normalized.NamespaceGeneration}
	result := Resolution{Disposition: ResolutionNotApplicable, Reason: "no exact active release claim"}
	evidence := lockedEvidence{adapter: adapter}

	rootDir, _, items, enumerateErr := enumerateSessionSeries(normalized)
	if rootDir != nil {
		defer rootDir.Close()
	}
	defer closeEnumerated(items)
	if enumerateErr != nil {
		setCommonBarrier(&result, DegradationEnumerationFailed, "release series enumeration is incomplete")
	}
	held := make([]heldSeriesInspection, 0, len(items))
	for _, item := range items {
		h, acquireErr := acquireSharedSeries(item)
		if acquireErr != nil {
			code := DegradationLockUnavailable
			if errors.Is(acquireErr, ErrStoreBusy) {
				code = DegradationStoreBusy
			}
			setCommonBarrier(&result, code, "one or more release stores could not be inspected under a shared lock")
			continue
		}
		held = append(held, h)
	}

	inspections := make([]SeriesInspection, 0, len(held))
	inspectionFailures := make(map[string]error)
	for _, h := range held {
		inspection, inspectErr := inspectSeriesLocked(h.item, h.artifact)
		if inspectErr != nil {
			identified, identifyErr := identifySeriesLocked(h.item, h.artifact)
			if identifyErr != nil {
				setCommonBarrier(&result, DegradationIdentityUnavailable, "a release series has no validated prepared identity")
				continue
			}
			inspection = identified
			inspectionFailures[inspection.SeriesID] = inspectErr
		}
		inspections = append(inspections, inspection)
	}

	// Root resolution and the single mailbox scan happen only after every
	// available store lease has been acquired and remain inside those leases.
	evidence.root, err = adapter.ResolveSessionRoot(resolveScope)
	if err != nil || !filepath.IsAbs(evidence.root) || filepath.Clean(evidence.root) != evidence.root {
		setCommonBarrier(&result, DegradationRootUnavailable, "resolved session root is unavailable or non-canonical")
	} else {
		messages, scanWarnings := adapter.ScanSessionMessages(evidence.root, time.Now)
		evidence.groups = groupReleaseMessages(messages)
		for _, group := range evidence.groups {
			for _, copy := range group.Copies {
				if _, present := copy.Context["release_child"]; present {
					result.Suppression.MessageIDs = append(result.Suppression.MessageIDs, group.Message.ID)
					break
				}
			}
		}
		if len(scanWarnings) != 0 {
			setCommonBarrier(&result, DegradationScanWarning, fmt.Sprintf("release mailbox scan produced %d warning(s)", len(scanWarnings)))
		}
	}

	for _, inspection := range inspections {
		appendValidatedSuppression(&result.Suppression, inspection)
		if failure := inspectionFailures[inspection.SeriesID]; failure != nil {
			result.Leaves = append(result.Leaves, corruptProjectionLeaf(inspection, ProjectionReasonCorruptLifecycle))
			result.Recovery = append(result.Recovery, recoveryProjection(inspection, "inspect_corrupt_lifecycle", "", false, RecoveryReasonCorruptLifecycle))
			continue
		}
		if result.Degradation != nil {
			result.Leaves = append(result.Leaves, projectionLeaf(inspection, ProjectionReasonCommonBarrier))
			result.Recovery = append(result.Recovery, recoveryProjection(inspection, "inspect_common_barrier", "", false, RecoveryReasonCommonBarrier))
			continue
		}
		leaf, claims, suppress, recovery, resolveErr := resolveSeriesLocked(inspection, query, evidence)
		if resolveErr != nil {
			result.Leaves = append(result.Leaves, corruptProjectionLeaf(inspection, ProjectionReasonResolutionFailed))
			result.Recovery = append(result.Recovery, recoveryProjection(inspection, "inspect_series_evidence", "", false, RecoveryReasonResolutionFailed))
			continue
		}
		result.Leaves = append(result.Leaves, leaf)
		result.Suppression.MessageIDs = append(result.Suppression.MessageIDs, suppress.MessageIDs...)
		result.Suppression.Threads = append(result.Suppression.Threads, suppress.Threads...)
		result.Recovery = append(result.Recovery, recovery...)
		if len(claims) != 0 {
			if result.Claim != nil || len(claims) != 1 {
				return Resolution{}, fmt.Errorf("internal invariant: multiple exact compound release claims")
			}
			claim := claims[0]
			result.Claim = &claim
		}
	}

	// Release only after receipt reads and eligibility projection. A verified
	// prepared identity lets a close/artifact failure isolate to that series.
	for i := len(held) - 1; i >= 0; i-- {
		if closeErr := held[i].closeAndVerify(); closeErr != nil {
			var identified *SeriesInspection
			for j := range inspections {
				if inspections[j].SeriesID == held[i].item.name {
					identified = &inspections[j]
					break
				}
			}
			if identified == nil {
				setCommonBarrier(&result, DegradationLockChanged, "a release store lock artifact changed during inspection")
				continue
			}
			if result.Claim != nil && result.Claim.SeriesID == identified.SeriesID {
				result.Claim = nil
			}
			for j := range result.Leaves {
				if result.Leaves[j].SeriesID == identified.SeriesID {
					result.Leaves[j] = corruptProjectionLeaf(*identified, ProjectionReasonLockArtifactChanged)
				}
			}
			replaceSeriesRecovery(&result.Recovery, recoveryProjection(*identified, "inspect_lock_artifact", "", false, RecoveryReasonLockArtifactChanged))
		}
	}
	normalizeSuppression(&result.Suppression)
	if result.Degradation != nil {
		result.Claim = nil
		return result, nil
	}
	if result.Claim != nil {
		result.Disposition = ResolutionEligible
		result.Reason = "exact active compound release claim"
		return result, nil
	}
	if query.MessageID != "" && slices.Contains(result.Suppression.MessageIDs, query.MessageID) || slices.Contains(result.Suppression.Threads, query.Gate) {
		result.Disposition = ResolutionSuppressed
		result.Reason = "query is claimed by exact release evidence"
	}
	return result, nil
}

func normalizeSuppression(projection *SuppressionProjection) {
	sort.Strings(projection.MessageIDs)
	projection.MessageIDs = slices.Compact(projection.MessageIDs)
	sort.Strings(projection.Threads)
	projection.Threads = slices.Compact(projection.Threads)
}

func appendValidatedSuppression(projection *SuppressionProjection, inspection SeriesInspection) {
	for i, child := range inspection.Snapshot.Prepared.Children {
		projection.Threads = append(projection.Threads, child.Thread)
		if inspection.Snapshot.Active != nil && i < len(inspection.Snapshot.Active.Children) {
			projection.MessageIDs = append(projection.MessageIDs, inspection.Snapshot.Active.Children[i].QuestionMessageID)
		}
	}
}

func setCommonBarrier(result *Resolution, code DegradationCode, reason string) {
	if result.Degradation != nil {
		return
	}
	result.Disposition = ResolutionCommonBarrier
	result.Reason = reason
	result.Degradation = &Degradation{Code: code, Reason: reason}
}

func boundedLifecycleState(value string) ProjectionState {
	switch value {
	case operatorauth.ReleaseStatePlanned, operatorauth.ReleaseStatePublishing, operatorauth.ReleaseStateActive, operatorauth.ReleaseStateConflict, operatorauth.ReleaseStateSuperseded, operatorauth.ReleaseStateAborted:
		return ProjectionState(value)
	default:
		return ProjectionStateUnknown
	}
}

func recoveryProjection(inspection SeriesInspection, kind string, state ProjectionState, cleared bool, reason RecoveryReason) RecoveryProjection {
	prepared := inspection.Snapshot.Prepared
	projection := RecoveryProjection{
		Key: recoveryProjectionKey(inspection.Scope, inspection.SeriesID), Scope: inspection.Scope,
		SeriesID: inspection.SeriesID, GenerationID: prepared.GenerationID,
		PreparedManifestID: prepared.PreparedManifestID,
		Kind:               kind, State: state, Reason: reason, Cleared: cleared,
	}
	canonical := struct {
		SeriesID           string `json:"series_id"`
		GenerationID       string `json:"generation_id"`
		PreparedManifestID string `json:"prepared_manifest_id"`
		Kind               string `json:"kind"`
		State              string `json:"state,omitempty"`
		Reason             string `json:"reason"`
		Cleared            bool   `json:"cleared"`
	}{inspection.SeriesID, prepared.GenerationID, prepared.PreparedManifestID, projection.Kind, string(projection.State), string(projection.Reason), projection.Cleared}
	b, _ := json.Marshal(canonical)
	digest := sha256.Sum256(b)
	projection.Fingerprint = "release-recovery-fingerprint-v1-" + hex.EncodeToString(digest[:])
	return projection
}

func recoveryProjectionKey(scope Scope, seriesID string) string {
	canonical := struct {
		ProjectDir          string `json:"project_dir"`
		Profile             string `json:"profile"`
		Session             string `json:"session"`
		NamespaceGeneration string `json:"namespace_generation"`
		SeriesID            string `json:"series_id"`
	}{scope.ProjectDir, scope.Profile, scope.Session, scope.NamespaceGeneration, seriesID}
	b, _ := json.Marshal(canonical)
	digest := sha256.Sum256(b)
	return "release-recovery-v1-" + hex.EncodeToString(digest[:])
}

func replaceSeriesRecovery(recovery *[]RecoveryProjection, replacement RecoveryProjection) {
	out := (*recovery)[:0]
	for _, item := range *recovery {
		if item.Key != replacement.Key {
			out = append(out, item)
		}
	}
	*recovery = append(out, replacement)
}

// resolveSeriesLocked is shared by ordinary inspection and the invocation
// guard. The caller must hold the verified store lock descriptor.
func resolveSeriesLocked(inspection SeriesInspection, query ResolveQuery, evidence lockedEvidence) (SeriesLeaf, []EligibilityClaim, SuppressionProjection, []RecoveryProjection, error) {
	leaf := newSeriesLeaf(inspection, "")
	suppression := SuppressionProjection{}
	appendValidatedSuppression(&suppression, inspection)
	if inspection.RecordAhead {
		leaf = projectionLeaf(inspection, ProjectionReasonRecordAhead)
		return leaf, nil, suppression, []RecoveryProjection{recoveryProjection(inspection, "repair_active_pointer", "", false, RecoveryReasonRecordAhead)}, nil
	}
	if inspection.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || inspection.Snapshot.Active == nil {
		leaf = projectionLeaf(inspection, ProjectionReasonInactive)
		var recovery []RecoveryProjection
		switch inspection.Snapshot.Pointer.State {
		case operatorauth.ReleaseStatePlanned:
			recovery = append(recovery, recoveryProjection(inspection, "resume_planned_release", "", false, RecoveryReasonPlanned))
		case operatorauth.ReleaseStatePublishing:
			recovery = append(recovery, recoveryProjection(inspection, "reconcile_publishing_release", "", false, RecoveryReasonPublishing))
		case operatorauth.ReleaseStateConflict:
			recovery = append(recovery, recoveryProjection(inspection, "resolve_release_conflict", "", false, RecoveryReasonConflict))
		case operatorauth.ReleaseStateSuperseded:
			recovery = append(recovery, recoveryProjection(inspection, "complete_successor", "", false, RecoveryReasonSuperseded))
		case operatorauth.ReleaseStateAborted:
			recovery = append(recovery, recoveryProjection(inspection, "release_series", ProjectionStateAborted, true, RecoveryReasonTerminalClear))
		default:
			return leaf, nil, suppression, nil, fmt.Errorf("unsupported release lifecycle state %q", inspection.Snapshot.Pointer.State)
		}
		return leaf, nil, suppression, recovery, nil
	}
	active := inspection.Snapshot.Active
	if len(active.Children) != len(inspection.Snapshot.Prepared.Children) {
		return leaf, nil, suppression, nil, fmt.Errorf("active child count mismatch")
	}
	byID := make(map[string]releaseMessageGroup, len(evidence.groups))
	for _, group := range evidence.groups {
		byID[group.Message.ID] = group
	}
	var claims []EligibilityClaim
	needsRecovery := false
	for i, child := range inspection.Snapshot.Prepared.Children {
		published := active.Children[i]
		childLeaf := ChildLeaf{Role: child.Role, Ordinal: child.Ordinal, Thread: child.Thread, QuestionMessageID: published.QuestionMessageID}
		path, err := evidence.adapter.ExpectedReceiptPath(inspection.Scope, child.Receipt.AttemptID)
		if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			childLeaf.Reason = "canonical receipt path is unavailable"
			leaf.Children = append(leaf.Children, childLeaf)
			needsRecovery = true
			continue
		}
		raw, readErr := evidence.adapter.ReadReceipt(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				childLeaf.Reason = "active receipt is missing"
				leaf.Children = append(leaf.Children, childLeaf)
				needsRecovery = true
				continue
			}
			childLeaf.Reason = "active receipt cannot be read"
			leaf.Children = append(leaf.Children, childLeaf)
			needsRecovery = true
			continue
		}
		bound, decodeErr := decodeBoundReleaseReceiptV2(raw, inspection.Scope, child, path, evidence.root)
		if decodeErr != nil || bound.Tuple == nil || bound.Tuple.MessageID != published.QuestionMessageID || !deliveryReceiptStableEqual(*bound.Tuple, published.Receipt) {
			childLeaf.Reason = "active receipt is malformed or drifted"
			leaf.Children = append(leaf.Children, childLeaf)
			needsRecovery = true
			continue
		}
		group, found := byID[published.QuestionMessageID]
		if !found || !group.Equal || !exactReleaseMessage(group, child) {
			childLeaf.Reason = "exact active message id is absent"
			leaf.Children = append(leaf.Children, childLeaf)
			needsRecovery = true
			continue
		}
		childLeaf.Eligible = true
		childLeaf.Reason = "exact active compound release authority"
		if query.MessageID != published.QuestionMessageID || query.Gate != child.Thread || query.Action != child.Action || query.Target != child.Target {
			leaf.Children = append(leaf.Children, childLeaf)
			continue
		}
		receiptSHA, shaErr := operatorauth.ReleaseDeliveryReceiptSHA256(published.Receipt)
		if shaErr != nil {
			return leaf, nil, suppression, nil, shaErr
		}
		claim := EligibilityClaim{
			SeriesID: inspection.SeriesID, Scope: inspection.Scope,
			GenerationID:       inspection.Snapshot.Prepared.GenerationID,
			PreparedManifestID: inspection.Snapshot.Prepared.PreparedManifestID,
			PreparedSHA256:     inspection.Snapshot.Pointer.PreparedSHA256,
			ActiveManifestID:   inspection.Snapshot.Pointer.ActiveManifestID,
			ActiveSHA256:       inspection.Snapshot.Pointer.ActiveSHA256,
			Role:               child.Role, Ordinal: child.Ordinal, Gate: child.Thread,
			Action: child.Action, Target: child.Target,
			QuestionMessageID: published.QuestionMessageID,
			ReceiptSHA256:     receiptSHA, Receipt: published.Receipt,
		}
		claim.Token = eligibilityToken(claim)
		claims = append(claims, claim)
		childLeaf.Reason = "exact active compound release authority; selected by query"
		leaf.Children = append(leaf.Children, childLeaf)
	}
	var recovery []RecoveryProjection
	if needsRecovery {
		recovery = append(recovery, recoveryProjection(inspection, "inspect_active_evidence", "", false, RecoveryReasonActiveEvidence))
	} else {
		recovery = append(recovery, recoveryProjection(inspection, "release_series", ProjectionStateActive, true, RecoveryReasonHealthyClear))
	}
	return leaf, claims, suppression, recovery, nil
}

func projectionLeaf(inspection SeriesInspection, reason ProjectionReason) SeriesLeaf {
	leaf := newSeriesLeaf(inspection, reason)
	for i, child := range inspection.Snapshot.Prepared.Children {
		projected := ChildLeaf{Role: child.Role, Ordinal: child.Ordinal, Thread: child.Thread, Reason: string(reason)}
		if inspection.Snapshot.Active != nil && i < len(inspection.Snapshot.Active.Children) {
			projected.QuestionMessageID = inspection.Snapshot.Active.Children[i].QuestionMessageID
		}
		leaf.Children = append(leaf.Children, projected)
	}
	return leaf
}

func corruptProjectionLeaf(inspection SeriesInspection, reason ProjectionReason) SeriesLeaf {
	leaf := projectionLeaf(inspection, reason)
	leaf.State = "unknown"
	return leaf
}

func newSeriesLeaf(inspection SeriesInspection, reason ProjectionReason) SeriesLeaf {
	return SeriesLeaf{
		Scope: inspection.Scope, SeriesID: inspection.SeriesID,
		GenerationID:       inspection.Snapshot.Prepared.GenerationID,
		PreparedManifestID: inspection.Snapshot.Prepared.PreparedManifestID,
		State:              boundedLifecycleState(inspection.Snapshot.Pointer.State), Reason: reason,
		RecordAhead: inspection.RecordAhead,
	}
}

func eligibilityToken(claim EligibilityClaim) string {
	canonical := struct {
		Domain             string                                   `json:"domain"`
		Schema             int                                      `json:"schema"`
		SeriesID           string                                   `json:"series_id"`
		Scope              Scope                                    `json:"scope"`
		GenerationID       string                                   `json:"generation_id"`
		PreparedManifestID string                                   `json:"prepared_manifest_id"`
		PreparedSHA256     string                                   `json:"prepared_sha256"`
		ActiveManifestID   string                                   `json:"active_manifest_id"`
		ActiveSHA256       string                                   `json:"active_sha256"`
		Role               string                                   `json:"role"`
		Ordinal            int                                      `json:"ordinal"`
		Gate               string                                   `json:"gate"`
		Action             string                                   `json:"action"`
		Target             string                                   `json:"target"`
		QuestionMessageID  string                                   `json:"question_message_id"`
		ReceiptSHA256      string                                   `json:"receipt_sha256"`
		Receipt            operatorauth.ReleaseDeliveryReceiptTuple `json:"receipt"`
	}{
		Domain: "amq-squad.compound-release.eligibility", Schema: 1,
		SeriesID: claim.SeriesID, Scope: claim.Scope, GenerationID: claim.GenerationID,
		PreparedManifestID: claim.PreparedManifestID, ActiveManifestID: claim.ActiveManifestID,
		PreparedSHA256: claim.PreparedSHA256, ActiveSHA256: claim.ActiveSHA256,
		Role: claim.Role, Ordinal: claim.Ordinal, Gate: claim.Gate, Action: claim.Action,
		Target: claim.Target, QuestionMessageID: claim.QuestionMessageID,
		ReceiptSHA256: claim.ReceiptSHA256, Receipt: claim.Receipt,
	}
	b, _ := json.Marshal(canonical)
	digest := sha256.Sum256(b)
	return eligibilityTokenPrefix + hex.EncodeToString(digest[:])
}
