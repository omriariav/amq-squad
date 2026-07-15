package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/compoundrelease"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type cliReleaseSelectedContext struct {
	ProjectDir          string
	Profile             string
	Session             string
	NamespaceGeneration string
	BaseRoot            string
	SessionRoot         string
}

type cliReleaseDomainClassification struct {
	Disposition cliReleaseDomainDisposition
	Eligible    bool
	Reason      string
	Marker      operatorauth.ReleaseChildContext
	Claim       *compoundrelease.EligibilityClaim
	Resolution  compoundrelease.Resolution
	selected    cliReleaseSelectedContext
	question    state.Message
	query       compoundrelease.ResolveQuery
}

type cliReleaseDomainDisposition string

const (
	cliReleaseDomainUnknown      cliReleaseDomainDisposition = "unknown"
	cliReleaseDomainOrdinary     cliReleaseDomainDisposition = "ordinary"
	cliReleaseDomainReleaseOwned cliReleaseDomainDisposition = "release_owned"
)

type cliReleaseGuardObservation struct {
	question state.Message
	messages []state.Message
}

func (o cliReleaseGuardObservation) Question() state.Message {
	return cloneReleaseStateMessage(o.question)
}

func (o cliReleaseGuardObservation) Messages() []state.Message {
	return cloneReleaseStateMessages(o.messages)
}

type cliReleaseGuardedUse struct {
	guard          *compoundrelease.InvocationGuard
	adapter        *cliReleaseInspectionAdapter
	classification cliReleaseDomainClassification
}

func selectedReleaseContext(ctx contextResolution) cliReleaseSelectedContext {
	return cliReleaseSelectedContext{
		ProjectDir: ctx.ProjectDir, Profile: ctx.Profile, Session: ctx.Session,
		NamespaceGeneration: ctx.NamespaceGeneration, BaseRoot: ctx.BaseRoot, SessionRoot: ctx.Root,
	}
}

func latestGateQuestionInSelectedContext(selected cliReleaseSelectedContext, gate string, now func() time.Time) (state.Message, error) {
	if now == nil {
		now = time.Now
	}
	if err := validateCLIReleaseSelectedContext(selected); err != nil {
		return state.Message{}, err
	}
	msgs, warnings := state.ScanSessionMessages(selected.SessionRoot, now)
	if len(warnings) > 0 {
		return state.Message{}, usageErrorf("message scan degraded; approval fails closed")
	}
	var conflict bool
	msgs, conflict = dedupeSecurityMessages(msgs)
	if conflict {
		return state.Message{}, usageErrorf("conflicting mailbox copies share a message id; approval fails closed")
	}
	latest := latestGateQuestionCandidate(msgs, gate)
	if latest == nil {
		return state.Message{}, usageErrorf("no gate question on %s", gate)
	}
	return cloneReleaseStateMessage(*latest), nil
}

func classifyCLIReleaseQuestion(selected cliReleaseSelectedContext, question state.Message) (cliReleaseDomainClassification, error) {
	classification := cliReleaseDomainClassification{
		Disposition: cliReleaseDomainUnknown,
		selected:    selected,
		question:    cloneReleaseStateMessage(question),
	}
	_, initialMarkerPresent := question.Context["release_child"]
	if initialMarkerPresent {
		classification.Disposition = cliReleaseDomainReleaseOwned
	}
	if err := validateCLIReleaseSelectedContext(selected); err != nil {
		return classification, err
	}

	query := compoundrelease.ResolveQuery{MessageID: question.ID, Gate: question.Thread}
	if question.AuthorizationRequestValid && question.AuthorizationRequest != nil {
		query.Action, query.Target = question.AuthorizationRequest.Action, question.AuthorizationRequest.Target
	}
	classification.query = query

	adapter := newCLIReleaseInspectionAdapter(
		selected.ProjectDir, selected.Profile, selected.Session, selected.NamespaceGeneration,
		selected.BaseRoot, selected.SessionRoot,
	)
	scope := compoundrelease.SessionScope{
		ProjectDir: selected.ProjectDir, Profile: selected.Profile, Session: selected.Session,
		NamespaceGeneration: selected.NamespaceGeneration,
	}
	resolution, err := resolveCompoundReleaseAttention(scope, query, adapter)
	if err != nil {
		return classification, err
	}
	classification.Resolution = resolution
	capture := cloneOperatorSessionCapture(adapter.capture)
	physicalMarker := false
	for _, message := range capture.Messages {
		if message.ID == question.ID {
			if _, present := message.Context["release_child"]; present {
				physicalMarker = true
			}
		}
	}
	if physicalMarker {
		classification.Disposition = cliReleaseDomainReleaseOwned
	}
	if adapter.scanCalls != 1 {
		return classification, fmt.Errorf("release-domain classifier requires exactly one coherent mailbox scan")
	}
	if !capture.Scanned || len(capture.Warnings) != 0 {
		return classification, fmt.Errorf("release-domain mailbox observation is unavailable or degraded")
	}
	exactSuppressed := slices.Contains(resolution.Suppression.MessageIDs, question.ID)
	if exactSuppressed {
		classification.Disposition = cliReleaseDomainReleaseOwned
	}
	trusted, _, equal := equalCapturedMessageGroup(capture.Messages, question.ID, "")
	if !equal || !securityMessageEqual(trusted, question) {
		return classification, fmt.Errorf("selected gate question is absent, duplicated, or changed")
	}
	deduped, conflict := dedupeSecurityMessages(capture.Messages)
	if conflict {
		return classification, fmt.Errorf("release-domain mailbox contains conflicting message copies")
	}
	latest := latestGateQuestionCandidate(deduped, question.Thread)
	if latest == nil || latest.ID != question.ID || !securityMessageEqual(*latest, trusted) {
		return classification, fmt.Errorf("selected gate question is no longer the current trusted gate question")
	}
	if classification.Disposition != cliReleaseDomainReleaseOwned {
		classification.Disposition = cliReleaseDomainOrdinary
		classification.Reason = "ordinary gate question"
		return classification, nil
	}
	rawMarker, markerPresent := trusted.Context["release_child"]
	if !markerPresent {
		classification.Reason = "release-owned question has no physical release_child marker"
		return classification, nil
	}
	marker, markerErr := operatorauth.DecodeReleaseChild(rawMarker)
	if markerErr != nil {
		classification.Reason = "release_child marker is malformed"
		return classification, nil
	}
	if resolution.Disposition != compoundrelease.ResolutionEligible || resolution.Claim == nil {
		classification.Reason = resolution.Reason
		if classification.Reason == "" {
			classification.Reason = "release-owned question has no exact eligible claim"
		}
		return classification, nil
	}
	if err := validateCLIReleaseMarkerClaim(selected, adapter, trusted, marker, *resolution.Claim); err != nil {
		classification.Reason = err.Error()
		return classification, nil
	}
	claim := cloneCLIReleaseEligibilityClaim(*resolution.Claim)
	classification.Marker = marker
	classification.Claim = &claim
	classification.Eligible = true
	classification.Reason = "exact active compound release claim"
	return classification, nil
}

func validateCLIReleaseSelectedContext(selected cliReleaseSelectedContext) error {
	for name, value := range map[string]string{
		"project": selected.ProjectDir, "profile": selected.Profile, "session": selected.Session,
		"namespace generation": selected.NamespaceGeneration, "base root": selected.BaseRoot, "session root": selected.SessionRoot,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("selected release %s is empty", name)
		}
	}
	if !filepath.IsAbs(selected.ProjectDir) || filepath.Clean(selected.ProjectDir) != selected.ProjectDir ||
		!filepath.IsAbs(selected.BaseRoot) || filepath.Clean(selected.BaseRoot) != selected.BaseRoot ||
		!filepath.IsAbs(selected.SessionRoot) || filepath.Clean(selected.SessionRoot) != selected.SessionRoot {
		return fmt.Errorf("selected release project and roots must be absolute and clean")
	}
	expected := selected.BaseRoot
	if squadnamespace.ProfilesEqual(selected.Profile, team.DefaultProfile) {
		expected = filepath.Join(selected.BaseRoot, selected.Session)
	}
	if selected.SessionRoot != expected {
		return fmt.Errorf("selected release session root %q does not match profile-aware root %q", selected.SessionRoot, expected)
	}
	return nil
}

func validateCLIReleaseMarkerClaim(selected cliReleaseSelectedContext, adapter *cliReleaseInspectionAdapter, question state.Message, marker operatorauth.ReleaseChildContext, claim compoundrelease.EligibilityClaim) error {
	if err := validateCLIReleaseSelectedContext(selected); err != nil {
		return err
	}
	if claim.Scope.ProjectDir != selected.ProjectDir || !squadnamespace.ProfilesEqual(claim.Scope.Profile, selected.Profile) ||
		claim.Scope.Session != selected.Session || claim.Scope.NamespaceGeneration != selected.NamespaceGeneration || claim.Scope.ParentGate != marker.ParentGate {
		return fmt.Errorf("release eligibility claim does not match selected namespace scope")
	}
	if question.ID == "" || claim.QuestionMessageID != question.ID || claim.Receipt.MessageID != question.ID {
		return fmt.Errorf("release question id does not match eligibility claim")
	}
	if marker.GenerationID != claim.GenerationID || marker.PreparedManifestID != claim.PreparedManifestID ||
		marker.Role != claim.Role || marker.Ordinal != claim.Ordinal || marker.Thread != claim.Gate ||
		marker.Action != claim.Action || marker.Target != claim.Target || marker.AttemptID != claim.Receipt.AttemptID {
		return fmt.Errorf("release_child marker does not match eligibility claim")
	}
	if question.Thread != claim.Gate || question.RawThread != claim.Gate || question.From != claim.Receipt.Sender || len(question.To) != 1 || len(claim.Receipt.Recipients) != 1 || !slices.Equal(question.To, claim.Receipt.Recipients) ||
		claim.Receipt.Thread != claim.Gate || claim.Receipt.NamespaceID != squadnamespace.ID(claim.Scope.Profile, claim.Scope.Session) ||
		claim.Receipt.Root != selected.SessionRoot || claim.Scope.Profile == "" || claim.Scope.Session == "" || claim.Scope.NamespaceGeneration == "" || claim.ReceiptSHA256 == "" {
		return fmt.Errorf("release eligibility claim has inconsistent scope or receipt binding")
	}
	receiptSHA, err := operatorauth.ReleaseDeliveryReceiptSHA256(claim.Receipt)
	if err != nil || receiptSHA != claim.ReceiptSHA256 {
		return fmt.Errorf("release eligibility claim receipt digest is invalid")
	}
	expectedPath, err := adapter.ExpectedReceiptPath(claim.Scope, claim.Receipt.AttemptID)
	if err != nil || expectedPath != claim.Receipt.Path {
		return fmt.Errorf("release eligibility claim receipt path is not canonical")
	}
	resolvedRoot, err := adapter.ResolveSessionRoot(claim.Scope)
	if err != nil || resolvedRoot != selected.SessionRoot {
		return fmt.Errorf("release eligibility claim does not resolve to selected session root")
	}
	if !question.AuthorizationRequestValid || question.AuthorizationRequest == nil {
		return fmt.Errorf("release question authorization request is malformed")
	}
	request := question.AuthorizationRequest
	if request.Gate != claim.Gate || request.Thread != claim.Gate || request.GateKind != marker.GateKind || request.Action != claim.Action || request.Target != claim.Target {
		return fmt.Errorf("release question authorization request does not match eligibility claim")
	}
	if request.Namespace.ProjectDir != claim.Scope.ProjectDir || !squadnamespace.ProfilesEqual(request.Namespace.Profile, claim.Scope.Profile) ||
		request.Namespace.Session != claim.Scope.Session || request.Namespace.NamespaceID != squadnamespace.ID(claim.Scope.Profile, claim.Scope.Session) ||
		request.Namespace.Generation != claim.Scope.NamespaceGeneration {
		return fmt.Errorf("release question typed routing does not match eligibility scope")
	}
	return nil
}

func (c cliReleaseDomainClassification) NewGuardedUse() (*cliReleaseGuardedUse, error) {
	if c.Disposition != cliReleaseDomainReleaseOwned || !c.Eligible || c.Claim == nil {
		return nil, fmt.Errorf("exact eligible compound release claim is required")
	}
	frozen := c
	frozen.question = cloneReleaseStateMessage(c.question)
	frozenClaim := cloneCLIReleaseEligibilityClaim(*c.Claim)
	frozen.Claim = &frozenClaim
	adapter := newCLIReleaseInspectionAdapter(
		frozen.selected.ProjectDir, frozen.selected.Profile, frozen.selected.Session, frozen.selected.NamespaceGeneration,
		frozen.selected.BaseRoot, frozen.selected.SessionRoot,
	)
	scope := compoundrelease.SessionScope{
		ProjectDir: frozen.selected.ProjectDir, Profile: frozen.selected.Profile, Session: frozen.selected.Session,
		NamespaceGeneration: frozen.selected.NamespaceGeneration,
	}
	guardClaim := cloneCLIReleaseEligibilityClaim(frozenClaim)
	guard, err := compoundrelease.NewInvocationGuard(scope, frozen.query, guardClaim, adapter)
	if err != nil {
		return nil, err
	}
	return &cliReleaseGuardedUse{guard: guard, adapter: adapter, classification: frozen}, nil
}

func (u *cliReleaseGuardedUse) Run(fn func(cliReleaseGuardObservation) error) error {
	if u == nil || fn == nil {
		return fmt.Errorf("release guarded use and callback are required")
	}
	return u.guard.Run(func() error {
		if u.adapter.scanCalls != 1 {
			return fmt.Errorf("guarded release use requires exactly one fresh mailbox scan")
		}
		capture := cloneOperatorSessionCapture(u.adapter.capture)
		if !capture.Scanned || len(capture.Warnings) != 0 {
			return fmt.Errorf("guarded release mailbox observation is unavailable or degraded")
		}
		question, _, equal := equalCapturedMessageGroup(capture.Messages, u.classification.Claim.QuestionMessageID, "")
		if !equal || !securityMessageEqual(question, u.classification.question) {
			return fmt.Errorf("guarded release question is absent, duplicated, or changed")
		}
		var exactGate []state.Message
		for _, message := range capture.Messages {
			if message.Thread == u.classification.Claim.Gate && message.RawThread == u.classification.Claim.Gate {
				exactGate = append(exactGate, message)
			}
		}
		dedupedGate, conflict := dedupeSecurityMessages(exactGate)
		if conflict {
			return fmt.Errorf("guarded release gate contains conflicting message copies")
		}
		latest := latestGateQuestionCandidate(dedupedGate, u.classification.Claim.Gate)
		if latest == nil || latest.ID != u.classification.Claim.QuestionMessageID || !securityMessageEqual(*latest, question) {
			return fmt.Errorf("guarded release question is no longer the current trusted gate question")
		}
		rawMarker, present := question.Context["release_child"]
		if !present {
			return fmt.Errorf("guarded release question lost its release_child marker")
		}
		marker, err := operatorauth.DecodeReleaseChild(rawMarker)
		if err != nil {
			return err
		}
		if err := validateCLIReleaseMarkerClaim(u.classification.selected, u.adapter, question, marker, *u.classification.Claim); err != nil {
			return err
		}
		observation := cliReleaseGuardObservation{
			question: cloneReleaseStateMessage(question),
			messages: cloneReleaseStateMessages(exactGate),
		}
		return fn(observation)
	})
}

func cloneOperatorSessionCapture(capture operatorSessionCapture) operatorSessionCapture {
	return operatorSessionCapture{
		Messages: cloneReleaseStateMessages(capture.Messages),
		Warnings: append([]state.Warning(nil), capture.Warnings...),
		Scanned:  capture.Scanned,
	}
}

func cloneCLIReleaseEligibilityClaim(claim compoundrelease.EligibilityClaim) compoundrelease.EligibilityClaim {
	claim.Receipt.Recipients = append([]string(nil), claim.Receipt.Recipients...)
	return claim
}

func cloneReleaseStateMessages(messages []state.Message) []state.Message {
	out := make([]state.Message, len(messages))
	for i := range messages {
		out[i] = cloneReleaseStateMessage(messages[i])
	}
	return out
}

func cloneReleaseStateMessage(message state.Message) state.Message {
	message.To = append([]string(nil), message.To...)
	message.Labels = append([]string(nil), message.Labels...)
	if message.AuthorizationRequest != nil {
		request := *message.AuthorizationRequest
		message.AuthorizationRequest = &request
	}
	if message.Approval != nil {
		approval := *message.Approval
		message.Approval = &approval
	}
	if message.Context != nil {
		if raw, err := json.Marshal(message.Context); err == nil {
			var context map[string]any
			if decodeErr := json.Unmarshal(raw, &context); decodeErr == nil {
				message.Context = context
			} else {
				message.Context = failedReleaseContextClone(message.Context, decodeErr)
			}
		} else {
			message.Context = failedReleaseContextClone(message.Context, err)
		}
	}
	return message
}

func failedReleaseContextClone(context map[string]any, err error) map[string]any {
	failed := make(map[string]any, len(context)+1)
	for key := range context {
		failed[key] = nil
	}
	failed["__amq_squad_clone_error__"] = err.Error()
	return failed
}
