package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Acceptance 7 (brief 3-1): credential_change is in the operatorauth taxonomy,
// so a credential gate can be answered, and a gh auth login guard verdict
// turns to allow after a verified credential_change answer.
func TestGuardCredentialGateOpensWithVerifiedAnswer(t *testing.T) {
	f := newHookFixture(t)
	call := guardCall{Tool: "Bash", Args: map[string]string{"command": "gh auth login --with-token"}, CWD: f.repo}
	v := evaluateGuard(f.id(), call, f.now)
	if v.Verdict != "needs_gate" || v.ReasonCode != "gate_credential" || v.ActionClass != "credential_change" || v.Target != "gh auth login" {
		t.Fatalf("before the answer: %+v", v)
	}
	f.gate(t, "g-cred", "credential_change", "gh auth login", answerSigned, "cto")
	if v := evaluateGuard(f.id(), call, f.now); v.Verdict != "allow" {
		t.Fatalf("after a verified credential_change answer: %+v", v)
	}
	// the answer binds its target: another credential command stays gated
	other := guardCall{Tool: "Bash", Args: map[string]string{"command": "gh auth logout"}, CWD: f.repo}
	if v := evaluateGuard(f.id(), other, f.now); v.Verdict != "needs_gate" {
		t.Fatalf("other credential command: %+v", v)
	}
	// a credential write is its own target
	write := guardCall{Tool: "Write", Args: map[string]string{"file_path": "~/.ssh/config"}, CWD: f.repo}
	v = evaluateGuard(f.id(), write, f.now)
	if v.Verdict != "needs_gate" {
		t.Fatalf("credential write: %+v", v)
	}
	f.gate(t, "g-ssh", "credential_change", v.Target, answerSigned, "cto")
	if v := evaluateGuard(f.id(), write, f.now); v.Verdict != "allow" {
		t.Fatalf("credential write after its answer: %+v", v)
	}
}

// The gate store the core writes is read by guard and context unchanged in
// contract: a gate raised through internal/gatestore and an approval
// committed there opens guard; a committed deny record closes the gate in
// context without opening guard.
func TestGateStoreWrittenByCoreIsReadByGuardAndContext(t *testing.T) {
	f := newHookFixture(t)
	signer := f.signer(t)
	store, err := gatestore.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	ident := gatestore.Identity{OwnerPubKey: strings.Repeat("a1", 32), IssuerKeyID: signer.KeyID()}
	send := hookTestSender{}
	raise := func(id, target string) gatestore.Record {
		rec, _, err := store.Raise(gatestore.Spec{GateID: id, Action: "tag", Target: target, RaisedBy: "cto"}, ident, f.now.Add(-time.Minute), send)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	commit := func(rec gatestore.Record, decision, event string) {
		ts := f.now.Format(time.RFC3339Nano)
		p := operatorauth.AuthorizationPayload{
			Namespace: operatorauth.NamespaceBinding{ProjectDir: f.project, Profile: "default", Session: "s1", NamespaceID: "default/s1", Generation: "g1"},
			Gate:      "gate/" + rec.GateID, Thread: "gate/" + rec.GateID, GateKind: rec.GateKind, Action: rec.Class, Target: rec.Digest,
			QuestionMessageID: "nostr:" + strings.Repeat("c3", 32), AnswerMessageID: "nostr:" + event, QuestionCreatedAt: ts, AnswerCreatedAt: ts, VerifiedAt: ts,
			Actor:    operatorauth.AuthorizationActor{Role: "operator", Handle: "owner", Source: "human"},
			Issuer:   operatorauth.AuthorizationIssuer{Binary: "amq-squad", Version: "test"},
			Evidence: []operatorauth.AuthorizationEvidence{{Kind: "gate.text", Identity: "gate/" + rec.GateID, SHA256: rec.TextSHA256}},
		}
		newEnv := operatorauth.NewAuthorizationEnvelope
		if decision == operatorauth.DecisionDenied {
			newEnv = operatorauth.NewDenialEnvelope
		}
		env, err := newEnv(p, signer)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Commit(gatestore.AnswerFile{Schema: gatestore.AnswerSchema, Scheme: "bip340", Decision: decision, Envelope: env,
			Evidence: gatestore.Evidence{Scheme: "bip340", EventID: event}, Gate: rec}); err != nil {
			t.Fatal(err)
		}
	}
	tag := func(v string) guardVerdict {
		return evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "git tag " + v}, CWD: f.repo}, f.now)
	}
	open := func() []string {
		var ids []string
		for _, g := range buildHookContext(f.id(), f.project, "", f.now).OpenGates {
			ids = append(ids, g.GateID)
		}
		return ids
	}
	raise("g-approve", "git tag v1")
	raise("g-deny", "git tag v2")
	if v := tag("v1"); v.Verdict != "needs_gate" {
		t.Fatalf("before the answer: %+v", v)
	}
	if got := strings.Join(open(), ","); got != "g-approve,g-deny" {
		t.Fatalf("open gates = %s", got)
	}
	commit(mustLoad(t, store, "g-approve"), operatorauth.DecisionApproved, strings.Repeat("0a", 32))
	commit(mustLoad(t, store, "g-deny"), operatorauth.DecisionDenied, strings.Repeat("0b", 32))
	if v := tag("v1"); v.Verdict != "allow" {
		t.Fatalf("approved gate: %+v", v)
	}
	if v := tag("v2"); v.Verdict != "needs_gate" {
		t.Fatalf("denied gate opened guard: %+v", v)
	}
	if got := open(); len(got) != 0 {
		t.Fatalf("open gates after approve and deny = %v", got)
	}
}

func mustLoad(t *testing.T, s gatestore.Store, id string) gatestore.Record {
	t.Helper()
	r, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type hookTestSender struct{}

func (hookTestSender) SignQuestion(r gatestore.Record) (gatestore.Question, error) {
	return gatestore.Question{GateID: r.GateID, EventID: strings.Repeat("c3", 32), RawEvent: "{}", PostedAt: "2026-10-23T11:59:00Z"}, nil
}

func (hookTestSender) PublishQuestion(gatestore.Question) error { return nil }

// Ruling 1 at the 3-1 halfway checkpoint: the gate store's consumed/ marks
// (and denials/, reasks/) sit under the protected extension dir, and the
// owner's gate binding and issuer key are owner files guard protects.
func TestGuardProtectsGateStoreAndOwnerGateFiles(t *testing.T) {
	f := newHookFixture(t)
	for _, p := range []string{
		hookExtPath(f.root, "consumed", "gates", "g1"),
		hookExtPath(f.root, "consumed", "events", strings.Repeat("a", 64)),
		hookExtPath(f.root, "denials", "g1.json"),
		hookExtPath(f.root, "reasks", "g1.json"),
		"~/.amq-squad/gate-binding.json",
		"~/.amq-squad/issuer-key.pem",
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("write %s: %+v", p, v)
		}
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "rm -rf " + hookExtPath(f.root, "consumed")}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
		t.Errorf("rm consumed/: %+v", v)
	}
}

// Review finding 3: an ordinary Edit that rewrites part of a marker (here the
// begin marker's prefix, outside the block body) is applied in memory and
// refused, because the result no longer has the managed block.
func TestReview3EditCannotStripTheManagedMarker(t *testing.T) {
	f := newHookFixture(t)
	mustWrite(t, filepath.Join(f.repo, "CLAUDE.md"), "Intro\n<!-- amq-squad:managed:begin -->\nrules\n<!-- amq-squad:managed:end -->\n")
	for name, args := range map[string]map[string]string{
		"begin marker prefix": {"file_path": "CLAUDE.md", "old_string": "Intro\n<!-- amq-", "new_string": "Intro\n<!-- other-"},
		"end marker suffix":   {"file_path": "CLAUDE.md", "old_string": "managed:end -->", "new_string": "managed:gone -->"},
		"replace_all inside":  {"file_path": "CLAUDE.md", "old_string": "r", "new_string": "R", "replace_all": "true"},
		"absent search text":  {"file_path": "CLAUDE.md", "old_string": "nowhere", "new_string": "x"},
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Edit", Args: args, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("%s: %+v", name, v)
		}
	}
	multi := `[{"old_string":"Intro","new_string":"Intro2"},{"old_string":"Intro2\n<!-- amq-squad","new_string":"Intro2\n<!-- x"}]`
	if v := evaluateGuard(f.id(), guardCall{Tool: "MultiEdit", Args: map[string]string{"file_path": "CLAUDE.md", "edits": multi}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
		t.Errorf("chained multiedit: %+v", v)
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Edit", Args: map[string]string{"file_path": "CLAUDE.md", "old_string": "Intro", "new_string": "Preface"}, CWD: f.repo}, f.now); v.Verdict != "allow" {
		t.Errorf("edit outside the block: %+v", v)
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": "CLAUDE.md",
		"content": "New\n<!-- amq-squad:managed:begin -->\nrules\n<!-- amq-squad:managed:end -->\n<!-- amq-squad:managed:begin -->\n"}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
		t.Errorf("write adding a second begin marker: %+v", v)
	}
}
