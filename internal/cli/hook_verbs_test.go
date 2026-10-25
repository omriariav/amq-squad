package cli

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/role"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

type hookFixture struct {
	root, home, project, repo, agentDir string
	now                                 time.Time
}

// newHookFixture builds a session root with a cto mailbox, a team home, a
// seat worktree on branch seat-cto (default branch main), and a HOME without
// an owner grants file, and injects the seat identity.
func newHookFixture(t *testing.T) hookFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := hookFixture{
		root: filepath.Join(base, "project", ".agent-mail", "s1"), home: filepath.Join(base, "home"),
		project: filepath.Join(base, "project"), repo: filepath.Join(base, "wt"),
		now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
	f.agentDir = filepath.Join(f.root, "agents", "cto")
	for _, d := range []string{f.agentDir, filepath.Join(f.root, "agents", "qa"), f.home, filepath.Join(f.repo, ".git", "refs", "remotes", "origin")} {
		mustMkdir(t, d)
	}
	mustWrite(t, filepath.Join(f.repo, ".git", "HEAD"), "ref: refs/heads/seat-cto\n")
	mustWrite(t, filepath.Join(f.repo, ".git", "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/main\n")
	t.Setenv("HOME", f.home)
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", f.root)
	t.Setenv("AM_SESSION", "s1")
	t.Setenv("AMQ_SQUAD_BRANCH", "")
	t.Setenv("AMQ_SQUAD_RULES_PATH", "")
	prevNow := hookNow
	hookNow = func() time.Time { return f.now }
	t.Cleanup(func() { hookNow = prevNow })
	return f
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f hookFixture) id() hookIdentity {
	return hookIdentity{Handle: "cto", Root: f.root, Session: "s1", AgentDir: f.agentDir}
}

// Answer modes for gate: no answer, an envelope signed by the trusted core
// key, the same envelope with a forged signature, or a signed envelope whose
// target digest is another target's.
const (
	answerNone = iota
	answerSigned
	answerForged
	answerOtherTarget
	answerSignedPastNotAfter // a verified answer on a record whose not_after has passed
)

// gate writes a gate record binding action+target and, per mode, its answer.
func (f hookFixture) gate(t *testing.T, id, action, target string, mode int, raisedBy string) {
	t.Helper()
	digest := gatetarget.Digest(action, target)
	notAfter := f.now.Add(time.Hour)
	if mode == answerSignedPastNotAfter {
		notAfter = f.now.Add(-time.Second)
	}
	rec, _ := json.Marshal(map[string]string{"schema": "s1", "gate_id": id, "class": action, "digest": digest, "not_after": notAfter.Format(time.RFC3339), "raised_by": raisedBy})
	mustWrite(t, hookExtPath(f.root, "gates", id+".json"), string(rec))
	if mode == answerNone {
		return
	}
	signed := digest
	if mode == answerOtherTarget {
		signed = gatetarget.Digest(action, target+"-other")
	}
	cap, err := operatorauth.LookupAction(action)
	if err != nil {
		t.Fatal(err)
	}
	ts := f.now.Add(-time.Minute).Format(time.RFC3339Nano)
	env, err := operatorauth.NewAuthorizationEnvelope(operatorauth.AuthorizationPayload{
		Namespace: operatorauth.NamespaceBinding{ProjectDir: f.project, Profile: "default", Session: "s1", NamespaceID: "default/s1", Generation: "g1"},
		Gate:      "gate/" + id, Thread: "gate/" + id, GateKind: cap.GateKind, Action: action, Target: signed,
		QuestionMessageID: "q1", AnswerMessageID: "a1", QuestionCreatedAt: ts, AnswerCreatedAt: ts, VerifiedAt: ts,
		Actor:    operatorauth.AuthorizationActor{Role: "operator", Handle: "owner", Source: "human"},
		Issuer:   operatorauth.AuthorizationIssuer{Binary: "amq-squad", Version: "test"},
		Evidence: []operatorauth.AuthorizationEvidence{{Kind: "gate", Identity: "gate/" + id, SHA256: digest}},
	}, f.signer(t))
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if mode == answerForged {
		env.Signature.Value = base64.StdEncoding.EncodeToString(make([]byte, 64))
	}
	ans, _ := json.Marshal(map[string]any{"schema": "s1-answer", "envelope": env})
	mustWrite(t, hookExtPath(f.root, "answers", id+".json"), string(ans))
}

// signer returns the core issuer key the owner's trust store trusts,
// creating both on first use.
func (f hookFixture) signer(t *testing.T) operatorauth.AuthorizationSigner {
	t.Helper()
	keyPath := filepath.Join(f.home, "issuer.pem")
	if _, err := os.Stat(keyPath); err != nil {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		der, _ := x509.MarshalPKCS8PrivateKey(priv)
		mustWrite(t, keyPath, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
		trust, _ := json.Marshal(operatorauth.AuthorizationTrustStore{SchemaVersion: 1, Keys: []operatorauth.AuthorizationTrustKey{{
			KeyID: operatorauth.KeyIDForEd25519(pub), Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Status: "trusted",
		}}})
		mustWrite(t, filepath.Join(f.home, ".amq-squad", "authorization-trust.json"), string(trust))
	}
	s, err := operatorauth.LoadEd25519Signer(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func withHookStdin(t *testing.T, body []byte) {
	t.Helper()
	prev := hookStdin
	hookStdin = bytes.NewReader(body)
	t.Cleanup(func() { hookStdin = prev })
}

func TestContextReportsSeededRoleRulesTaskAndGates(t *testing.T) {
	f := newHookFixture(t)
	mustWrite(t, role.Path(f.agentDir), "# Role: CTO\nOwn the architecture.\n")
	mustWrite(t, filepath.Join(f.agentDir, "extensions", role.LayerName, "launch.json"), fmt.Sprintf(`{"schema":1,"handle":"cto","session":"s1","root":%q,"team_home":%q,"started_at":"2026-10-23T11:00:00Z"}`, f.root, f.project))
	mustWrite(t, filepath.Join(f.project, ".amq-squad", "team-rules.md"), "# Team Rules\n")
	// The 3.0 store under the session root (acceptance 7): cto finished t1,
	// holds a claimed lease on t2; qa holds t3; t4 waits in the pool.
	store, err := taskstore.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"old", "build the verbs", "qa's", "queued"} {
		if _, err := store.Add(taskstore.AddInput{Title: title, Class: "implement"}, f.now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	steps := []func() (taskstore.Task, error){
		func() (taskstore.Task, error) { return store.Claim("t1", "cto", nil, time.Hour, f.now.Add(-time.Hour)) },
		func() (taskstore.Task, error) {
			harnessPass(t, store, "t1", f.now.Add(-30*time.Minute))
			return store.Done("t1", "cto", "ok", "", f.now.Add(-30*time.Minute))
		},
		func() (taskstore.Task, error) {
			return store.Claim("t2", "cto", nil, time.Hour, f.now.Add(-time.Minute))
		},
		func() (taskstore.Task, error) { return store.Grant("t3", "qa", nil, time.Hour, f.now) },
	}
	for _, step := range steps {
		if _, err := step(); err != nil {
			t.Fatal(err)
		}
	}
	t2Before, err := os.ReadFile(filepath.Join(taskstore.DirFor(f.root), "t2.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.gate(t, "g-open", "tag", "tag:v1", answerNone, "cto")
	f.gate(t, "g-forged", "tag", "tag:v3", answerForged, "")
	f.gate(t, "g-qa", "tag", "tag:v2", answerNone, "qa")
	f.gate(t, "g-done", "external_send", "send:x", answerSigned, "")

	stdout, _, err := captureOutput(t, func() error { return Run([]string{"context", "--me", "cto", "--json"}, "test") })
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	var env struct {
		Data hookContextData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode %s: %v", stdout, err)
	}
	d := env.Data
	if d.RoleText != "# Role: CTO\nOwn the architecture.\n" || !strings.HasPrefix(d.Role.SHA256, "sha256:") {
		t.Errorf("role = %+v %q", d.Role, d.RoleText)
	}
	// sha256 of "# Team Rules\n"
	if d.Rules.Path != filepath.Join(f.project, ".amq-squad", "team-rules.md") || d.Rules.SHA256 != "sha256:"+sha256Hex("# Team Rules\n") {
		t.Errorf("rules = %+v", d.Rules)
	}
	if d.Task == nil || d.Task.ID != "t2" || d.Task.Expired || !d.Task.Claimed || d.Task.Class != "implement" {
		t.Errorf("task = %+v, want t2 (the lease cto holds)", d.Task)
	}
	if after, _ := os.ReadFile(filepath.Join(taskstore.DirFor(f.root), "t2.json")); string(after) != string(t2Before) {
		t.Error("context wrote the task store (it must never renew a lease)")
	}
	qa := buildHookContext(hookIdentity{Handle: "qa", Root: f.root, AgentDir: filepath.Join(f.root, "agents", "qa")}, f.project, f.now)
	if qa.Task == nil || qa.Task.ID != "t3" || qa.Task.Claimed {
		t.Errorf("qa task = %+v, want the unclaimed t3", qa.Task)
	}
	// g-forged stays open: an answer file whose envelope does not verify never counts.
	if len(d.OpenGates) != 2 || d.OpenGates[0].GateID != "g-forged" || d.OpenGates[1].GateID != "g-open" {
		t.Errorf("open gates = %+v, want g-forged and g-open", d.OpenGates)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("warnings = %v", d.Warnings)
	}
	text, _, err := captureOutput(t, func() error { return Run([]string{"context"}, "test") })
	if err != nil || !strings.Contains(text, "task: t2 in_progress") || !strings.Contains(text, "open gate: g-open") || !strings.Contains(text, "Own the architecture.") {
		t.Errorf("text context (%v):\n%s", err, text)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestContextWithoutLaunchRecordHasNoTaskAndRulesFromEnv(t *testing.T) {
	f := newHookFixture(t)
	rulesPath := filepath.Join(f.project, "rules.md")
	mustWrite(t, rulesPath, "r")
	t.Setenv("AMQ_SQUAD_RULES_PATH", rulesPath)
	d := buildHookContext(f.id(), "", f.now)
	if d.Task != nil || d.Rules.Path != rulesPath || len(d.OpenGates) != 0 {
		t.Fatalf("context = %+v", d)
	}
	if len(d.Warnings) != 1 || !strings.HasPrefix(d.Warnings[0], "role:") {
		t.Fatalf("warnings = %v, want the missing role only", d.Warnings)
	}
}

func TestHookVerbIdentityErrors(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"no handle", map[string]string{"AM_ME": ""}, nil, "seat handle"},
		{"flag contradicts env", nil, []string{"--me", "qa2"}, "disagrees with AM_ME"},
		{"relative root", map[string]string{"AM_ROOT": "rel/root"}, nil, "clean absolute path"},
		{"no mailbox", map[string]string{"AM_ME": "ghost"}, nil, "no mailbox"},
		{"bad flag", nil, []string{"--nope"}, "flag provided but not defined"},
		{"positional", nil, []string{"extra"}, "no positional arguments"},
	}
	for _, verb := range [][]string{{"context"}, {"guard", "--tool", "Bash"}, {"evidence", "ingest"}} {
		for _, tc := range cases {
			t.Run(verb[0]+"/"+tc.name, func(t *testing.T) {
				newHookFixture(t)
				for k, v := range tc.env {
					t.Setenv(k, v)
				}
				withHookStdin(t, []byte(`{}`))
				stdout, _, err := captureOutput(t, func() error { return Run(append(append([]string{}, verb...), tc.args...), "test") })
				// guard never exits 2: an error is exit 1 with no verdict (R36 fail-open).
				want := 2
				if verb[0] == "guard" {
					want = 1
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) || ExitCode(err) != want || strings.Contains(err.Error(), "\n") || stdout != "" {
					t.Fatalf("err = %v (exit %d), stdout %q; want one-line exit %d containing %q", err, ExitCode(err), stdout, want, tc.want)
				}
			})
		}
	}
}

func TestGuardRuleMatrix(t *testing.T) {
	type tc struct {
		name, tool string
		args       map[string]string
		setup      func(t *testing.T, f hookFixture)
		verdict    string
		reason     string
	}
	bash := func(cmd string) map[string]string { return map[string]string{"command": cmd} }
	gate := func(action, target string, mode int) func(*testing.T, hookFixture) {
		return func(t *testing.T, f hookFixture) { f.gate(t, "g-"+action, action, target, mode, "cto") }
	}
	approve := func(action, target string) func(*testing.T, hookFixture) { return gate(action, target, answerSigned) }
	grants := func(body string) func(*testing.T, hookFixture) {
		return func(t *testing.T, f hookFixture) {
			mustWrite(t, filepath.Join(f.home, ".amq-squad", "grants.yaml"), body)
		}
	}
	grantsAnd := func(body string, also func(*testing.T, hookFixture)) func(*testing.T, hookFixture) {
		return func(t *testing.T, f hookFixture) { grants(body)(t, f); also(t, f) }
	}
	growGate := func(mode int) func(*testing.T, hookFixture) {
		return func(t *testing.T, f hookFixture) { f.gate(t, "g-grow", "spawn", "grow "+f.root+" seats=3", mode, "") }
	}
	cases := []tc{
		// tag and release
		{"release/needs_gate tag", "Bash", bash("git tag v1.2.0"), nil, "needs_gate", "gate_release"},
		{"release/needs_gate gh release", "Bash", bash("cd x && gh release create v1.2.0 --notes x"), nil, "needs_gate", "gate_release"},
		{"release/needs_gate npm publish", "Bash", bash("npm publish"), nil, "needs_gate", "gate_release"},
		{"release/allow verified answer", "Bash", bash("git tag v1.2.0"), approve("tag", "git tag v1.2.0"), "allow", "allowed"},
		{"release/allow gh release verified", "Bash", bash("gh release create v1.2.0"), approve("github_release", "gh release create v1.2.0"), "allow", "allowed"},
		{"release/allow list", "Bash", bash("git tag -l"), nil, "allow", "allowed"},
		{"release/needs_gate same action other target digest", "Bash", bash("git tag v9"), approve("tag", "git tag v1.2.0"), "needs_gate", "gate_release"},
		{"release/needs_gate answer signed for another digest", "Bash", bash("git tag v1.2.0"), gate("tag", "git tag v1.2.0", answerOtherTarget), "needs_gate", "gate_release"},
		{"release/needs_gate forged answer", "Bash", bash("git tag v1.2.0"), gate("tag", "git tag v1.2.0", answerForged), "needs_gate", "gate_release"},
		{"release/needs_gate answer for another action", "Bash", bash("gh release create v1.2.0"), approve("tag", "gh release create v1.2.0"), "needs_gate", "gate_release"},
		{"release/deny verified but merge", "Bash", bash("git tag v1.2.0 && gh pr merge 7"), approve("tag", "git tag v1.2.0"), "deny", "seat_merge"},
		// external send
		{"send/needs_gate mcp", "mcp__slack__send_message", map[string]string{"channel": "C1", "text": "hi"}, nil, "needs_gate", "gate_send"},
		{"send/needs_gate gws", "Bash", bash("gws gmail send --to a@b.c --subject s"), nil, "needs_gate", "gate_send"},
		{"send/allow verified answer", "mcp__slack__send_message", map[string]string{"channel": "C1"}, approve("external_send", "mcp__slack__send_message C1"), "allow", "allowed"},
		{"send/allow read tool", "mcp__slack__read_channel", map[string]string{"channel": "C1"}, nil, "allow", "allowed"},
		{"send/deny verified but grants invalid", "mcp__slack__send_message", map[string]string{"channel": "C1"}, grantsAnd("bogus: 1\n", approve("external_send", "mcp__slack__send_message C1")), "deny", "grants_invalid"},
		// credential change (credential_change answers verify since 3-1: TestGuardCredentialGateOpensWithVerifiedAnswer)
		{"credential/needs_gate gh auth", "Bash", bash("gh auth login --with-token"), nil, "needs_gate", "gate_credential"},
		{"credential/needs_gate ssh path", "Write", map[string]string{"file_path": "~/.ssh/config"}, nil, "needs_gate", "gate_credential"},
		{"credential/needs_gate git config", "Bash", bash("git config --global credential.helper store"), nil, "needs_gate", "gate_credential"},
		{"credential/allow gh auth status", "Bash", bash("gh auth status"), nil, "allow", "allowed"},
		{"credential/deny status then force push", "Bash", bash("gh auth status; git push -f origin seat-cto"), nil, "deny", "force_push"},
		// growth beyond the grant
		{"grow/allow within", "Bash", bash("amq launch --seat dev"), nil, "allow", "allowed"},
		{"grow/needs_gate beyond", "Bash", bash("amq launch --seat dev"), grants("max_seats: 2\n"), "needs_gate", "gate_grow"},
		{"grow/needs_gate squad.yaml narrows", "Bash", bash("amq launch --seat dev"), func(t *testing.T, f hookFixture) {
			mustWrite(t, filepath.Join(f.repo, "squad.yaml"), "seats:\n  - cto\nmax_seats: 2\n")
		}, "needs_gate", "gate_grow"},
		{"grow/allow verified answer", "Bash", bash("amq coop exec dev"), grantsAnd("max_seats: 2\n", growGate(answerSigned)), "allow", "allowed"},
		{"grow/deny verified but push main", "Bash", bash("amq launch x && git push origin main"), grantsAnd("max_seats: 2\n", growGate(answerSigned)), "deny", "seat_merge"},
		// merge to the default branch
		{"merge/deny gh pr merge", "Bash", bash("gh pr merge 12 --squash"), nil, "deny", "seat_merge"},
		{"merge/deny merge api", "Bash", bash("gh api -X PUT repos/o/r/pulls/12/merge"), nil, "deny", "seat_merge"},
		{"merge/deny push to main", "Bash", bash("git push origin seat-cto:main"), nil, "deny", "seat_merge"},
		{"merge/allow merge main into seat branch", "Bash", bash("git merge origin/main"), nil, "allow", "allowed"},
		// 3-2 credential custody: the owner's GitHub and release credentials
		{"cred/deny gh auth token", "Bash", bash("GH=$(gh auth token) && echo ok"), nil, "deny", "credential_read"},
		{"cred/deny gh auth status --show-token", "Bash", bash("gh auth status --show-token"), nil, "deny", "credential_read"},
		{"cred/allow gh auth status", "Bash", bash("gh auth status"), nil, "allow", "allowed"},
		{"cred/deny keychain password", "Bash", bash("security find-generic-password -s gh:github.com -w"), nil, "deny", "credential_read"},
		{"cred/deny git credential fill", "Bash", bash("printf 'host=github.com\\n' | git credential fill"), nil, "deny", "credential_read"},
		{"cred/deny osxkeychain helper", "Bash", bash("git credential-osxkeychain get"), nil, "deny", "credential_read"},
		{"cred/deny standalone helper by path", "Bash", bash("printf 'host=github.com\\n' | /Library/Developer/CommandLineTools/usr/libexec/git-core/git-credential-osxkeychain get"), nil, "deny", "credential_read"},
		{"cred/deny standalone helper on PATH", "Bash", bash("git-credential-store get"), nil, "deny", "credential_read"},
		{"cred/deny launchd ssh agent", "Bash", bash("export SSH_AUTH_SOCK=$(launchctl getenv SSH_AUTH_SOCK)"), nil, "deny", "credential_read"},
		{"merge/needs_gate gate first", "Bash", bash("git tag v3 && gh pr merge 1"), nil, "needs_gate", "gate_release"},
		// pushes
		{"push/allow own branch", "Bash", bash("git push -u origin seat-cto"), nil, "allow", "allowed"},
		{"push/allow implicit", "Bash", bash("git push"), nil, "allow", "allowed"},
		{"push/allow HEAD", "Bash", bash("git -C . push origin HEAD"), nil, "allow", "allowed"},
		{"push/deny other branch", "Bash", bash("git push origin feature-x"), nil, "deny", "push_other_branch"},
		{"push/deny force", "Bash", bash("git push --force origin seat-cto"), nil, "deny", "force_push"},
		{"push/deny f", "Bash", bash("git push -f"), nil, "deny", "force_push"},
		{"push/deny plus refspec", "Bash", bash("git push origin +seat-cto"), nil, "deny", "force_push"},
		{"push/deny lease", "Bash", bash("git push --force-with-lease"), nil, "deny", "force_push"},
		{"push/deny all", "Bash", bash("git push --all origin"), nil, "deny", "push_other_branch"},
		{"push/deny delete", "Bash", bash("git push origin :seat-cto"), nil, "deny", "push_delete"},
		{"push/deny in bash -c", "Bash", bash(`bash -c "git push origin other"`), nil, "deny", "push_other_branch"},
		{"push/needs_gate tags", "Bash", bash("git push origin --tags"), nil, "needs_gate", "gate_release"},
		{"push/needs_gate tag refspec", "Bash", bash("git push origin refs/tags/v1"), nil, "needs_gate", "gate_release"},
		// protected paths
		{"path/deny write", "Write", map[string]string{"file_path": ".amq-squad/team-rules.md"}, grants("protected_paths:\n  - .amq-squad/team-rules.md\n"), "deny", "protected_path"},
		{"path/deny dir tree", "Edit", map[string]string{"file_path": "hooks/pre.sh"}, grants("protected_paths:\n  - hooks/\n"), "deny", "protected_path"},
		{"path/deny glob", "Bash", bash("echo x > conf/a.yaml"), grants("protected_paths:\n  - conf/*.yaml\n"), "deny", "protected_path"},
		{"path/deny rm", "Bash", bash("rm -f .amq-squad/team-rules.md"), grants("protected_paths:\n  - .amq-squad/team-rules.md\n"), "deny", "protected_path"},
		{"path/deny squad.yaml adds", "Write", map[string]string{"file_path": "docs/x.md"}, func(t *testing.T, f hookFixture) {
			mustWrite(t, filepath.Join(f.repo, "squad.yaml"), "protected_paths:\n  - docs/\n")
		}, "deny", "protected_path"},
		{"path/deny gate store", "Write", map[string]string{"file_path": "<gate store>"}, nil, "deny", "protected_path"},
		{"path/deny owner grants", "Bash", bash("echo 'max_seats: 99' > ~/.amq-squad/grants.yaml"), nil, "deny", "protected_path"},
		{"path/allow other file", "Write", map[string]string{"file_path": "src/main.go"}, grants("protected_paths:\n  - .amq-squad/team-rules.md\n"), "allow", "allowed"},
		{"path/allow redirect to /dev/null", "Bash", bash("go test ./... 2>&1 >/dev/null | tail"), nil, "allow", "allowed"},
		{"path/needs_gate credential write", "Bash", bash("echo k >> ~/.ssh/authorized_keys"), grants("protected_paths:\n  - ~/.ssh/\n"), "needs_gate", "gate_credential"},
		// everything else
		{"other/allow read", "Read", map[string]string{"file_path": "/etc/hosts"}, nil, "allow", "allowed"},
		{"other/allow tests", "Bash", bash("go test ./... && git commit -m x"), nil, "allow", "allowed"},
		// review defects (2a-3 fix commit), one row each; (1) and (8) are ingest
		// and output tests below
		{"fix2/deny push --repo= refspec to main", "Bash", bash("git push --repo=origin HEAD:main"), nil, "deny", "seat_merge"},
		{"fix2/deny push --repo refspec to main", "Bash", bash("git push --repo origin HEAD:main"), nil, "deny", "seat_merge"},
		{"fix3/deny short cluster -vf", "Bash", bash("git push -vf origin seat-cto"), nil, "deny", "force_push"},
		{"fix4/deny $( ) inside double quotes", "Bash", bash(`echo "$(git push --force origin main)"`), nil, "deny", "seat_merge"},
		{"fix4/deny backquote inside double quotes", "Bash", bash("echo \"`git push --force origin seat-cto`\""), nil, "deny", "force_push"},
		{"fix5/deny sudo -u root wrapper", "Bash", bash("sudo -u root git push --force origin main"), nil, "deny", "seat_merge"},
		{"fix6/deny /bin/bash -lc", "Bash", bash("/bin/bash -lc 'git push origin other'"), nil, "deny", "push_other_branch"},
		{"fix7/allow git -C as the last word", "Bash", bash("git -C"), nil, "allow", "allowed"},
		{"fix9/allow squad.yaml without max_seats keeps the owner cap", "Bash", bash("amq launch --seat dev"), func(t *testing.T, f hookFixture) {
			mustWrite(t, filepath.Join(f.home, ".amq-squad", "grants.yaml"), "max_seats: 20\n")
			mustWrite(t, filepath.Join(f.repo, "squad.yaml"), "protected_paths:\n  - docs/\n")
			for i := 0; i < 12; i++ {
				mustMkdir(t, filepath.Join(f.root, "agents", fmt.Sprintf("s%02d", i)))
			}
		}, "allow", "allowed"},
		{"fix10/deny dotdot after a symlink", "Write", map[string]string{"file_path": "link/../secret"}, func(t *testing.T, f hookFixture) {
			vault := filepath.Join(filepath.Dir(f.repo), "vault")
			mustMkdir(t, filepath.Join(vault, "subdir"))
			if err := os.Symlink(filepath.Join(vault, "subdir"), filepath.Join(f.repo, "link")); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(f.home, ".amq-squad", "grants.yaml"), "protected_paths:\n  - "+filepath.Join(vault, "secret")+"\n")
		}, "deny", "protected_path"},
		{"fix11/deny git -C another checkout on main", "Bash", bash("git -C ../main-wt merge seat-cto"), func(t *testing.T, f hookFixture) {
			mustWrite(t, filepath.Join(filepath.Dir(f.repo), "main-wt", ".git", "HEAD"), "ref: refs/heads/main\n")
		}, "deny", "seat_merge"},
		{"fix12/allow verified answer past not_after", "Bash", bash("git tag v1.2.0"), gate("tag", "git tag v1.2.0", answerSignedPastNotAfter), "allow", "allowed"},
		{"fix13/allow verified answer for a dot-led gate id", "Bash", bash("git tag v1.2.0"), func(t *testing.T, f hookFixture) {
			f.gate(t, ".g-dot", "tag", "git tag v1.2.0", answerSigned, "")
		}, "allow", "allowed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHookFixture(t)
			if c.setup != nil {
				c.setup(t, f)
			}
			args := map[string]string{}
			for k, v := range c.args {
				args[k] = v
			}
			if args["file_path"] == "<gate store>" {
				args["file_path"] = hookExtPath(f.root, "answers", "g-x.json")
			}
			call := guardCall{Tool: c.tool, Args: args, CWD: f.repo}
			got := evaluateGuard(f.id(), call, f.now)
			if got.Verdict != c.verdict || got.ReasonCode != c.reason {
				t.Fatalf("guard = %+v, want %s %s", got, c.verdict, c.reason)
			}
			if again := evaluateGuard(f.id(), call, f.now); again != got {
				t.Fatalf("not deterministic: %+v then %+v", got, again)
			}
		})
	}
}

func TestGuardSeatBranchAndDefaultBranchSources(t *testing.T) {
	f := newHookFixture(t)
	mustWrite(t, filepath.Join(f.repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	push := guardCall{Tool: "Bash", Args: map[string]string{"command": "git push origin HEAD"}, CWD: f.repo}
	if v := evaluateGuard(f.id(), push, f.now); v.ReasonCode != "seat_merge" {
		t.Fatalf("push from the default branch = %+v", v)
	}
	// On the default branch, git merge is a merge to it.
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "git merge seat-cto"}, CWD: f.repo}, f.now); v.ReasonCode != "seat_merge" {
		t.Fatalf("git merge on main = %+v", v)
	}
	t.Setenv("AMQ_SQUAD_BRANCH", "seat-cto")
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "git push origin seat-cto"}, CWD: f.repo}, f.now); v.Verdict != "allow" {
		t.Fatalf("push to AMQ_SQUAD_BRANCH = %+v", v)
	}
	mustWrite(t, filepath.Join(f.home, ".amq-squad", "grants.yaml"), "default_branch: trunk\n")
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "git push origin trunk"}, CWD: f.repo}, f.now); v.ReasonCode != "seat_merge" {
		t.Fatalf("push to the granted default branch = %+v", v)
	}
}

func TestHookGrantsSourcesAndFormat(t *testing.T) {
	f := newHookFixture(t)
	if g, err := loadHookGrants(f.repo); err != nil || g.Source != "default" || g.MaxSeats != 12 || len(g.ProtectedPaths) != 0 {
		t.Fatalf("defaults = %+v, %v", g, err)
	}
	// The session root is not a grants source.
	mustWrite(t, filepath.Join(f.root, "grants.yaml"), "max_seats: 3\n")
	if g, _ := loadHookGrants(f.repo); g.MaxSeats != 12 || g.Source != "default" {
		t.Fatalf("session-root grants were read: %+v", g)
	}
	owner := filepath.Join(f.home, ".amq-squad", "grants.yaml")
	mustWrite(t, owner, "# owner\nschema: 1\nmax_seats: 5 # cap\ndefault_branch: \"main\"\nprotected_paths:\n  - a/b\n  - '~/.claude/settings.json'\n")
	g, err := loadHookGrants(f.repo)
	if err != nil || g.Source != owner || g.MaxSeats != 5 || strings.Join(g.ProtectedPaths, ",") != "a/b,~/.claude/settings.json" {
		t.Fatalf("owner grants = %+v, %v", g, err)
	}
	// squad.yaml only narrows: a lower cap and extra paths count, a higher cap does not.
	mustWrite(t, filepath.Join(f.repo, "squad.yaml"), "seats:\n  - name: cto\nmax_seats: 9\nprotected_paths:\n  - c\ndefault_branch: dev\n")
	if g, _ := loadHookGrants(f.repo); g.MaxSeats != 5 || g.DefaultBranch != "main" || strings.Join(g.ProtectedPaths, ",") != "a/b,~/.claude/settings.json,c" {
		t.Fatalf("squad.yaml narrowing = %+v", g)
	}
	mustWrite(t, filepath.Join(f.repo, "squad.yaml"), "max_seats: 2\n")
	if g, _ := loadHookGrants(f.repo); g.MaxSeats != 2 {
		t.Fatalf("squad.yaml cap = %+v", g)
	}
	// A group- or world-writable owner file fails closed (3-1 ruling 1): no
	// seat starts, its protected paths still apply, typed gates stay closed
	// even with a verified answer.
	mustWrite(t, owner, "max_seats: 5\nprotected_paths:\n  - a/b\n")
	if err := os.Chmod(owner, 0o666); err != nil {
		t.Fatal(err)
	}
	if g, err := loadHookGrants(""); err != nil || g.MaxSeats != 0 || !g.Insecure || g.ReasonCode != "owner_grants_insecure" || strings.Join(g.ProtectedPaths, ",") != "a/b" {
		t.Fatalf("insecure owner file = %+v, %v", g, err)
	}
	v := evaluateGuard(f.id(), guardCall{Tool: "Read", Args: map[string]string{}, CWD: f.repo}, f.now)
	if v.Verdict != "allow" || v.GrantsReasonCode != "owner_grants_insecure" {
		t.Fatalf("guard with insecure owner grants = %+v", v)
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": "a/b"}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
		t.Fatalf("insecure file's protected path = %+v", v)
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "amq launch --seat dev"}, CWD: f.repo}, f.now); v.Verdict != "needs_gate" || v.ReasonCode != "gate_grow" {
		t.Fatalf("seat start under insecure grants = %+v", v)
	}
	f.gate(t, "g-tag", "tag", "git tag v1", answerSigned, "cto")
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "git tag v1"}, CWD: f.repo}, f.now); v.Verdict != "needs_gate" || v.ReasonCode != "owner_grants_insecure" {
		t.Fatalf("verified tag answer under insecure grants = %+v", v)
	}
	if err := os.Chmod(owner, 0o600); err != nil {
		t.Fatal(err)
	}
	if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": "git tag v1"}, CWD: f.repo}, f.now); v.Verdict != "allow" {
		t.Fatalf("verified tag answer under secure grants = %+v", v)
	}
	for _, bad := range []string{"schema: 2\n", "max_seats: 0\n", "max_seats: x\n", "nope: 1\n", "- orphan\n", "protected_paths:\n- unindented\n", "  max_seats: 3\n", "protected_paths: a\n"} {
		if _, err := grants.ParseOwner(bad, "t"); err == nil {
			t.Errorf("ParseOwner(%q) accepted", bad)
		}
		mustWrite(t, owner, bad)
		if v := evaluateGuard(f.id(), guardCall{Tool: "Read", Args: map[string]string{}, CWD: f.repo}, f.now); v.ReasonCode != "grants_invalid" {
			t.Errorf("guard with owner file %q = %+v", bad, v)
		}
	}
}

// TestGuardImplicitProtectedSet covers the R37 set (3-1 ruling 3): rules,
// settings, hook files and squad.yaml are protected without any grants file;
// CLAUDE.md and AGENTS.md only in their managed block.
func TestGuardImplicitProtectedSet(t *testing.T) {
	f := newHookFixture(t)
	block := "<!-- amq-squad:managed:begin -->\nteam rules live in .amq-squad\n<!-- amq-squad:managed:end -->\n"
	mustWrite(t, filepath.Join(f.repo, "CLAUDE.md"), "# Mine\nuser notes\n"+block)
	mustWrite(t, filepath.Join(f.repo, "NOTES.md"), "plain\n")
	call := func(tool string, args map[string]string) guardVerdict {
		return evaluateGuard(f.id(), guardCall{Tool: tool, Args: args, CWD: f.repo}, f.now)
	}
	for _, c := range []struct {
		name, tool string
		args       map[string]string
		reason     string
	}{
		{"team rules", "Write", map[string]string{"file_path": ".amq-squad/team-rules.md"}, "protected_path"},
		{"repo settings", "Edit", map[string]string{"file_path": ".claude/settings.local.json"}, "protected_path"},
		{"global settings", "Bash", map[string]string{"command": "echo {} > ~/.claude/settings.json"}, "protected_path"},
		{"codex config", "Write", map[string]string{"file_path": "~/.codex/config.toml"}, "protected_path"},
		{"hook plugin files", "Write", map[string]string{"file_path": ".claude/hooks/guard.sh"}, "protected_path"},
		{"installed plugins", "Bash", map[string]string{"command": "rm -rf ~/.claude/plugins/cache/amq-squad"}, "protected_path"},
		{"squad.yaml", "Write", map[string]string{"file_path": "squad.yaml"}, "protected_path"},
		{"rules path override", "Write", map[string]string{"file_path": "rules/custom.md"}, "protected_path"},
		{"managed block edited", "Edit", map[string]string{"file_path": "CLAUDE.md", "old_string": "team rules live in .amq-squad", "new_string": "no rules"}, "protected_path"},
		{"managed marker removed", "Edit", map[string]string{"file_path": "CLAUDE.md", "old_string": "<!-- amq-squad:managed:end -->", "new_string": ""}, "protected_path"},
		{"managed block rewritten", "Write", map[string]string{"file_path": "CLAUDE.md", "content": "# Mine\n"}, "protected_path"},
		{"managed file by shell", "Bash", map[string]string{"command": "sed -i '' s/rules/x/ CLAUDE.md"}, "protected_path"},
		{"multiedit into block", "MultiEdit", map[string]string{"file_path": "CLAUDE.md", "edits": `[{"old_string":"user notes","new_string":"n"},{"old_string":"rules live","new_string":"x"}]`}, "protected_path"},
		{"user part edited", "Edit", map[string]string{"file_path": "CLAUDE.md", "old_string": "user notes", "new_string": "more user notes"}, "allowed"},
		{"user part rewritten, block kept", "Write", map[string]string{"file_path": "CLAUDE.md", "content": "# New\n" + block}, "allowed"},
		{"multiedit outside block", "MultiEdit", map[string]string{"file_path": "CLAUDE.md", "edits": `[{"old_string":"user notes","new_string":"n"}]`}, "allowed"},
		{"AGENTS.md without a block", "Write", map[string]string{"file_path": "AGENTS.md", "content": "x"}, "allowed"},
		{"other file", "Write", map[string]string{"file_path": "NOTES.md"}, "allowed"},
		{"other settings-like name", "Write", map[string]string{"file_path": "docs/settings.json"}, "allowed"},
	} {
		t.Setenv("AMQ_SQUAD_RULES_PATH", filepath.Join(f.repo, "rules", "custom.md"))
		if got := call(c.tool, c.args); got.ReasonCode != c.reason {
			t.Errorf("%s: %+v, want %s", c.name, got, c.reason)
		}
	}
}

func TestGuardOutputContract(t *testing.T) {
	f := newHookFixture(t)
	claude := fmt.Sprintf(`{"session_id":"x","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push --force","timeout":5},"cwd":%q}`, f.repo)
	pi := fmt.Sprintf(`{"toolName":"bash","input":{"command":"git push origin seat-cto"},"cwd":%q}`, f.repo)
	tag := fmt.Sprintf(`{"tool_name":"Bash","tool_input":{"command":"git tag v1"},"cwd":%q}`, f.repo)
	for _, c := range []struct{ payload, verdict, code string }{{claude, "deny", "force_push"}, {pi, "allow", "allowed"}, {tag, "needs_gate", "gate_release"}} {
		withHookStdin(t, []byte(c.payload))
		out, _, err := captureOutput(t, func() error { return Run([]string{"guard", "--me", "cto", "--stdin"}, "test") })
		var obj map[string]any
		if err != nil || strings.Count(out, "\n") != 1 || json.Unmarshal([]byte(out), &obj) != nil || obj["verdict"] != c.verdict || obj["reason_code"] != c.code || obj["reason"] == nil {
			t.Fatalf("guard --stdin = %q, %v; want %s %s", out, err, c.verdict, c.code)
		}
		if c.verdict == "needs_gate" && (obj["action_class"] != "tag" || obj["target_digest"] != gatetarget.Digest("tag", "git tag v1")) {
			t.Fatalf("needs_gate output lacks action_class/target_digest: %s", out)
		}
	}
	withHookStdin(t, []byte("not json"))
	if out, _, err := captureOutput(t, func() error { return Run([]string{"guard", "--stdin"}, "test") }); ExitCode(err) != 1 || out != "" {
		t.Fatalf("malformed guard payload: %q %v", out, err)
	}
	out, _, err := captureOutput(t, func() error {
		return Run([]string{"guard", "--tool", "Bash", "--arg", "command=git push origin x", "--cwd", f.repo}, "test")
	})
	if err != nil || !strings.Contains(out, `"reason_code":"push_other_branch"`) {
		t.Fatalf("guard --arg = %q, %v", out, err)
	}
}

func TestSplitShell(t *testing.T) {
	segs := splitShell(`FOO=1 git commit -m "a; b" && echo 'x > y' > out.txt 2>&1; cat <in | tee -a log $(git push) ` + "`date`")
	var got []string
	for _, s := range segs {
		got = append(got, strings.Join(s.words, " ")+"|"+strings.Join(s.writes, " "))
	}
	want := []string{"FOO=1 git commit -m a; b|", "echo x > y|out.txt", "cat <in|", "tee -a log|", "git push|", "date|"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("segments:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func readLedger(t *testing.T, path string) []hookLedgerLine {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var lines []hookLedgerLine
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 16<<20), 16<<20)
	for sc.Scan() {
		var l hookLedgerLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("ledger line %d does not parse (interleaved?): %v", len(lines)+1, err)
		}
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestEvidenceIngestAppendsSchemaLines(t *testing.T) {
	f := newHookFixture(t)
	post := []byte("{\n \"hook_event_name\": \"PostToolUse\", \"tool_name\": \"Bash\", \"tool_use_id\": \"toolu_1\",\n \"tool_response\": {\"stdout\": \"ok\"}}")
	for _, payload := range [][]byte{post, []byte(`{"tool_name":"Edit","tool_response":{"is_error":true}}`), []byte(`{"toolName":"bash","isError":true}`), []byte("{broken"), nil} {
		withHookStdin(t, payload)
		if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", "cto"}, "test") }); err != nil {
			t.Fatalf("ingest %q: %v", payload, err)
		}
	}
	path := hookExtPath(f.root, "ledger", "cto.jsonl")
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode: %v %v", st, err)
	}
	lines := readLedger(t, path)
	if len(lines) != 5 {
		t.Fatalf("lines = %d", len(lines))
	}
	l := lines[0]
	if l.Schema != 1 || l.Seq != 1 || l.Handle != "cto" || l.Session != "s1" || l.Event != "PostToolUse" || l.Tool != "Bash" || l.ToolUseID != "toolu_1" || l.Outcome != "ok" || l.At != "2026-10-23T12:00:00Z" {
		t.Errorf("line 1 = %+v", l)
	}
	if l.PayloadSHA256 != "sha256:"+sha256Hex(string(post)) || !strings.Contains(string(l.Payload), `"tool_use_id":"toolu_1"`) {
		t.Errorf("line 1 payload = %s %s", l.PayloadSHA256, l.Payload)
	}
	if lines[1].Outcome != "error" || lines[2].Outcome != "error" || lines[2].Tool != "bash" {
		t.Errorf("error outcomes = %+v / %+v", lines[1], lines[2])
	}
	if m := lines[3]; !m.Malformed || m.PayloadPrefix != "{broken" || m.Payload != nil || m.Seq != 4 {
		t.Errorf("malformed line = %+v", m)
	}
	if e := lines[4]; !e.Malformed || e.Seq != 5 {
		t.Errorf("empty payload line = %+v", e)
	}
}

func TestEvidenceIngestOversizeIsRecordedTruncated(t *testing.T) {
	f := newHookFixture(t)
	big := append([]byte(`{"tool_name":"Bash","pad":"`), bytes.Repeat([]byte("a"), 3<<20)...)
	big = append(big, '"', '}')
	withHookStdin(t, big)
	if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest"}, "test") }); err != nil {
		t.Fatalf("oversize: %v", err)
	}
	withHookStdin(t, bytes.Repeat([]byte("a"), hookMaxPayload))
	if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest"}, "test") }); err != nil {
		t.Fatalf("exactly 1 MiB: %v", err)
	}
	lines := readLedger(t, hookExtPath(f.root, "ledger", "cto.jsonl"))
	l := lines[0]
	prefix := []byte(l.PayloadPrefix)
	if !l.Truncated || l.Malformed || l.Payload != nil || l.PayloadBytes != int64(len(big)) || l.PayloadSHA256 != "sha256:"+sha256Hex(string(big)) || !bytes.Equal(prefix, big[:hookPrefixKeep]) || l.Seq != 1 {
		t.Fatalf("truncated record = seq %d truncated %v malformed %v bytes %d sha %s prefix %d", l.Seq, l.Truncated, l.Malformed, l.PayloadBytes, l.PayloadSHA256, len(prefix))
	}
	if m := lines[1]; m.Truncated || !m.Malformed || m.PayloadBytes != hookMaxPayload {
		t.Fatalf("1 MiB record = %+v", m.Truncated)
	}
}

// fix1: a payload of '<' must not be HTML-escaped sixfold, and a long last
// line must still be found by the backward seq scan.
func TestEvidenceIngestFix1HTMLHeavyPayloadKeepsSeq(t *testing.T) {
	f := newHookFixture(t)
	body := append([]byte(`{"x":"`), bytes.Repeat([]byte("<"), hookMaxPayload-16)...)
	body = append(body, '"', '}')
	for i := 0; i < 2; i++ {
		withHookStdin(t, body)
		if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest"}, "test") }); err != nil {
			t.Fatal(err)
		}
	}
	lines := readLedger(t, hookExtPath(f.root, "ledger", "cto.jsonl"))
	raw, _ := os.ReadFile(hookExtPath(f.root, "ledger", "cto.jsonl"))
	if len(lines) != 2 || lines[0].Seq != 1 || lines[1].Seq != 2 || len(raw) > 2*(hookMaxPayload+4096) {
		t.Fatalf("seqs %d,%d; ledger %d bytes for two 1 MiB payloads", lines[0].Seq, lines[len(lines)-1].Seq, len(raw))
	}
	// A pre-existing record far longer than any fixed window still yields its seq.
	path := hookExtPath(f.root, "ledger", "cto.jsonl")
	mustWrite(t, path, `{"schema":1,"seq":7,"pad":"`+strings.Repeat("x", 9<<20)+`"}`+"\n")
	withHookStdin(t, []byte(`{}`))
	if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest"}, "test") }); err != nil {
		t.Fatal(err)
	}
	if l := readLedger(t, path); l[len(l)-1].Seq != 8 {
		t.Fatalf("seq after a 9 MiB line = %d, want 8", l[len(l)-1].Seq)
	}
}

// fix7 and fix8: a panic inside the evaluation and a failed stdout write both
// exit 1, never 2.
func TestGuardFix7PanicAndFix8WriteErrorExitOne(t *testing.T) {
	f := newHookFixture(t)
	args := []string{"guard", "--tool", "Read", "--cwd", f.repo}
	prev := guardEvaluate
	guardEvaluate = func(hookIdentity, guardCall, time.Time) guardVerdict { panic("boom") }
	_, _, err := captureOutput(t, func() error { return Run(args, "test") })
	guardEvaluate = prev
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "internal error: boom") {
		t.Fatalf("panic: %v (exit %d)", err, ExitCode(err))
	}
	r, w, _ := os.Pipe()
	r.Close()
	w.Close()
	stdout := os.Stdout
	os.Stdout = w
	err = Run(args, "test")
	os.Stdout = stdout
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("stdout write error: %v (exit %d)", err, ExitCode(err))
	}
}

func TestEvidenceIngestTornTailKeepsSeq(t *testing.T) {
	f := newHookFixture(t)
	path := hookExtPath(f.root, "ledger", "cto.jsonl")
	mustWrite(t, path, `{"schema":1,"seq":41,"handle":"cto"}`+"\n"+`{"schema":1,"seq":42,"hand`)
	withHookStdin(t, []byte(`{}`))
	if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest"}, "test") }); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	last := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var l hookLedgerLine
	if json.Unmarshal([]byte(last[len(last)-1]), &l); l.Seq != 42 || len(last) != 3 {
		t.Fatalf("after a torn tail: %q", raw)
	}
}

// TestEvidenceIngestChild is one writer process for the concurrency test.
func TestEvidenceIngestChild(t *testing.T) {
	tag := os.Getenv("HOOK_INGEST_CHILD_TAG")
	if tag == "" {
		t.Skip("helper process")
	}
	// Each line is far above PIPE_BUF, so only the flock keeps lines whole.
	withHookStdin(t, []byte(`{"tool_name":"Bash","writer":"`+tag+`","pad":"`+strings.Repeat("x", 512<<10)+`"}`))
	if err := Run([]string{"evidence", "ingest", "--me", "cto", "--root", os.Getenv("HOOK_INGEST_CHILD_ROOT"), "--session", "s1"}, "test"); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceIngestConcurrentWritersDoNotInterleave(t *testing.T) {
	f := newHookFixture(t)
	const writers = 8
	var cmds []*exec.Cmd
	for i := 0; i < writers; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestEvidenceIngestChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), fmt.Sprintf("HOOK_INGEST_CHILD_TAG=w%d", i), "HOOK_INGEST_CHILD_ROOT="+f.root)
		out := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
		t.Cleanup(func() {
			if t.Failed() {
				t.Log(out.String())
			}
		})
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer: %v", err)
		}
	}
	lines := readLedger(t, hookExtPath(f.root, "ledger", "cto.jsonl"))
	if len(lines) != writers {
		t.Fatalf("lines = %d, want %d", len(lines), writers)
	}
	seen := map[string]bool{}
	for i, l := range lines {
		var p struct{ Writer string }
		_ = json.Unmarshal(l.Payload, &p)
		if l.Seq != uint64(i+1) || seen[p.Writer] || p.Writer == "" {
			t.Fatalf("line %d: seq %d writer %q (want seq 1..%d in file order, one line per writer)", i+1, l.Seq, p.Writer, writers)
		}
		seen[p.Writer] = true
	}
}

// TestHookVerbsColdRunBound bounds one cold guard and one cold context run
// on the fixture at 200 ms (a loose CI bound), and logs p50/p95 over 20 warm
// runs. The R36 50 ms figure is reported in docs/v3-spike/2a3-notes.md, not
// asserted here.
func TestHookVerbsColdRunBound(t *testing.T) {
	f := newHookFixture(t)
	mustWrite(t, role.Path(f.agentDir), "# Role\n")
	for i := 0; i < 20; i++ {
		mode := answerNone
		if i%2 == 0 {
			mode = answerSigned
		}
		f.gate(t, fmt.Sprintf("g-%02d", i), "tag", fmt.Sprintf("git tag v%d", i), mode, "cto")
	}
	mustWrite(t, filepath.Join(f.home, ".amq-squad", "grants.yaml"), "protected_paths:\n  - .amq-squad/\n  - conf/*.yaml\n")
	call := guardCall{Tool: "Bash", Args: map[string]string{"command": "git tag v4 && git push origin seat-cto && echo x > conf/a.txt"}, CWD: f.repo}
	guard := func() {
		if v := evaluateGuard(f.id(), call, f.now); v.Verdict != "allow" {
			t.Fatalf("fixture verdict = %+v", v)
		}
	}
	context := func() { buildHookContext(f.id(), f.project, f.now) }
	for name, fn := range map[string]func(){"guard": guard, "context": context} {
		start := time.Now()
		fn()
		cold := time.Since(start)
		var warm []time.Duration
		for i := 0; i < 20; i++ {
			start := time.Now()
			fn()
			warm = append(warm, time.Since(start))
		}
		sort.Slice(warm, func(i, j int) bool { return warm[i] < warm[j] })
		t.Logf("%s: cold %v, warm p50 %v, p95 %v", name, cold, warm[9], warm[18])
		if cold > 200*time.Millisecond {
			t.Fatalf("%s cold run %v over the 200 ms bound", name, cold)
		}
	}
}

func BenchmarkGuard(b *testing.B) {
	base := b.TempDir()
	root := filepath.Join(base, "root")
	_ = os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755)
	_ = os.MkdirAll(filepath.Join(base, "wt", ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(base, "wt", ".git", "HEAD"), []byte("ref: refs/heads/seat-cto\n"), 0o600)
	id := hookIdentity{Handle: "cto", Root: root, AgentDir: filepath.Join(root, "agents", "cto")}
	call := guardCall{Tool: "Bash", Args: map[string]string{"command": "git push origin seat-cto && go test ./..."}, CWD: filepath.Join(base, "wt")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluateGuard(id, call, time.Now())
	}
}
