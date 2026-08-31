package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealLaunchapiInspectSessionTargetShape is t10's real-binding
// verification (cto's ruling on task/t10 acceptance item 5, gh#766): t7's
// "Known limitation" flagged that liveness.go's launchapiSessionInspect sent
// Target.SessionRoot=ProjectRoot, and the pinned launchapi module's
// openExplicitBaseAuthority requires SessionRoot to be BaseRoot's direct
// child named Session instead -- unresolved without a live binding at the
// time. Source-tracing (openPrepareTarget/openExplicitBaseAuthority in the
// pinned v0.75.0 module) confirmed the mismatch independently of this test;
// this proves it against one real, fully-initialized .amqrc/base-root layout
// produced by a real `amq init` plus the .amqrc launchapi's own authority
// check requires (amq init itself does not write one -- confirmed directly
// against the real binary; a bare `amq env` resolution does not need it, but
// launchapi's openExplicitBaseAuthority does, per realAMQRootAuthorityCompatibilityContract's
// existing fixture convention).
//
// This same live run ALSO caught two further real defects beyond the
// originally-suspected SessionRoot mismatch, both now fixed above:
// ProjectRoot must be canonical (openPrepareTarget's literal "project_root
// must be canonical" check, comparing against filepath.Abs+EvalSymlinks),
// and BaseRoot must be canonicalized the same way for
// openExplicitBaseAuthority's configured-root string comparison to match --
// t.Project/baseRoot are not guaranteed symlink-free (a macOS temp dir under
// /var/folders resolves through /private/var), so both were failing
// independently of the SessionRoot shape being correct.
//
// A real launchapi launch never occurred against this fixture, so the
// expected fired outcome is launchapiInspectNotApplicable via a clean
// binding_missing ReasonCode (Inspect correctly identifies the target and
// finds no launchapi lifecycle state there yet) -- NOT any of the three
// pre-fix failure modes, which were all launchapiInspectNotApplicable via a
// Target-validation error instead of a real answer. All map to the same
// launchapiInspectOutcome enum value, so the Evidence string is what
// actually distinguishes "corroboration inert because inapplicable" from
// "corroboration inert because the request itself was malformed".
func TestRealLaunchapiInspectSessionTargetShape(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run the disposable real-launchapi Target shape proof")
	}
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("AMQ_SQUAD_REAL_AMQ %q is unavailable or not executable: %v", binary, err)
	}
	version := strings.TrimSpace(realAMQCommand(t, binary, t.TempDir(), nil, "version"))
	t.Logf("real AMQ binary=%s version=%s", binary, version)

	project := t.TempDir()
	session := "t10-real-binding"
	root := filepath.Join(project, ".agent-mail", session)
	realAMQInit(t, binary, project, root)
	baseRoot := filepath.Dir(root)
	rc := []byte("{\n  \"root\": \".agent-mail\"\n}\n")
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), rc, 0o600); err != nil {
		t.Fatal(err)
	}

	signal := launchapiSessionInspect(project, baseRoot, session)
	t.Logf("launchapiSessionInspect outcome=%v evidence=%q", signal.Outcome, signal.Evidence)

	if strings.Contains(signal.Evidence, "base_root_relation_invalid") || strings.Contains(signal.Evidence, "base_root_unauthorized") {
		t.Fatalf("Target shape rejected against a real, fully-initialized .amqrc/base-root layout -- the fix did not resolve the SessionRoot mismatch: outcome=%v evidence=%q", signal.Outcome, signal.Evidence)
	}
	if signal.Outcome != launchapiInspectNotApplicable {
		t.Fatalf("outcome = %v, want launchapiInspectNotApplicable (no launchapi launch occurred against this fixture, so binding_missing is the correct fired outcome): evidence=%q", signal.Outcome, signal.Evidence)
	}
	if !strings.Contains(signal.Evidence, "binding_missing") {
		t.Fatalf("evidence = %q, want binding_missing (a clean 'no binding here yet' result, not a Target validation failure)", signal.Evidence)
	}
}
