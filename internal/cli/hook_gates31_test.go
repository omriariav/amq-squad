package cli

import "testing"

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
