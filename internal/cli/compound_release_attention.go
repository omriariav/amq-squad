package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/compoundrelease"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type compoundReleaseAttentionProjection struct {
	Items                []operatorAttention
	SuppressedMessageIDs []string
	// SuppressedThreads is retained as typed evidence for inspection and tests.
	// It is deliberately not a fallback filter: unrelated traffic on a
	// prepared thread remains ordinary operator attention.
	SuppressedThreads []string
}

type operatorSessionCapture struct {
	Messages []state.Message
	Warnings []state.Warning
	Scanned  bool
}

type projectedOperatorAttention struct {
	Items    []operatorAttention
	Snapshot state.Snapshot
	Captures map[string]operatorSessionCapture
}

var resolveCompoundReleaseAttention = compoundrelease.ResolveSessionSeries
var scanOperatorSessionMessages = state.ScanSessionMessages

type cliReleaseInspectionAdapter struct {
	projectDir          string
	profile             string
	session             string
	namespaceGeneration string
	root                string
	rootErr             error
	capture             operatorSessionCapture
	scanCalls           int
}

func newCLIReleaseInspectionAdapter(projectDir, profile, session, namespaceGeneration, selectedBaseRoot, observedRoot string) *cliReleaseInspectionAdapter {
	profile = squadnamespace.NormalizeProfile(profile)
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return &cliReleaseInspectionAdapter{rootErr: err}
	}
	projectDir = filepath.Clean(projectDir)
	baseRoot := strings.TrimSpace(selectedBaseRoot)
	expectedRoot := ""
	rootErr := error(nil)
	if baseRoot == "" || !filepath.IsAbs(baseRoot) || filepath.Clean(baseRoot) != baseRoot {
		rootErr = fmt.Errorf("selected AMQ base root must be nonempty, absolute, and clean: %q", selectedBaseRoot)
	} else if strings.TrimSpace(session) == "" {
		rootErr = fmt.Errorf("selected AMQ session must be nonempty")
	} else if profile == team.DefaultProfile {
		expectedRoot = filepath.Join(baseRoot, strings.TrimSpace(session))
	} else {
		expectedRoot = baseRoot
	}
	adapter := &cliReleaseInspectionAdapter{
		projectDir: projectDir, profile: profile, session: strings.TrimSpace(session),
		namespaceGeneration: namespaceGeneration, root: expectedRoot, rootErr: rootErr,
	}
	observedRoot = strings.TrimSpace(observedRoot)
	if adapter.rootErr == nil && (observedRoot == "" || !filepath.IsAbs(observedRoot) || filepath.Clean(observedRoot) != observedRoot || observedRoot != expectedRoot) {
		adapter.rootErr = fmt.Errorf("observed session root %q does not match selected root %q", observedRoot, expectedRoot)
	}
	return adapter
}

func (a *cliReleaseInspectionAdapter) validateScope(scope compoundrelease.Scope) error {
	if filepath.Clean(scope.ProjectDir) != a.projectDir || !squadnamespace.ProfilesEqual(scope.Profile, a.profile) || scope.Session != a.session || scope.NamespaceGeneration != a.namespaceGeneration {
		return fmt.Errorf("compound release scope diverges from selected CLI namespace")
	}
	return nil
}

func (a *cliReleaseInspectionAdapter) ResolveSessionRoot(scope compoundrelease.Scope) (string, error) {
	if err := a.validateScope(scope); err != nil {
		return "", err
	}
	if a.rootErr != nil {
		return "", a.rootErr
	}
	return a.root, nil
}

func (a *cliReleaseInspectionAdapter) ExpectedReceiptPath(scope compoundrelease.Scope, attemptID string) (string, error) {
	if err := a.validateScope(scope); err != nil {
		return "", err
	}
	if !safeReceiptAttemptID(attemptID) {
		return "", fmt.Errorf("unsafe delivery receipt attempt id %q", attemptID)
	}
	root, dir, err := openReceiptDirRoot(a.projectDir, a.profile, a.session, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return filepath.Join(dir, attemptID+".json"), nil
}

func (a *cliReleaseInspectionAdapter) ReadReceipt(path string) ([]byte, error) {
	root, dir, err := openReceiptDirRoot(a.projectDir, a.profile, a.session, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	attemptID := strings.TrimSuffix(name, ".json")
	if !strings.HasSuffix(name, ".json") || !safeReceiptAttemptID(attemptID) || filepath.Clean(path) != filepath.Join(dir, name) {
		return nil, fmt.Errorf("delivery receipt path is outside the selected namespace: %s", path)
	}
	return readDeliveryReceiptRawAt(root, name, path)
}

func (a *cliReleaseInspectionAdapter) ScanSessionMessages(root string, now func() time.Time) ([]state.Message, []state.Warning) {
	a.scanCalls++
	if root != a.root {
		warning := state.Warning{Path: root, Reason: "compound release scan root diverges from selected namespace"}
		a.capture = operatorSessionCapture{Warnings: []state.Warning{warning}, Scanned: true}
		return nil, a.capture.Warnings
	}
	messages, warnings := scanOperatorSessionMessages(root, now)
	a.capture = operatorSessionCapture{Messages: cloneReleaseStateMessages(messages), Warnings: append([]state.Warning(nil), warnings...), Scanned: true}
	return cloneReleaseStateMessages(a.capture.Messages), append([]state.Warning(nil), a.capture.Warnings...)
}

func collectProjectedOperatorAttention(t team.Team, projectDir, profile string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time) (projectedOperatorAttention, error) {
	filtered := snap
	filtered.Sessions = append([]state.Session(nil), snap.Sessions...)
	captures := make(map[string]operatorSessionCapture)
	var releaseItems []operatorAttention
	for i := range filtered.Sessions {
		sess := &filtered.Sessions[i]
		if !squadnamespace.ProfilesEqual(profile, sess.TeamProfile) || onlySession != "" && sess.Name != onlySession {
			continue
		}
		generation, err := namespaceEndpointGeneration(projectDir, profile, sess.Name)
		if err != nil {
			return projectedOperatorAttention{}, fmt.Errorf("resolve compound release namespace generation: %w", err)
		}
		projection, capture, err := projectCompoundReleaseSession(projectDir, profile, generation, operatorHandle, filtered.BaseRoot, *sess, now)
		if err != nil {
			return projectedOperatorAttention{}, err
		}
		releaseItems = append(releaseItems, projection.Items...)
		if capture.Scanned {
			filteredMessages := filterClaimedReleaseMessageIDs(capture.Messages, projection.SuppressedMessageIDs)
			sess.Coordination = state.ProjectCoordination(filteredMessages, sess.Agents, capture.Warnings, now, state.Thresholds{OperatorHandle: operatorHandle})
			capture = operatorSessionCapture{Messages: filteredMessages, Warnings: capture.Warnings, Scanned: true}
		}
		captures[sess.Name] = capture
	}
	items := collectOperatorAttention(projectDir, profile, filtered, operatorHandle, onlySession, now)
	items = mergeOperatorAttention(items, collectGateAttentionProjectionCaptured(t, projectDir, profile, filtered, operatorHandle, onlySession, now, captures))
	items = mergeOperatorAttention(items, releaseItems)
	return projectedOperatorAttention{Items: items, Snapshot: filtered, Captures: captures}, nil
}

func projectCompoundReleaseSession(projectDir, profile, generation, operatorHandle, selectedBaseRoot string, sess state.Session, now time.Time) (compoundReleaseAttentionProjection, operatorSessionCapture, error) {
	adapter := newCLIReleaseInspectionAdapter(projectDir, profile, sess.Name, generation, selectedBaseRoot, sess.Root)
	resolution, err := resolveCompoundReleaseAttention(compoundrelease.SessionScope{
		ProjectDir: adapter.projectDir, Profile: adapter.profile, Session: adapter.session, NamespaceGeneration: generation,
	}, compoundrelease.ResolveQuery{}, adapter)
	if err != nil {
		return compoundReleaseAttentionProjection{}, adapter.capture, err
	}
	if adapter.scanCalls > 1 {
		return compoundReleaseAttentionProjection{}, adapter.capture, fmt.Errorf("internal invariant: compound release resolver scanned session %d times", adapter.scanCalls)
	}
	projection := compoundReleaseAttentionProjection{
		SuppressedMessageIDs: append([]string(nil), resolution.Suppression.MessageIDs...),
		SuppressedThreads:    append([]string(nil), resolution.Suppression.Threads...),
	}
	inspect := compoundReleaseInspectCommand(adapter.projectDir, adapter.profile, adapter.session)
	for _, leaf := range resolution.Leaves {
		for _, child := range leaf.Children {
			key := compoundReleaseChildAttentionKey(adapter.profile, adapter.session, leaf.SeriesID, child.Role, child.Ordinal)
			if !child.Eligible || child.QuestionMessageID == "" {
				projection.Items = append(projection.Items, compoundReleaseChildTombstone(adapter.profile, adapter.session, key, leaf, child))
				continue
			}
			message, operatorUnread, ok := equalCapturedMessageGroup(adapter.capture.Messages, child.QuestionMessageID, operatorHandle)
			if !ok {
				projection.Items = append(projection.Items, compoundReleaseChildTombstone(adapter.profile, adapter.session, key, leaf, child))
				continue
			}
			age := now.Sub(message.Created)
			if age < 0 {
				age = 0
			}
			projection.Items = append(projection.Items, operatorAttention{
				EventType: "compound_release_child", Key: key,
				Profile: adapter.profile, Session: adapter.session, NamespaceID: squadnamespace.ID(adapter.profile, adapter.session),
				Thread: child.Thread, LatestID: child.QuestionMessageID, From: message.From, Subject: message.Subject, Kind: message.Kind,
				Role:   child.Role,
				Reason: state.ClassifyAttnSubject(message.Subject), Age: roundDuration(age).String(), LastEventAt: message.Created,
				Escalation: string(state.OperatorGateEscalationForAge(age)), Inspect: inspect,
				Respond: notifyRespondCommand(operatorHandle, message.From, child.Thread, state.ClassifyAttnSubject(message.Subject)),
				Summary: child.Reason, Actionable: true, Answerable: true, Unread: operatorUnread,
			})
		}
	}
	for _, recovery := range resolution.Recovery {
		projection.Items = append(projection.Items, compoundReleaseRecoveryAttention(adapter.projectDir, adapter.profile, adapter.session, recovery, inspect))
	}
	if resolution.Degradation != nil {
		projection.Items = append(projection.Items, compoundReleaseBarrierAttention(adapter.projectDir, adapter.profile, adapter.session, *resolution.Degradation, inspect, false))
	} else {
		projection.Items = append(projection.Items, compoundReleaseBarrierAttention(adapter.projectDir, adapter.profile, adapter.session, compoundrelease.Degradation{}, inspect, true))
	}
	sortOperatorAttention(projection.Items)
	return projection, adapter.capture, nil
}

func filterClaimedReleaseMessageIDs(messages []state.Message, ids []string) []state.Message {
	claimed := make(map[string]bool, len(ids))
	for _, id := range ids {
		claimed[id] = true
	}
	filtered := make([]state.Message, 0, len(messages))
	for _, message := range messages {
		if !claimed[message.ID] {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func equalCapturedMessageGroup(messages []state.Message, id, operatorHandle string) (state.Message, bool, bool) {
	var copies []state.Message
	operatorUnread := false
	for _, message := range messages {
		if message.ID == id {
			copies = append(copies, message)
			if message.Owner == operatorHandle && message.State == state.MailboxNew {
				operatorUnread = true
			}
		}
	}
	if len(copies) == 0 {
		return state.Message{}, false, false
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].Path < copies[j].Path })
	want := comparableCapturedMessage(copies[0])
	for _, copy := range copies[1:] {
		if !reflect.DeepEqual(want, comparableCapturedMessage(copy)) {
			return state.Message{}, false, false
		}
	}
	return copies[0], operatorUnread, true
}

func comparableCapturedMessage(message state.Message) state.Message {
	message.Owner = ""
	message.State = ""
	message.Path = ""
	return message
}

func compoundReleaseRecoveryAttention(projectDir, profile, session string, recovery compoundrelease.RecoveryProjection, inspect string) operatorAttention {
	return operatorAttention{
		EventType: "compound_release_recovery", Key: recovery.Key, LatestID: recovery.Fingerprint,
		Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session), Thread: recovery.Scope.ParentGate,
		Subject: "compound release recovery", Kind: state.KindStatus, Reason: state.AttnGeneric,
		Inspect: inspect, Respond: "", Summary: string(recovery.Reason), Cleared: recovery.Cleared,
		Actionable: !recovery.Cleared, Answerable: false,
	}
}

func compoundReleaseChildTombstone(profile, session, key string, leaf compoundrelease.SeriesLeaf, child compoundrelease.ChildLeaf) operatorAttention {
	fingerprint := "compound-release-child-clear-v1-" + digestAttentionParts(leaf.GenerationID, leaf.PreparedManifestID, child.Role, fmt.Sprint(child.Ordinal), child.Reason)
	return operatorAttention{
		EventType: "compound_release_child", Key: key, LatestID: fingerprint,
		Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session), Thread: child.Thread,
		Role: child.Role, Cleared: true, Actionable: false, Answerable: false,
	}
}

func compoundReleaseBarrierAttention(projectDir, profile, session string, degradation compoundrelease.Degradation, inspect string, cleared bool) operatorAttention {
	key := compoundReleaseBarrierKey(projectDir, profile, session)
	fingerprint := compoundReleaseBarrierFingerprint(degradation)
	return operatorAttention{
		EventType: "compound_release_degraded", Key: key, LatestID: fingerprint,
		Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session), Thread: "release/session",
		Subject: "compound release inspection degraded", Kind: state.KindStatus, Reason: state.AttnGeneric,
		Inspect: inspect, Respond: "", Summary: degradation.Reason, Cleared: cleared,
		Actionable: !cleared, Answerable: false,
	}
}

func compoundReleaseBarrierKey(projectDir, profile, session string) string {
	return "compound-release-session-v1-" + digestAttentionParts(projectDir, squadnamespace.NormalizeProfile(profile), session)
}

func compoundReleaseBarrierFingerprint(degradation compoundrelease.Degradation) string {
	if degradation.Code == "" {
		return "compound-release-session-clear-v1"
	}
	return "compound-release-session-degraded-v1-" + digestAttentionParts(string(degradation.Code), degradation.Reason)
}

func compoundReleaseChildAttentionKey(profile, session, seriesID, role string, ordinal int) string {
	return squadnamespace.NormalizeProfile(profile) + "/" + session + "\x00compound_release_child\x00" + seriesID + "\x00" + role + "\x00" + fmt.Sprint(ordinal)
}

func digestAttentionParts(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func compoundReleaseInspectCommand(projectDir, profile, session string) string {
	return "amq-squad operator status --project " + notifyShellQuote(projectDir) + " --profile " + notifyShellQuote(squadnamespace.NormalizeProfile(profile)) + " --session " + notifyShellQuote(session) + " --json"
}

var _ compoundrelease.InspectionAdapter = (*cliReleaseInspectionAdapter)(nil)
