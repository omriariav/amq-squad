package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var selfOperatorNow = time.Now
var errSelfApprovalRetrySafe = errors.New("expired self approval send attempt has no durable answer")

type selfApprovalReservation struct {
	Token             string    `json:"token"`
	QuestionMessageID string    `json:"question_message_id"`
	PolicyRevision    int64     `json:"policy_revision"`
	PolicyHash        string    `json:"policy_hash"`
	HumanCursor       string    `json:"human_cursor"`
	ExpiresAt         time.Time `json:"expires_at"`
	Sending           bool      `json:"sending,omitempty"`
	AnswerMessageID   string    `json:"answer_message_id,omitempty"`
}

func runOperatorSelfApprove(args []string) error {
	fs := flag.NewFlagSet("operator self-approve", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory")
	profileFlag := fs.String("profile", "", "team profile")
	sessionFlag := fs.String("session", "", "exact delegated session")
	gateFlag := fs.String("gate", "", "gate topic")
	kindFlag := fs.String("kind", "", "gate kind")
	actionFlag := fs.String("action", "", "normalized action")
	targetFlag := fs.String("target", "", "exact case-sensitive target")
	evidenceFlag := fs.String("evidence", "", "strict preflight evidence JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	session, gate := strings.TrimSpace(*sessionFlag), normalizeGateTopic(*gateFlag)
	if err := team.ValidateSessionName(session); err != nil || gate == "" {
		return usageErrorf("self-approve requires valid --session and --gate")
	}
	cfg, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return err
	}
	view := team.EffectiveSelfOperator(cfg, session)
	if !view.Enabled {
		return usageErrorf("self_operator is not enabled for exact profile/session")
	}
	if err := operatorauth.Evaluate(*kindFlag, *actionFlag, view.AllowedGateKinds); err != nil {
		return usageErrorf("self approval blocked: %v", err)
	}
	if strings.TrimSpace(*kindFlag) != operatorauth.GateMerge {
		return usageErrorf("self approval preflight for %q is not implemented; human approval required", *kindFlag)
	}
	actor, err := resolveVerifiedOperatorActor(projectDir, profile, session, view.LeadRole, view.LeadHandle)
	if err != nil {
		return usageErrorf("self approval actor identity: %v", err)
	}
	question, humanCursor, err := selfApprovalGateSnapshot(projectDir, profile, session, gate, *kindFlag, *actionFlag, *targetFlag, team.EffectiveOperator(cfg).Handle)
	if err != nil {
		return err
	}
	reservationPath := selfApprovalReservationPath(projectDir, profile, session, gate)
	if existing, ok := pendingSelfApprovalReservation(reservationPath); ok {
		if err := reconcileSentSelfApproval(projectDir, profile, session, gate, *kindFlag, *actionFlag, *targetFlag, question, existing, reservationPath); !errors.Is(err, errSelfApprovalRetrySafe) {
			return err
		}
	}
	token, err := newReservationToken()
	if err != nil {
		return err
	}
	reservation := selfApprovalReservation{Token: token, QuestionMessageID: question.ID, PolicyRevision: view.PolicyRevision, PolicyHash: view.PolicyHash, HumanCursor: humanCursor, ExpiresAt: selfOperatorNow().UTC().Add(5 * time.Minute)}
	if err := reserveSelfApproval(reservationPath, reservation); err != nil {
		return err
	}
	sent := false
	sendAttempted := false
	defer func() {
		if !sent && !sendAttempted {
			_ = os.Remove(reservationPath)
		}
	}()

	evidenceBytes, evidenceDigest, evidence, err := validateSelfMergeEvidence(*evidenceFlag, *targetFlag)
	if err != nil {
		return err
	}
	evidencePath := selfApprovalEvidencePath(projectDir, profile, session, gate, question.ID, evidenceDigest)
	if err := writeImmutableEvidence(evidencePath, evidenceBytes); err != nil {
		return err
	}

	current, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return err
	}
	currentView := team.EffectiveSelfOperator(current, session)
	if !currentView.Enabled || currentView.PolicyRevision != reservation.PolicyRevision || currentView.PolicyHash != reservation.PolicyHash {
		return usageErrorf("self policy changed during preflight")
	}
	latestQuestion, latestHumanCursor, err := selfApprovalGateSnapshot(projectDir, profile, session, gate, *kindFlag, *actionFlag, *targetFlag, team.EffectiveOperator(current).Handle)
	if err != nil {
		return err
	}
	if latestQuestion.ID != reservation.QuestionMessageID || latestHumanCursor != reservation.HumanCursor {
		return usageErrorf("gate question or human intervention changed during preflight")
	}
	if err := validateSelfApprovalReservation(reservationPath, reservation); err != nil {
		return err
	}
	if err := markSelfApprovalSending(reservationPath, reservation.Token); err != nil {
		return err
	}
	now := selfOperatorNow().UTC()
	approval := operatorauth.ApprovalContext{
		SchemaVersion: operatorauth.ApprovalSchemaVersion, Source: "self_operator", SelfApproved: true,
		GateKind: *kindFlag, Action: operatorauth.NormalizeAction(*actionFlag), Target: strings.TrimSpace(*targetFlag), QuestionMessageID: question.ID,
		AnsweredByRole: actor.Role, AnsweredByHandle: actor.Handle, PolicyRevision: currentView.PolicyRevision, PolicyHash: currentView.PolicyHash,
		PreflightKind: "verify_merge", PreflightSHA256: evidenceDigest, PreflightPath: evidencePath, VerifiedAt: now.Format(time.RFC3339Nano),
	}
	body := fmt.Sprintf("Gate-Kind: %s\nAction: %s\nTarget: %s\nEvidence: %s at %s", approval.GateKind, approval.Action, approval.Target, evidence.Subject, evidence.HeadSHA)
	sendAttempted = true
	err = sendOperatorAMQ(operatorSendOptions{
		Command: "operator self-approve", Project: projectDir, Profile: profile, Session: session,
		From: actor.Handle, To: question.From, Thread: gate, Kind: string(state.KindAnswer), Subject: "APPROVED: " + strings.TrimPrefix(gate, "gate/"), Body: body,
		Context: map[string]any{"approval": approval}, OnSent: func(answerID string) error {
			if err := markSelfApprovalSent(reservationPath, reservation.Token, answerID); err != nil {
				return err
			}
			receipt := operatorauth.Receipt{Gate: gate, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Decision: "approved", ApprovalSource: approval.Source, SelfApproved: true, QuestionMessageID: question.ID, AnswerMessageID: answerID, AnsweredBy: actor.Handle, PolicyRevision: approval.PolicyRevision, PolicyHash: approval.PolicyHash, Preflight: operatorauth.PreflightReceipt{Kind: approval.PreflightKind, SHA256: approval.PreflightSHA256, Path: approval.PreflightPath, OK: true}}
			return writeSelfApprovalReceipt(projectDir, profile, session, gate, answerID, receipt)
		},
	})
	if err != nil {
		return err
	}
	sent = true
	_ = os.Remove(reservationPath)
	return nil
}

func pendingSelfApprovalReservation(path string) (selfApprovalReservation, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return selfApprovalReservation{}, false
	}
	var reservation selfApprovalReservation
	if json.Unmarshal(b, &reservation) != nil || (!reservation.Sending && reservation.AnswerMessageID == "") {
		return selfApprovalReservation{}, false
	}
	return reservation, true
}

func reconcileSentSelfApproval(projectDir, profile, session, gate, kind, action, target string, question state.Message, reservation selfApprovalReservation, reservationPath string) error {
	if reservation.QuestionMessageID != question.ID {
		return usageErrorf("sent self approval reservation belongs to a different question; human reconciliation required")
	}
	_, msgs, err := latestStrictGateQuestion(projectDir, profile, session, gate, kind, action, target)
	if err != nil {
		return err
	}
	var conflict bool
	msgs, conflict = dedupeSecurityMessages(msgs)
	if conflict {
		return usageErrorf("sent self approval cannot be reconciled; conflicting mailbox copies")
	}
	cfg, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return err
	}
	view := team.EffectiveSelfOperator(cfg, session)
	if !view.Enabled || view.PolicyRevision != reservation.PolicyRevision || view.PolicyHash != reservation.PolicyHash {
		return usageErrorf("sent self approval policy changed; human reconciliation required")
	}
	var candidates []state.Message
	for i := range msgs {
		msg := msgs[i]
		if reservation.AnswerMessageID != "" && msg.ID != reservation.AnswerMessageID {
			continue
		}
		if msg.Thread != gate || msg.Kind != state.KindAnswer || !msg.ApprovalValid || msg.Approval == nil {
			continue
		}
		a := *msg.Approval
		if a.Source == "self_operator" && a.SelfApproved && a.QuestionMessageID == question.ID && a.PolicyRevision == reservation.PolicyRevision && a.PolicyHash == reservation.PolicyHash && a.GateKind == kind && operatorauth.NormalizeAction(a.Action) == operatorauth.NormalizeAction(action) && a.Target == target && a.AnsweredByRole == view.LeadRole && a.AnsweredByHandle == view.LeadHandle {
			candidates = append(candidates, msg)
		}
	}
	if len(candidates) != 1 {
		if len(candidates) == 0 && reservation.AnswerMessageID == "" && reservation.Sending && !reservation.ExpiresAt.After(selfOperatorNow()) {
			if err := clearExpiredSelfApprovalReservation(reservationPath, reservation.Token); err != nil {
				return err
			}
			return errSelfApprovalRetrySafe
		}
		return usageErrorf("sent self approval cannot be reconciled; expected exactly one matching typed answer, found %d", len(candidates))
	}
	answer := &candidates[0]
	a := *answer.Approval
	if a.Source != "self_operator" || !a.SelfApproved || a.QuestionMessageID != question.ID || a.PolicyRevision != reservation.PolicyRevision || a.PolicyHash != reservation.PolicyHash || a.GateKind != kind || operatorauth.NormalizeAction(a.Action) != operatorauth.NormalizeAction(action) || a.Target != target {
		return usageErrorf("sent self approval typed context does not match reservation")
	}
	if err := revalidateSelfApprovalEvidence(projectDir, a, target); err != nil {
		return err
	}
	receipt := operatorauth.Receipt{Gate: gate, GateKind: a.GateKind, Action: a.Action, Target: a.Target, Decision: "approved", ApprovalSource: a.Source, SelfApproved: true, QuestionMessageID: a.QuestionMessageID, AnswerMessageID: answer.ID, AnsweredBy: a.AnsweredByHandle, PolicyRevision: a.PolicyRevision, PolicyHash: a.PolicyHash, Preflight: operatorauth.PreflightReceipt{Kind: a.PreflightKind, SHA256: a.PreflightSHA256, Path: a.PreflightPath, OK: true}}
	if err := writeSelfApprovalReceipt(projectDir, profile, session, gate, answer.ID, receipt); err != nil {
		return err
	}
	return os.Remove(reservationPath)
}

func selfApprovalGateSnapshot(projectDir, profile, session, gate, kind, action, target, humanHandle string) (state.Message, string, error) {
	latest, msgs, err := latestStrictGateQuestion(projectDir, profile, session, gate, kind, action, target)
	if err != nil {
		return state.Message{}, "", err
	}
	humanCursor := ""
	for _, msg := range msgs {
		if msg.Thread == gate && msg.From == humanHandle && messageAfter(msg, latest) {
			if humanCursor == "" || msg.ID > humanCursor {
				humanCursor = msg.ID
			}
		}
	}
	if humanCursor != "" {
		return state.Message{}, humanCursor, usageErrorf("human_intervention_pending")
	}
	return latest, humanCursor, nil
}

func humanApprovalQuestion(projectDir, profile, session, gate, kind, action, target string) (state.Message, error) {
	question, _, err := latestStrictGateQuestion(projectDir, profile, session, gate, kind, action, target)
	return question, err
}

func latestStrictGateQuestion(projectDir, profile, session, gate, kind, action, target string) (state.Message, []state.Message, error) {
	baseRoot, err := scanBaseRootForProject(projectDir)
	if err != nil {
		return state.Message{}, nil, err
	}
	snap, err := state.Build(projectDir, baseRoot, state.DefaultProbe)
	if err != nil {
		return state.Message{}, nil, err
	}
	sess, ok := findThreadsSession(snap.Sessions, profile, session)
	if !ok {
		return state.Message{}, nil, usageErrorf("session %q not found", session)
	}
	msgs, warnings := state.ScanSessionMessages(sess.Root, selfOperatorNow)
	if len(warnings) > 0 {
		return state.Message{}, nil, usageErrorf("message scan degraded; approval fails closed")
	}
	var latest *state.Message
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread == gate && msg.Kind == state.KindQuestion && (latest == nil || messageAfter(msg, *latest)) {
			copy := msg
			latest = &copy
		}
	}
	if latest == nil {
		return state.Message{}, nil, usageErrorf("no gate question on %s", gate)
	}
	binding, bindErr := operatorauth.ParseStrictBinding(latest.Subject + "\n" + latest.Body)
	if bindErr != nil || !binding.Matches(kind, action, target) {
		return state.Message{}, nil, usageErrorf("latest gate question does not have the exact Gate-Kind/Action/Target binding")
	}
	return *latest, msgs, nil
}

func validateSelfMergeEvidence(path, target string) ([]byte, string, verifyMergeEvidence, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return nil, "", verifyMergeEvidence{}, usageErrorf("self merge approval requires a persisted --evidence file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", verifyMergeEvidence{}, err
	}
	evidence, err := readVerifyMergeEvidence(path, nil)
	if err != nil {
		return nil, "", verifyMergeEvidence{}, err
	}
	if result := validateVerifyMergeEvidence(evidence); !result.OK || strings.TrimSpace(evidence.Base) == "" {
		return nil, "", verifyMergeEvidence{}, usageErrorf("strict verify merge evidence failed or omitted base")
	}
	parsed, parseErr := operatorauth.ParseMergeTarget(target)
	if parseErr != nil || parsed.Subject != evidence.Subject || parsed.Head != evidence.HeadSHA || parsed.Base != evidence.Base {
		return nil, "", verifyMergeEvidence{}, usageErrorf("merge target must bind exact PR, head, and base")
	}
	sum := sha256.Sum256(b)
	return b, fmt.Sprintf("sha256:%x", sum), evidence, nil
}

func selfApprovalStoreDir(projectDir, profile, session string) string {
	return filepath.Join(projectDir, team.DirName, "evidence", profile, session, "self-operator")
}

func safeGateFile(gate string) string { return strings.NewReplacer("/", "_", "..", "_").Replace(gate) }
func selfApprovalReservationPath(projectDir, profile, session, gate string) string {
	return filepath.Join(selfApprovalStoreDir(projectDir, profile, session), safeGateFile(gate)+".reservation.json")
}
func selfApprovalEvidencePath(projectDir, profile, session, gate, questionID, digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	return filepath.Join(selfApprovalStoreDir(projectDir, profile, session), safeGateFile(gate)+"-"+safeGateFile(questionID)+"-"+digest+".preflight.json")
}

func reserveSelfApproval(path string, reservation selfApprovalReservation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return flock.WithLock(path+".lock", func() error {
		if b, err := os.ReadFile(path); err == nil {
			var existing selfApprovalReservation
			if json.Unmarshal(b, &existing) != nil || existing.AnswerMessageID != "" || existing.ExpiresAt.After(selfOperatorNow()) {
				return usageErrorf("self approval already reserved for this gate")
			}
		}
		b, _ := json.MarshalIndent(reservation, "", "  ")
		return atomicWriteJSONBytes(path, b)
	})
}

func newReservationToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validateSelfApprovalReservation(path string, want selfApprovalReservation) error {
	return flock.WithLock(path+".lock", func() error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var got selfApprovalReservation
		if err := json.Unmarshal(b, &got); err != nil {
			return err
		}
		if got.Token != want.Token || got.QuestionMessageID != want.QuestionMessageID || got.PolicyRevision != want.PolicyRevision || got.PolicyHash != want.PolicyHash || got.HumanCursor != want.HumanCursor || got.Sending || got.AnswerMessageID != "" || got.ExpiresAt.Before(selfOperatorNow()) {
			return usageErrorf("self approval reservation changed or expired")
		}
		return nil
	})
}

func markSelfApprovalSending(path, token string) error {
	return flock.WithLock(path+".lock", func() error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var reservation selfApprovalReservation
		if err := json.Unmarshal(b, &reservation); err != nil {
			return err
		}
		if reservation.Token != token || reservation.Sending || reservation.AnswerMessageID != "" || reservation.ExpiresAt.Before(selfOperatorNow()) {
			return usageErrorf("self approval reservation token mismatch or expired")
		}
		reservation.Sending = true
		encoded, _ := json.MarshalIndent(reservation, "", "  ")
		return atomicWriteJSONBytes(path, encoded)
	})
}

func clearExpiredSelfApprovalReservation(path, token string) error {
	return flock.WithLock(path+".lock", func() error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var reservation selfApprovalReservation
		if err := json.Unmarshal(b, &reservation); err != nil {
			return err
		}
		if reservation.Token != token || !reservation.Sending || reservation.AnswerMessageID != "" || reservation.ExpiresAt.After(selfOperatorNow()) {
			return usageErrorf("self approval retry reservation changed or is not safely expired")
		}
		return os.Remove(path)
	})
}

func markSelfApprovalSent(path, token, answerID string) error {
	return flock.WithLock(path+".lock", func() error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var reservation selfApprovalReservation
		if err := json.Unmarshal(b, &reservation); err != nil {
			return err
		}
		if reservation.Token != token || !reservation.Sending || reservation.AnswerMessageID != "" {
			return usageErrorf("self approval reservation token mismatch")
		}
		reservation.AnswerMessageID = answerID
		reservation.ExpiresAt = time.Time{}
		encoded, _ := json.MarshalIndent(reservation, "", "  ")
		return atomicWriteJSONBytes(path, encoded)
	})
}

func writeImmutableEvidence(path string, b []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(b) {
			return fmt.Errorf("immutable evidence collision at %s", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWriteJSONBytes(path, b)
}

func atomicWriteJSONBytes(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".atomic-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func writeSelfApprovalReceipt(projectDir, profile, session, gate, answerID string, receipt operatorauth.Receipt) error {
	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(selfApprovalStoreDir(projectDir, profile, session), safeGateFile(gate)+"-"+safeGateFile(answerID)+".receipt.json")
	return atomicWriteJSONBytes(path, b)
}
