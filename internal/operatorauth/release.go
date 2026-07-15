package operatorauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ReleaseSchemaVersion      = 1
	ReleaseChildSchemaVersion = 2
	ReleaseManifestVersion    = 1
)

const (
	ReleaseStatePlanned    = "planned"
	ReleaseStatePublishing = "publishing"
	ReleaseStateActive     = "active"
	ReleaseStateSuperseded = "superseded"
	ReleaseStateAborted    = "aborted"
	ReleaseStateConflict   = "conflict"
)

const (
	ReleaseChildTag           = "tag"
	ReleaseChildGitHubRelease = "github_release"
)

// ReleaseNote is deliberately structured even though its first schema has a
// single field. This avoids turning untyped prose into a future authority
// extension. Summary is integrity-bearing and is mirrored into both atomic
// child requests.
type ReleaseNote struct {
	Summary string `json:"summary"`
}

// ReleaseSpec is the domain-neutral compound-release intent. Targets are
// exact catalog action bindings; this layer does not parse GitHub concepts.
type ReleaseSpec struct {
	SchemaVersion       int              `json:"schema_version"`
	TaxonomyVersion     int              `json:"taxonomy_version"`
	Namespace           NamespaceBinding `json:"namespace"`
	ParentGate          string           `json:"parent_gate"`
	RequesterHandle     string           `json:"requester_handle"`
	OperatorHandle      string           `json:"operator_handle"`
	TagTarget           string           `json:"tag_target"`
	GitHubReleaseTarget string           `json:"github_release_target"`
	Note                ReleaseNote      `json:"note"`
}

// ReleaseChildContext is the strict marker carried beside the unchanged v1
// authorization_request context. It is not authority by itself.
type ReleaseChildContext struct {
	SchemaVersion      int    `json:"schema_version"`
	TaxonomyVersion    int    `json:"taxonomy_version"`
	ReleaseID          string `json:"release_id"`
	GenerationID       string `json:"generation_id"`
	Generation         uint64 `json:"generation"`
	ChildCount         int    `json:"child_count"`
	SpecSHA256         string `json:"spec_sha256"`
	PreparedManifestID string `json:"prepared_manifest_id"`
	ParentGate         string `json:"parent_gate"`
	Role               string `json:"role"`
	Ordinal            int    `json:"ordinal"`
	Thread             string `json:"thread"`
	GateKind           string `json:"gate_kind"`
	Action             string `json:"action"`
	Target             string `json:"target"`
	RenderedSHA256     string `json:"rendered_sha256"`
	AttemptID          string `json:"attempt_id"`
}

// ReleaseReceiptIntent is preallocated before any send. Its fields are the
// stable part of the owned delivery-receipt tuple.
type ReleaseReceiptIntent struct {
	AttemptID         string `json:"attempt_id"`
	Kind              string `json:"kind"`
	Sender            string `json:"sender"`
	Recipient         string `json:"recipient"`
	Thread            string `json:"thread"`
	NamespaceID       string `json:"namespace_id"`
	TargetIdentity    string `json:"target_identity"`
	MinimumGeneration uint64 `json:"minimum_generation"`
}

type ReleaseChildPlan struct {
	Role                 string               `json:"role"`
	Ordinal              int                  `json:"ordinal"`
	Thread               string               `json:"thread"`
	GateKind             string               `json:"gate_kind"`
	Action               string               `json:"action"`
	Target               string               `json:"target"`
	Subject              string               `json:"subject"`
	Body                 string               `json:"body"`
	RenderedSHA256       string               `json:"rendered_sha256"`
	AuthorizationRequest GateRequestContext   `json:"authorization_request"`
	ReleaseChild         ReleaseChildContext  `json:"release_child"`
	Receipt              ReleaseReceiptIntent `json:"receipt"`
}

type PreparedReleaseManifest struct {
	SchemaVersion      int                `json:"schema_version"`
	TaxonomyVersion    int                `json:"taxonomy_version"`
	State              string             `json:"state"`
	ReleaseID          string             `json:"release_id"`
	Generation         uint64             `json:"generation"`
	GenerationID       string             `json:"generation_id"`
	SpecSHA256         string             `json:"spec_sha256"`
	PreparedManifestID string             `json:"prepared_manifest_id"`
	Spec               ReleaseSpec        `json:"spec"`
	Children           []ReleaseChildPlan `json:"children"`
}

// ReleaseDeliveryReceiptTuple is the exact owned tuple adopted by an active
// publication manifest. Approval answers and approval receipts are expressly
// not part of activation.
type ReleaseDeliveryReceiptTuple struct {
	AttemptID         string   `json:"attempt_id"`
	Kind              string   `json:"kind"`
	Sender            string   `json:"sender"`
	Recipients        []string `json:"recipients"`
	Thread            string   `json:"thread"`
	MessageID         string   `json:"message_id"`
	Path              string   `json:"path"`
	Root              string   `json:"root"`
	NamespaceID       string   `json:"namespace_id"`
	TargetIdentity    string   `json:"target_identity"`
	AdoptedGeneration uint64   `json:"adopted_generation"`
}

func ReleaseDeliveryReceiptSHA256(receipt ReleaseDeliveryReceiptTuple) (string, error) {
	if !releaseIdentityValid(receipt.AttemptID, "release-attempt-v2-") {
		return "", fmt.Errorf("delivery receipt attempt identity is malformed")
	}
	if !releaseIdentityValid(receipt.TargetIdentity, "release-receipt-target-v1-") {
		return "", fmt.Errorf("delivery receipt target identity is malformed")
	}
	for _, field := range []struct{ name, value string }{
		{"kind", receipt.Kind},
		{"sender", receipt.Sender},
		{"thread", receipt.Thread},
		{"message_id", receipt.MessageID},
		{"namespace_id", receipt.NamespaceID},
		{"target_identity", receipt.TargetIdentity},
	} {
		if err := ValidateCanonicalSingleLineField("delivery receipt "+field.name, field.value, true); err != nil {
			return "", err
		}
	}
	if err := ValidateCanonicalGateThread(receipt.Thread); err != nil {
		return "", fmt.Errorf("delivery receipt thread: %w", err)
	}
	if len(receipt.Recipients) == 0 || len(receipt.Recipients) > 16 {
		return "", fmt.Errorf("delivery receipt recipients must be bounded and non-empty")
	}
	seenRecipients := map[string]bool{}
	for _, recipient := range receipt.Recipients {
		if err := ValidateCanonicalSingleLineField("delivery receipt recipient", recipient, true); err != nil {
			return "", err
		}
		if seenRecipients[recipient] {
			return "", fmt.Errorf("delivery receipt recipients must not repeat")
		}
		seenRecipients[recipient] = true
	}
	if err := validateCanonicalAbsolutePath("delivery receipt path", receipt.Path); err != nil {
		return "", err
	}
	if err := validateCanonicalAbsolutePath("delivery receipt root", receipt.Root); err != nil {
		return "", err
	}
	if receipt.AdoptedGeneration == 0 {
		return "", fmt.Errorf("delivery receipt adopted_generation must be positive")
	}
	canonical := struct {
		Domain            string   `json:"domain"`
		SchemaVersion     int      `json:"schema_version"`
		AttemptID         string   `json:"attempt_id"`
		Kind              string   `json:"kind"`
		Sender            string   `json:"sender"`
		Recipients        []string `json:"recipients"`
		Thread            string   `json:"thread"`
		MessageID         string   `json:"message_id"`
		Path              string   `json:"path"`
		Root              string   `json:"root"`
		NamespaceID       string   `json:"namespace_id"`
		TargetIdentity    string   `json:"target_identity"`
		AdoptedGeneration uint64   `json:"adopted_generation"`
	}{
		Domain: "amq-squad.compound-release.delivery-receipt", SchemaVersion: 1,
		AttemptID: receipt.AttemptID, Kind: receipt.Kind, Sender: receipt.Sender,
		Recipients: append([]string(nil), receipt.Recipients...), Thread: receipt.Thread,
		MessageID: receipt.MessageID, Path: receipt.Path, Root: receipt.Root,
		NamespaceID: receipt.NamespaceID, TargetIdentity: receipt.TargetIdentity,
		AdoptedGeneration: receipt.AdoptedGeneration,
	}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return releaseDigest(b), nil
}

type ActiveReleaseChild struct {
	Role              string                      `json:"role"`
	Ordinal           int                         `json:"ordinal"`
	QuestionMessageID string                      `json:"question_message_id"`
	Receipt           ReleaseDeliveryReceiptTuple `json:"receipt"`
}

type ActiveReleaseManifest struct {
	SchemaVersion      int                  `json:"schema_version"`
	TaxonomyVersion    int                  `json:"taxonomy_version"`
	State              string               `json:"state"`
	ReleaseID          string               `json:"release_id"`
	Generation         uint64               `json:"generation"`
	GenerationID       string               `json:"generation_id"`
	PreparedManifestID string               `json:"prepared_manifest_id"`
	PreparedSHA256     string               `json:"prepared_sha256"`
	ActiveManifestID   string               `json:"active_manifest_id"`
	Children           []ActiveReleaseChild `json:"children"`
}

func DecodeReleaseSpec(raw any) (ReleaseSpec, error) {
	var spec ReleaseSpec
	if err := strictReleaseDecodeRaw(raw, &spec); err != nil {
		return ReleaseSpec{}, fmt.Errorf("release spec: %w", err)
	}
	if err := ValidateReleaseSpec(spec); err != nil {
		return ReleaseSpec{}, fmt.Errorf("release spec: %w", err)
	}
	return spec, nil
}

func DecodeReleaseChild(raw any) (ReleaseChildContext, error) {
	var child ReleaseChildContext
	if err := strictReleaseDecodeRaw(raw, &child); err != nil {
		return ReleaseChildContext{}, fmt.Errorf("release_child context: %w", err)
	}
	if err := ValidateReleaseChild(child); err != nil {
		return ReleaseChildContext{}, fmt.Errorf("release_child context: %w", err)
	}
	return child, nil
}

func strictReleaseDecodeRaw(raw any, dst any) error {
	var b []byte
	switch value := raw.(type) {
	case json.RawMessage:
		b = value
	case []byte:
		b = value
	case string:
		b = []byte(value)
	default:
		var err error
		b, err = json.Marshal(raw)
		if err != nil {
			return err
		}
	}
	return DecodeStrictJSON(b, dst)
}

func ValidateReleaseSpec(spec ReleaseSpec) error {
	if spec.SchemaVersion != ReleaseSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", spec.SchemaVersion)
	}
	if spec.TaxonomyVersion != ActionTaxonomyVersion {
		return fmt.Errorf("unsupported taxonomy_version %d", spec.TaxonomyVersion)
	}
	if err := ValidateCanonicalGateThread(spec.ParentGate); err != nil {
		return fmt.Errorf("parent_gate: %w", err)
	}
	for name, value := range map[string]string{
		"requester_handle":      spec.RequesterHandle,
		"operator_handle":       spec.OperatorHandle,
		"tag_target":            spec.TagTarget,
		"github_release_target": spec.GitHubReleaseTarget,
	} {
		if err := ValidateCanonicalSingleLineField(name, value, true); err != nil {
			return err
		}
	}
	if spec.RequesterHandle == spec.OperatorHandle {
		return fmt.Errorf("requester_handle and operator_handle must differ")
	}
	if err := ValidateCanonicalSingleLineField("note.summary", spec.Note.Summary, false); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"namespace.project_dir":  spec.Namespace.ProjectDir,
		"namespace.profile":      spec.Namespace.Profile,
		"namespace.session":      spec.Namespace.Session,
		"namespace.namespace_id": spec.Namespace.NamespaceID,
		"namespace.generation":   spec.Namespace.Generation,
	} {
		if err := ValidateCanonicalSingleLineField(name, value, true); err != nil {
			return err
		}
	}
	return nil
}

func ValidateReleaseChild(child ReleaseChildContext) error {
	if child.SchemaVersion != ReleaseChildSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", child.SchemaVersion)
	}
	if child.TaxonomyVersion != ActionTaxonomyVersion {
		return fmt.Errorf("unsupported taxonomy_version %d", child.TaxonomyVersion)
	}
	if !releaseIdentityValid(child.ReleaseID, "release-v1-") || !releaseIdentityValid(child.GenerationID, "release-generation-v1-") || !releaseIdentityValid(child.PreparedManifestID, "release-prepared-v1-") {
		return fmt.Errorf("release or generation identity is malformed")
	}
	if !releaseDigestValid(child.SpecSHA256) || !releaseDigestValid(child.RenderedSHA256) {
		return fmt.Errorf("release_child digest is malformed")
	}
	if !releaseIdentityValid(child.AttemptID, "release-attempt-v2-") {
		return fmt.Errorf("release_child attempt identity is malformed")
	}
	if err := ValidateCanonicalGateThread(child.ParentGate); err != nil {
		return err
	}
	wantOrdinal, wantKind, wantAction, ok := releaseRoleTuple(child.Role)
	if !ok || child.Ordinal != wantOrdinal || child.Generation == 0 || child.ChildCount != 2 {
		return fmt.Errorf("release_child role/ordinal mismatch")
	}
	if child.GateKind != wantKind || child.Action != wantAction {
		return fmt.Errorf("release_child catalog tuple mismatch")
	}
	if _, err := ValidateGateAction(child.GateKind, child.Action); err != nil {
		return err
	}
	if err := ValidateCanonicalGateThread(child.Thread); err != nil {
		return err
	}
	wantThread := releaseChildThread(child.ParentGate, child.SpecSHA256, child.Generation, child.Ordinal, child.Role)
	if child.Thread != wantThread {
		return fmt.Errorf("release_child thread does not match parent, generation, and role")
	}
	if err := ValidateCanonicalSingleLineField("release_child target", child.Target, true); err != nil {
		return err
	}
	wantAttempt := releaseAttemptIdentity(child.ReleaseID, child.SpecSHA256, child.GenerationID, child.Generation, child.Role, child.Ordinal, child.Thread, releaseReceiptTargetIdentity(child.Thread, child.GateKind, child.Action, child.Target))
	if child.AttemptID != wantAttempt {
		return fmt.Errorf("release_child attempt identity does not match exact marker fields")
	}
	return nil
}

func derivePreparedReleaseCore(spec ReleaseSpec, generation uint64) (PreparedReleaseManifest, error) {
	if err := ValidateReleaseSpec(spec); err != nil {
		return PreparedReleaseManifest{}, err
	}
	if generation == 0 {
		return PreparedReleaseManifest{}, fmt.Errorf("release generation must be positive")
	}
	specBytes, _ := json.Marshal(spec)
	specSHA := releaseDigest(specBytes)
	releaseID := releaseIdentity("release-v1-", string(specBytes))
	generationID := releaseIdentity("release-generation-v1-", releaseID, strconv.FormatUint(generation, 10), spec.Namespace.Generation)

	roles := []string{ReleaseChildTag, ReleaseChildGitHubRelease}
	children := make([]ReleaseChildPlan, 0, len(roles))
	for _, role := range roles {
		ordinal, gateKind, action, _ := releaseRoleTuple(role)
		target := spec.TagTarget
		if role == ReleaseChildGitHubRelease {
			target = spec.GitHubReleaseTarget
		}
		thread := releaseChildThread(spec.ParentGate, specSHA, generation, ordinal, role)
		request := GateRequestContext{
			SchemaVersion: GateRequestSchemaVersion, TaxonomyVersion: ActionTaxonomyVersion,
			Gate: thread, Thread: thread, Namespace: spec.Namespace, GateKind: gateKind,
			Action: action, Target: target, Note: spec.Note.Summary,
		}
		if err := ValidateGateRequest(request); err != nil {
			return PreparedReleaseManifest{}, err
		}
		subject := "APPROVAL: " + strings.TrimPrefix(thread, "gate/")
		body := fmt.Sprintf("Gate-Kind: %s\nAction: %s\nTarget: %s", gateKind, action, target)
		if spec.Note.Summary != "" {
			body += "\nNote: " + spec.Note.Summary
		}
		renderedSHA := releaseDigest([]byte(subject + "\x00" + body))
		targetIdentity := releaseReceiptTargetIdentity(thread, gateKind, action, target)
		attemptID := releaseAttemptIdentity(releaseID, specSHA, generationID, generation, role, ordinal, thread, targetIdentity)
		marker := ReleaseChildContext{
			SchemaVersion: ReleaseChildSchemaVersion, TaxonomyVersion: ActionTaxonomyVersion,
			ReleaseID: releaseID, GenerationID: generationID, Generation: generation, ChildCount: 2, SpecSHA256: specSHA,
			ParentGate: spec.ParentGate, Role: role, Ordinal: ordinal, Thread: thread,
			GateKind: gateKind, Action: action, Target: target, RenderedSHA256: renderedSHA, AttemptID: attemptID,
		}
		children = append(children, ReleaseChildPlan{
			Role: role, Ordinal: ordinal, Thread: thread, GateKind: gateKind, Action: action,
			Target: target, Subject: subject, Body: body, RenderedSHA256: renderedSHA,
			AuthorizationRequest: request, ReleaseChild: marker,
			Receipt: ReleaseReceiptIntent{AttemptID: attemptID, Kind: "operator_gate_release_" + role, Sender: spec.RequesterHandle, Recipient: spec.OperatorHandle, Thread: thread, NamespaceID: spec.Namespace.NamespaceID, TargetIdentity: targetIdentity, MinimumGeneration: 1},
		})
	}
	manifest := PreparedReleaseManifest{
		SchemaVersion: ReleaseManifestVersion, TaxonomyVersion: ActionTaxonomyVersion,
		State: ReleaseStatePlanned, ReleaseID: releaseID, Generation: generation,
		GenerationID: generationID, SpecSHA256: specSHA, Spec: spec, Children: children,
	}
	manifest.PreparedManifestID = preparedReleaseIdentity(manifest)
	for i := range manifest.Children {
		manifest.Children[i].ReleaseChild.PreparedManifestID = manifest.PreparedManifestID
	}
	return manifest, nil
}

func DerivePreparedRelease(spec ReleaseSpec, generation uint64) (PreparedReleaseManifest, error) {
	manifest, err := derivePreparedReleaseCore(spec, generation)
	if err != nil {
		return PreparedReleaseManifest{}, err
	}
	return manifest, ValidatePreparedRelease(manifest)
}

func ValidatePreparedRelease(manifest PreparedReleaseManifest) error {
	if manifest.SchemaVersion != ReleaseManifestVersion || manifest.TaxonomyVersion != ActionTaxonomyVersion {
		return fmt.Errorf("unsupported prepared release schema or taxonomy")
	}
	if manifest.State != ReleaseStatePlanned || manifest.Generation == 0 {
		return fmt.Errorf("prepared release must be planned with a positive generation")
	}
	if err := ValidateReleaseSpec(manifest.Spec); err != nil {
		return err
	}
	for i, child := range manifest.Children {
		if child.ReleaseChild.AttemptID != child.Receipt.AttemptID {
			return fmt.Errorf("prepared release child %d marker and receipt attempts diverge", i)
		}
	}
	want, err := derivePreparedReleaseCore(manifest.Spec, manifest.Generation)
	if err != nil {
		return err
	}
	gotBytes, _ := json.Marshal(manifest)
	wantBytes, _ := json.Marshal(want)
	if !bytes.Equal(gotBytes, wantBytes) {
		return fmt.Errorf("prepared release does not exactly match deterministic derivation")
	}
	return nil
}

func releaseAttemptIdentity(releaseID, specSHA, generationID string, generation uint64, role string, ordinal int, thread, targetIdentity string) string {
	return releaseIdentity("release-attempt-v2-", releaseID, specSHA, generationID, strconv.FormatUint(generation, 10), role, strconv.Itoa(ordinal), thread, targetIdentity)
}

func preparedReleaseIdentity(manifest PreparedReleaseManifest) string {
	copy := manifest
	copy.PreparedManifestID = ""
	for i := range copy.Children {
		copy.Children[i].ReleaseChild.PreparedManifestID = ""
	}
	b, _ := json.Marshal(copy)
	return releaseIdentity("release-prepared-v1-", string(b))
}

func PreparedReleaseSHA256(manifest PreparedReleaseManifest) string {
	b, _ := json.Marshal(manifest)
	return releaseDigest(b)
}

func NewActiveRelease(prepared PreparedReleaseManifest, observed map[string]ReleaseDeliveryReceiptTuple) (ActiveReleaseManifest, error) {
	if err := ValidatePreparedRelease(prepared); err != nil {
		return ActiveReleaseManifest{}, err
	}
	if len(observed) != 2 {
		return ActiveReleaseManifest{}, fmt.Errorf("active release requires exactly two observed receipt roles")
	}
	children := make([]ActiveReleaseChild, 0, 2)
	for _, child := range prepared.Children {
		receipt, ok := observed[child.Role]
		if !ok {
			return ActiveReleaseManifest{}, fmt.Errorf("active release omitted observed %s receipt", child.Role)
		}
		receipt.Recipients = append([]string(nil), receipt.Recipients...)
		children = append(children, ActiveReleaseChild{Role: child.Role, Ordinal: child.Ordinal, QuestionMessageID: receipt.MessageID, Receipt: receipt})
	}
	active := ActiveReleaseManifest{
		SchemaVersion: ReleaseManifestVersion, TaxonomyVersion: ActionTaxonomyVersion,
		State: ReleaseStateActive, ReleaseID: prepared.ReleaseID, Generation: prepared.Generation,
		GenerationID: prepared.GenerationID, PreparedManifestID: prepared.PreparedManifestID,
		PreparedSHA256: PreparedReleaseSHA256(prepared), Children: children,
	}
	active.ActiveManifestID = activeReleaseIdentity(active)
	return active, ValidateActiveRelease(prepared, active)
}

func ValidateActiveRelease(prepared PreparedReleaseManifest, active ActiveReleaseManifest) error {
	if err := ValidatePreparedRelease(prepared); err != nil {
		return err
	}
	if active.SchemaVersion != ReleaseManifestVersion || active.TaxonomyVersion != ActionTaxonomyVersion || active.State != ReleaseStateActive {
		return fmt.Errorf("unsupported active release schema, taxonomy, or state")
	}
	if active.ReleaseID != prepared.ReleaseID || active.Generation != prepared.Generation || active.GenerationID != prepared.GenerationID || active.PreparedManifestID != prepared.PreparedManifestID || active.PreparedSHA256 != PreparedReleaseSHA256(prepared) {
		return fmt.Errorf("active release does not bind the exact prepared release")
	}
	if len(active.Children) != len(prepared.Children) {
		return fmt.Errorf("active release must adopt exactly two children")
	}
	seenMessages := map[string]bool{}
	seenAttempts := map[string]bool{}
	seenPaths := map[string]bool{}
	for i := range prepared.Children {
		p, a := prepared.Children[i], active.Children[i]
		if a.Role != p.Role || a.Ordinal != p.Ordinal || a.Receipt.AttemptID != p.Receipt.AttemptID || a.Receipt.Kind != p.Receipt.Kind || a.Receipt.Sender != p.Receipt.Sender || len(a.Receipt.Recipients) != 1 || a.Receipt.Recipients[0] != p.Receipt.Recipient || a.Receipt.Thread != p.Receipt.Thread || a.Receipt.NamespaceID != p.Receipt.NamespaceID || a.Receipt.TargetIdentity != p.Receipt.TargetIdentity || a.Receipt.AdoptedGeneration < p.Receipt.MinimumGeneration || a.QuestionMessageID == "" || a.Receipt.MessageID != a.QuestionMessageID {
			return fmt.Errorf("active release child %d does not adopt its exact question and receipt tuple", i)
		}
		if err := ValidateCanonicalSingleLineField("question_message_id", a.QuestionMessageID, true); err != nil {
			return err
		}
		if err := validateCanonicalAbsolutePath("receipt path", a.Receipt.Path); err != nil {
			return err
		}
		if err := validateCanonicalAbsolutePath("receipt root", a.Receipt.Root); err != nil {
			return err
		}
		if seenMessages[a.QuestionMessageID] || seenAttempts[a.Receipt.AttemptID] || seenPaths[a.Receipt.Path] {
			return fmt.Errorf("active release children must have distinct question, attempt, and receipt path identities")
		}
		seenMessages[a.QuestionMessageID], seenAttempts[a.Receipt.AttemptID] = true, true
		seenPaths[a.Receipt.Path] = true
	}
	if active.ActiveManifestID != activeReleaseIdentity(active) {
		return fmt.Errorf("active manifest identity mismatch")
	}
	return nil
}

func activeReleaseIdentity(active ActiveReleaseManifest) string {
	copy := active
	copy.ActiveManifestID = ""
	b, _ := json.Marshal(copy)
	return releaseIdentity("release-active-v1-", string(b))
}

func ActiveReleaseSHA256(active ActiveReleaseManifest) string {
	b, _ := json.Marshal(active)
	return releaseDigest(b)
}

func releaseRoleTuple(role string) (ordinal int, gateKind, action string, ok bool) {
	switch role {
	case ReleaseChildTag:
		return 0, GateTag, "tag", true
	case ReleaseChildGitHubRelease:
		return 1, GateRelease, "github_release", true
	default:
		return 0, "", "", false
	}
}

func releaseChildThread(parent, specSHA string, generation uint64, ordinal int, role string) string {
	return fmt.Sprintf("%s/release/%s/g%020d/%02d-%s", parent, strings.TrimPrefix(specSHA, "sha256:"), generation, ordinal, strings.ReplaceAll(role, "_", "-"))
}

func releaseReceiptTargetIdentity(thread, gateKind, action, target string) string {
	return releaseIdentity("release-receipt-target-v1-", thread, gateKind, action, target)
}

func validateCanonicalAbsolutePath(name, value string) error {
	if err := ValidateCanonicalSingleLineField(name, value, true); err != nil {
		return err
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("%s must be an absolute canonical path", name)
	}
	return nil
}

func releaseIdentity(prefix string, parts ...string) string {
	h := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(part))
	}
	return prefix + hex.EncodeToString(h.Sum(nil))
}

func releaseDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func releaseIdentityValid(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	rest := strings.TrimPrefix(value, prefix)
	if len(rest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(rest)
	return err == nil
}

func releaseDigestValid(value string) bool {
	return releaseIdentityValid(value, "sha256:")
}
