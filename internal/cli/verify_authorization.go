package cli

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

type verifyAuthorizationResult struct {
	OK              bool   `json:"ok"`
	AuthorizationID string `json:"authorization_id,omitempty"`
	Action          string `json:"action,omitempty"`
	Target          string `json:"target,omitempty"`
	Gate            string `json:"gate,omitempty"`
	KeyID           string `json:"key_id,omitempty"`
}

func emitSignedVerifyAuthorization(result *verifyActionResult, selected cliReleaseSelectedContext, keyPath, outputPath string, verifiedAt time.Time) error {
	// authorizationPayloadForCurrentResult checks the result is complete, human
	// and in the admitted namespace before anything is signed
	signer, err := operatorauth.LoadEd25519Signer(keyPath)
	if err != nil {
		return err
	}
	defer signer.Destroy()
	payload, err := authorizationPayloadForCurrentResult(result, selected, authorizationIssuer(), verifiedAt)
	if err != nil {
		return err
	}
	envelope, err := operatorauth.NewAuthorizationEnvelope(payload, signer)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := strings.TrimSpace(outputPath)
	if path == "" {
		path = filepath.Join(selected.ProjectDir, ".amq-squad", "evidence", selected.Profile, selected.Session, "operator-auth", "authorizations", envelope.Payload.AuthorizationID+".json")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("authorization output path must be absolute and clean")
	}
	if err := writeImmutableAuthorization(path, raw); err != nil {
		return err
	}
	result.EnvelopeEligible, result.AuthorizationID, result.AuthorizationPath, result.AuthorizationKeyID = true, envelope.Payload.AuthorizationID, path, envelope.Signature.KeyID
	return nil
}

func authorizationPayloadForCurrentResult(result *verifyActionResult, selected cliReleaseSelectedContext, issuer operatorauth.AuthorizationIssuer, fallback time.Time) (operatorauth.AuthorizationPayload, error) {
	if result == nil || result.Decision != actionDecisionApproved || result.approval == nil || result.Namespace == nil {
		return operatorauth.AuthorizationPayload{}, fmt.Errorf("signed authorization requires a complete approved typed result")
	}
	if result.ApprovalSource != "human" || result.SelfApproved || result.AnsweredByRole != "operator" {
		return operatorauth.AuthorizationPayload{}, fmt.Errorf("signed authorization requires human operator authority")
	}
	if result.Namespace.ProjectDir != selected.ProjectDir || !squadnamespace.ProfilesEqual(result.Namespace.Profile, selected.Profile) || result.Namespace.Session != selected.Session || result.Namespace.Generation != selected.NamespaceGeneration {
		return operatorauth.AuthorizationPayload{}, fmt.Errorf("signed authorization namespace no longer matches admitted context")
	}
	receiptPath := selfApprovalReceiptPath(selected.ProjectDir, selected.Profile, selected.Session, result.Gate, result.QuestionMessageID, result.AnswerMessageID)
	receiptBytes, err := operatorauth.ReadAuthorizationEvidenceFile(receiptPath, 1<<20)
	if err != nil {
		return operatorauth.AuthorizationPayload{}, fmt.Errorf("read approval receipt for signed authorization: %w", err)
	}
	evidence := []operatorauth.AuthorizationEvidence{{Kind: "approval_receipt", Identity: receiptPath, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(receiptBytes))}}
	approval := result.approval
	if approval.PreflightPath != "" || approval.PreflightSHA256 != "" || approval.PreflightKind != "" {
		preflightBytes, readErr := operatorauth.ReadAuthorizationEvidenceFile(approval.PreflightPath, 16<<20)
		if readErr != nil {
			return operatorauth.AuthorizationPayload{}, fmt.Errorf("read preflight evidence for signed authorization: %w", readErr)
		}
		if actual := fmt.Sprintf("sha256:%x", sha256.Sum256(preflightBytes)); actual != approval.PreflightSHA256 {
			return operatorauth.AuthorizationPayload{}, fmt.Errorf("preflight evidence digest changed before authorization")
		}
		evidence = append(evidence, operatorauth.AuthorizationEvidence{Kind: "preflight", Identity: approval.PreflightPath, SHA256: approval.PreflightSHA256})
	}
	slices.SortFunc(evidence, func(a, b operatorauth.AuthorizationEvidence) int {
		return cmp.Or(strings.Compare(a.Kind, b.Kind), strings.Compare(a.Identity, b.Identity))
	})
	stableVerifiedAt := approval.VerifiedAt
	if _, parseErr := time.Parse(time.RFC3339Nano, stableVerifiedAt); parseErr != nil {
		stableVerifiedAt = fallback.UTC().Format(time.RFC3339Nano)
	}
	evidenceDigest, err := operatorauth.AuthorizationEvidenceDigest(evidence)
	if err != nil {
		return operatorauth.AuthorizationPayload{}, err
	}
	return operatorauth.AuthorizationPayload{
		SchemaVersion: operatorauth.AuthorizationEnvelopeSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Decision: actionDecisionApproved,
		Namespace: *result.Namespace, Gate: result.Gate, Thread: result.Gate, GateKind: result.GateKind,
		Action: result.Action, Target: result.Target, Note: result.Note,
		QuestionMessageID: result.QuestionMessageID, AnswerMessageID: result.AnswerMessageID,
		QuestionCreatedAt: result.QuestionCreatedAt, AnswerCreatedAt: result.AnswerCreatedAt,
		Actor:  operatorauth.AuthorizationActor{Role: result.AnsweredByRole, Handle: result.AnsweredBy, Source: result.ApprovalSource, SelfApproved: result.SelfApproved},
		Issuer: issuer, Evidence: evidence, EvidenceSHA256: evidenceDigest, VerifiedAt: stableVerifiedAt,
		Policy:    operatorauth.AuthorizationPolicyEvidence{Revision: approval.PolicyRevision, SHA256: approval.PolicyHash},
		Preflight: operatorauth.AuthorizationPreflightEvidence{Kind: approval.PreflightKind, Path: approval.PreflightPath, SHA256: approval.PreflightSHA256},
	}, nil
}

func authorizationIssuer() operatorauth.AuthorizationIssuer {
	issuer := operatorauth.AuthorizationIssuer{Binary: "amq-squad", Version: "dev"}
	if executable, err := os.Executable(); err == nil && filepath.Base(executable) != "" {
		issuer.Binary = filepath.Base(executable)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			issuer.Version = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				issuer.BuildCommit = setting.Value
			}
		}
	}
	return issuer
}

var (
	authorizationArtifactOpenFile = os.OpenFile
	authorizationArtifactWrite    = func(f *os.File, data []byte) (int, error) { return f.Write(data) }
	authorizationArtifactSync     = func(f *os.File) error { return f.Sync() }
	authorizationArtifactClose    = func(f *os.File) error { return f.Close() }
	authorizationArtifactRemove   = os.Remove
	verifyAuthorizationFinalCheck = func() {}
)

func writeImmutableAuthorization(path string, raw []byte) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create authorization artifact directory: %w", err)
	}
	f, err := authorizationArtifactOpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		existing, readErr := operatorauth.ReadAuthorizationEvidenceFile(path, 4<<20)
		if readErr != nil || !bytes.Equal(existing, raw) {
			return fmt.Errorf("immutable authorization artifact already exists with different content")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("create authorization artifact: %w", err)
	}
	for written := 0; written < len(raw) && err == nil; {
		var n int
		n, err = authorizationArtifactWrite(f, raw[written:])
		if n < 0 || n > len(raw)-written {
			err = fmt.Errorf("invalid write count")
		} else if err == nil && n == 0 {
			err = io.ErrShortWrite
		}
		written += max(n, 0)
	}
	if err != nil {
		err = fmt.Errorf("write authorization artifact: %w", err)
	} else if err = authorizationArtifactSync(f); err != nil {
		err = fmt.Errorf("sync authorization artifact: %w", err)
	}
	if closeErr := authorizationArtifactClose(f); err == nil && closeErr != nil {
		err = fmt.Errorf("close authorization artifact: %w", closeErr)
	}
	if err != nil {
		_ = authorizationArtifactRemove(path)
	}
	return err
}

func runVerifyAuthorization(args []string) error {
	if slices.ContainsFunc(args, func(a string) bool { return a == "--gate" || a == "-gate" || strings.HasPrefix(a, "--gate=") }) {
		return runVerifyStoreAuthorization(args)
	}
	fs := flag.NewFlagSet("verify authorization", flag.ContinueOnError)
	fileFlag := fs.String("file", "", "absolute signed authorization envelope path")
	actionFlag := fs.String("action", "", "exact canonical action expected by the caller")
	targetFlag := fs.String("target", "", "exact target expected by the caller")
	trustStoreFlag := fs.String("trust-store", "", "absolute operator-provisioned public trust store")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned verification result")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad verify authorization - verify a signed action envelope and current durable authority

Usage:
  amq-squad verify authorization --file FILE --action KIND --target TARGET --trust-store FILE [--json]
  amq-squad verify authorization --gate ID ...   (typed gates: see 'verify authorization --gate --help')

--file (merge kinds): the signature against --trust-store, then the current
namespace, gate, answer receipt and policy. It never performs the action.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("verify authorization takes no positional arguments")
	}
	for name, value := range map[string]string{"file": *fileFlag, "action": *actionFlag, "target": *targetFlag, "trust-store": *trustStoreFlag} {
		if strings.TrimSpace(value) == "" {
			return usageErrorf("verify authorization requires --%s", name)
		}
	}
	envelope, err := operatorauth.LoadAuthorizationEnvelope(*fileFlag)
	if err != nil {
		return err
	}
	verifySignature := func() error {
		store, err := operatorauth.LoadAuthorizationTrustStore(*trustStoreFlag)
		if err != nil {
			return err
		}
		return operatorauth.VerifyAuthorizationEnvelope(envelope, store)
	}
	if err := verifySignature(); err != nil {
		return err
	}
	canonical, err := operatorauth.CanonicalAction(*actionFlag)
	if err != nil || canonical != envelope.Payload.Action || *targetFlag != envelope.Payload.Target {
		return usageErrorf("signed authorization does not exactly bind caller action/target")
	}
	if typedGateAction(canonical) {
		return usageErrorf("a typed gate's authorization is the gate store's answers/<id>.json; verify it with --gate ID")
	}
	p := envelope.Payload
	resolve := func() (contextResolution, error) {
		return resolveCanonicalContext(contextResolveOptions{ProjectFlag: p.Namespace.ProjectDir, ProfileFlag: p.Namespace.Profile, SessionFlag: p.Namespace.Session, ProjectExplicit: true, ProfileExplicit: true, SessionExplicit: true})
	}
	ctx, err := resolve()
	if err != nil {
		return err
	}
	ctx, admission, err := acquireRevalidatedContextWriter(ctx, resolve)
	if err != nil {
		return err
	}
	defer admission.close()
	if ctx.NamespaceGeneration != p.Namespace.Generation || ctx.ProjectDir != p.Namespace.ProjectDir || !squadnamespace.ProfilesEqual(ctx.Profile, p.Namespace.Profile) || ctx.Session != p.Namespace.Session {
		return fmt.Errorf("signed authorization namespace is stale")
	}
	var live verifyActionResult
	if liveErr := executeVerifyActionInSelectedContext(verifyActionExecution{ProjectDir: ctx.ProjectDir, Profile: ctx.Profile, Session: ctx.Session, Gate: p.Gate, Action: p.Action, Target: p.Target, Out: io.Discard, JSON: true, Capture: &live}, selectedReleaseContext(ctx)); liveErr != nil {
		return fmt.Errorf("signed authorization live revalidation failed: %w", liveErr)
	}
	current, err := authorizationPayloadForCurrentResult(&live, selectedReleaseContext(ctx), p.Issuer, time.Now())
	if err != nil {
		return fmt.Errorf("rebuild current signed authority: %w", err)
	}
	current.AuthorizationID = p.AuthorizationID
	if !reflect.DeepEqual(current, p) {
		return fmt.Errorf("signed authorization current authority tuple changed: %s", strings.Join(authorizationPayloadChangedSections(p, current), ","))
	}
	verifyAuthorizationFinalCheck()
	if err := verifySignature(); err != nil {
		return err
	}
	result := verifyAuthorizationResult{OK: true, AuthorizationID: p.AuthorizationID, Action: p.Action, Target: p.Target, Gate: p.Gate, KeyID: envelope.Signature.KeyID}
	if *jsonOut {
		return printJSONEnvelope("verify_authorization", result)
	}
	fmt.Printf("signed authorization verified: %s\naction: %s\ntarget: %s\ngate: %s\nkey_id: %s\n", result.AuthorizationID, result.Action, result.Target, result.Gate, result.KeyID)
	return nil
}

func authorizationPayloadChangedSections(signed, current operatorauth.AuthorizationPayload) []string {
	var changed []string
	for _, c := range []struct {
		name string
		a, b any
	}{
		{"namespace", signed.Namespace, current.Namespace}, {"binding", [6]string{signed.Gate, signed.Thread, signed.GateKind, signed.Action, signed.Target, signed.Note}, [6]string{current.Gate, current.Thread, current.GateKind, current.Action, current.Target, current.Note}},
		{"message_timeline", [5]string{signed.QuestionMessageID, signed.AnswerMessageID, signed.QuestionCreatedAt, signed.AnswerCreatedAt, signed.VerifiedAt}, [5]string{current.QuestionMessageID, current.AnswerMessageID, current.QuestionCreatedAt, current.AnswerCreatedAt, current.VerifiedAt}}, {"actor", signed.Actor, current.Actor},
		{"policy", signed.Policy, current.Policy}, {"preflight", signed.Preflight, current.Preflight},
		{"evidence", []any{signed.Evidence, signed.EvidenceSHA256}, []any{current.Evidence, current.EvidenceSHA256}},
	} {
		if !reflect.DeepEqual(c.a, c.b) {
			changed = append(changed, c.name)
		}
	}
	if len(changed) == 0 {
		changed = append(changed, "envelope_metadata")
	}
	return changed
}
