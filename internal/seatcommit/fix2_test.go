package seatcommit

import (
	"encoding/base64"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Tests of the 2b-2 milestone B fix commit 2 (the second pass on 1b39380f).

// N1 (git.go:338): the snapshot fed filenames to hash-object --stdin-paths,
// whose quoted-path parser turned a file named "\057etc\057hosts" into
// /etc/hosts and committed its contents under the worktree's name. Each
// path goes as an argument after --; the blob committed is the file's own.
func TestQuotedFilenameIsHashedAsItself(t *testing.T) {
	f := newFixture(t)
	name := `"\057etc\057hosts"` // at the worktree root: the stdin line starts with the quote
	write(t, filepath.Join(f.wt, name), "x\n")
	res := f.run(f.reported("cmd/", "*"))
	if !res.OK || !res.Pushed {
		t.Fatalf("commit: %+v", res)
	}
	out, err := runGit(t, f.dir, "--git-dir="+f.origin, "cat-file", "blob", res.Commit+":"+name)
	if err != nil || out != "x\n" {
		t.Fatalf("the blob under %s is %d bytes, not the file's own 2 (%v); its content is not printed", name, len(out), err)
	}
}

// R6 (parse.go:118): push_url accepted a relative name (origin), which a
// seat satisfies with a symlink in the worktree. push_url is an absolute
// URL of the admitted schemes only; a bare path needs local_push_ack; and
// http://, git://, another https host, credentials in the URL, ext:: and
// fd:: are grants_invalid at parse.
func TestPushURLSchemesAtParse(t *testing.T) {
	grant := func(url string, ack bool) string {
		s := "schema: 1\nmerge:\n  - repository: o/r\n    base_branch: main\n    size_cap_lines: 300\n    push_url: " + url + "\n"
		if ack {
			s += "    local_push_ack: true\n"
		}
		return s
	}
	ok := map[string]bool{"git@github.com:o/r.git": false, "ssh://git@github.com/o/r.git": false, "https://github.com/o/r.git": false, "/srv/git/r.git": true, "file:///srv/git/r.git": true}
	for url, ack := range ok {
		if _, err := grants.ParseOwner(grant(url, ack), "grants.yaml"); err != nil {
			t.Errorf("%s refused: %v", url, err)
		}
	}
	for _, url := range []string{"origin", "../r.git", "http://github.com/o/r.git", "git://github.com/o/r.git", "https://gitlab.com/o/r.git", "https://u:p@github.com/o/r.git", "ext::sh -c x", "fd::3", "-uorigin"} {
		if _, err := grants.ParseOwner(grant(url, true), "grants.yaml"); err == nil {
			t.Errorf("%s admitted", url)
		}
	}
	if _, err := grants.ParseOwner(grant("/srv/git/r.git", false), "grants.yaml"); err == nil || !strings.Contains(err.Error(), "local_push_ack") {
		t.Errorf("a bare path without the ack: %v", err)
	}
}

// D1: squad.yaml cannot set the push target: a push_url there never reaches
// the grant or the ledger.
func TestSquadYAMLCannotSetThePushURL(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "grants.yaml")
	os.WriteFile(owner, []byte("schema: 1\nmerge:\n  - repository: o/r\n    base_branch: main\n    size_cap_lines: 300\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "squad.yaml"), []byte("merge_repositories: [o/r]\npush_url: git@evil.example:x/y.git\nmerge:\n  - repository: o/r\n    push_url: git@evil.example:x/y.git\n"), 0o600)
	g, err := grants.LoadFrom(owner, dir)
	if err != nil {
		t.Fatal(err)
	}
	if tg := TargetFor(g); tg.PushURL != "" {
		t.Fatalf("squad.yaml set the push target %q", tg.PushURL)
	}
}

// D2, the launch: a local push target without the owner's ack is refused
// (push_target_unacknowledged), and so is an https target without the
// pinned gh; with the ack the launch records it.
func TestLaunchRefusesAnUnacknowledgedPushTarget(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		t    Target
		want string
	}{
		{Target{Repository: fixtureRepo, BaseBranch: "main", PushURL: f.origin}, ReasonPushTargetUnacknowledged},
		{Target{Repository: fixtureRepo, BaseBranch: "main", PushURL: "origin", LocalPushAck: true}, ReasonPushTargetUnacknowledged},
		{Target{Repository: fixtureRepo, BaseBranch: "main", PushURL: "https://github.com/o/r.git"}, registry.PinMissing},
		{Target{Repository: fixtureRepo, BaseBranch: "main", PushURL: f.origin, LocalPushAck: true}, ""},
	} {
		_, err := RecordWorktreeBase(gitBin(t), f.root, f.handle, f.wt, f.branch, tc.t, f.now)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("target %+v: %v (want %q)", tc.t, err, tc.want)
		}
	}
}

// D2, commit time: the target the launch recorded must still be the grant's;
// a grant that changed (another push_url, the ack withdrawn) refuses
// push_target_unacknowledged for the owner and commits and pushes nothing.
func TestCommitRefusesATargetTheGrantNoLongerNames(t *testing.T) {
	f := newFixture(t)
	f.grants.Merge[0].PushURL = filepath.Join(f.dir, "other.git")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// t\n")
	res := f.run(f.reported("cmd/"))
	if res.ReasonCode != ReasonPushTargetUnacknowledged || res.Route != task.RouteOwner || res.Commit != "" || f.originBranch() != "" {
		t.Fatalf("a changed grant: %+v (origin %q)", res, f.originBranch())
	}
}

// D2, the gh pin: the launch records the registry's pinned gh with its
// digest; the commit step re-hashes it before use, and a gh that changed
// since the launch is never run (no PR, the push refused for https).
func TestPinnedGhIsReHashedBeforeUse(t *testing.T) {
	f := newFixture(t)
	gh := filepath.Join(f.dir, "gh-pinned")
	write(t, gh, "#!/bin/sh\necho '[]'\n")
	os.Chmod(gh, 0o755)
	sum, _ := registry.FileSHA256(gh)
	home := filepath.Join(f.dir, "reghome")
	os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700)
	if err := registry.Write(registry.Path(home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "gh", Path: gh, SHA256: sum}}}); err != nil {
		t.Fatal(err)
	}
	tg, err := LaunchTarget(f.grants, home, ToolsDir(f.root))
	if err != nil || tg.GhPinPath != gh || tg.GhSHA256 != sum {
		t.Fatalf("launch target: %+v %v", tg, err)
	}
	l := Ledger{Target: tg}
	if tool, why := pinnedGh(l); why != "" || tool.Path != tg.GhPath {
		t.Fatalf("pinned gh: %v %q", tool, why)
	} else {
		tool.Close()
	}
	os.Chmod(tg.GhPath, 0o700)
	write(t, tg.GhPath, "#!/bin/sh\necho swapped\n")
	if _, why := pinnedGh(l); !strings.Contains(why, registry.PinMismatch) {
		t.Fatalf("a swapped gh passed: %q", why)
	}
	write(t, gh, "#!/bin/sh\necho swapped\n")
	if _, err := LaunchTarget(f.grants, home, ToolsDir(f.root)); err == nil || !strings.Contains(err.Error(), registry.PinMismatch) {
		t.Fatalf("the launch took a swapped gh: %v", err)
	}
}

// D2, the https push: the harness passes the pinned gh as the only
// credential helper itself (credential.helper reset, then the pinned gh),
// with gh's token variables on this call only; a credential helper the
// seat planted in the repository config never runs. An httptest server in
// front of git http-backend requires basic auth with the token the fake gh
// returns (the test seam admits http://127.0.0.1 as the https kind).
func TestHTTPSPushUsesOnlyThePinnedGhCredentialHelper(t *testing.T) {
	f := newFixture(t)
	backend := filepath.Join(strings.TrimSpace(sh(t, f.dir, "--exec-path")), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("no git-http-backend")
	}
	srvRoot := filepath.Join(f.dir, "srv")
	os.MkdirAll(srvRoot, 0o755)
	remote := filepath.Join(srvRoot, "r.git")
	sh(t, f.dir, "init", "-q", "--bare", "-b", "main", remote)
	sh(t, remote, "config", "http.receivepack", "true")
	sh(t, remote, "config", "receive.autogc", "false")
	sh(t, f.main, "push", "-q", remote, "main")
	const token = "CANARY-gh-push-token-2b2b-fix2" // a canary, in no real credential's shape
	var mu sync.Mutex
	authed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="t"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		authed++
		mu.Unlock()
		(&cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + srvRoot, "GIT_HTTP_EXPORT_ALL=1"}}).ServeHTTP(w, r)
	}))
	defer srv.Close()
	prev := pushKindOf
	pushKindOf = func(u string) string {
		if strings.HasPrefix(u, srv.URL+"/") {
			return grants.PushHTTPS
		}
		return prev(u)
	}
	t.Cleanup(func() { pushKindOf = prev })
	marker := filepath.Join(f.dir, "seat-helper-ran")
	gh := filepath.Join(f.dir, "gh-pinned")
	write(t, gh, "#!/bin/sh\nif [ \"$1 $2 $3\" = \"auth git-credential get\" ]; then echo username=x-access-token; echo password="+token+"; fi\n")
	os.Chmod(gh, 0o755)
	sum, _ := registry.FileSHA256(gh)
	// The seat plants its own helper in the repository config.
	sh(t, f.wt, "config", "credential.helper", "!touch "+marker+"; echo password=evil #")
	url := srv.URL + "/r.git"
	tg := Target{Repository: fixtureRepo, BaseBranch: "main", PushURL: url, GhPath: gh, GhSHA256: sum}
	if _, err := RecordTarget(f.root, f.handle, tg); err != nil {
		t.Fatal(err)
	}
	f.grants.Merge[0].PushURL, f.grants.Merge[0].LocalPushAck = url, false
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// https\n")
	res := f.run(f.reported("cmd/"))
	if !res.OK || !res.Pushed {
		t.Fatalf("the https push: %+v", res)
	}
	if got, _ := exec.Command(gitBin(t), "--git-dir="+remote, "rev-parse", "refs/heads/"+res.Branch).Output(); strings.TrimSpace(string(got)) != res.Commit || res.Branch != "seat/s1-t1" {
		t.Fatalf("the remote has %q, want %s", got, res.Commit)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the seat's credential helper ran")
	}
	mu.Lock()
	defer mu.Unlock()
	if authed == 0 {
		t.Fatal("the push never authenticated with the pinned gh's token")
	}
}

// P1-10: the hook reports the digest on every context call, so a removed
// hook is itself the drift signal: a Claude seat that ran (an accepted
// proof in the core's launch ledger) with no report since its launch is
// quarantined at its exit (claude_hooks_silent); one report clears it.
func TestClaudeSeatWithNoHookReportIsQuarantinedAtExit(t *testing.T) {
	f := newFixture(t)
	dir := claudeseat.Dir(f.root, f.handle, "launch-1")
	w, err := claudeseat.Write(dir, "dontAsk", f.wt, f.now)
	if err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{Handle: f.handle, Root: f.root, Session: "sess", Runtime: "claude", Worktree: f.wt, Branch: f.branch, LaunchID: "launch-1", ClaudeConfig: w.Digest}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(f.root, f.handle), launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteProof(f.root, launch.ProofEntry{Handle: f.handle, PID: 4242, Start: "s", At: f.now}); err != nil {
		t.Fatal(err)
	}
	o := f.opts(task.Task{ID: "t1"})
	if r := ExitCheck(o, "gone"); !r.Quarantine || r.ReasonCode != claudeseat.HooksSilent {
		t.Fatalf("a silent seat: %+v", r)
	}
	g := newFixture(t)
	dir = claudeseat.Dir(g.root, g.handle, "launch-1")
	w, _ = claudeseat.Write(dir, "dontAsk", g.wt, g.now)
	rec.Root, rec.Worktree, rec.ClaudeConfig = g.root, g.wt, w.Digest
	launch.WriteFile(filepath.Join(launch.StagedDir(g.root, g.handle), launch.FileName), rec)
	launch.WriteProof(g.root, launch.ProofEntry{Handle: g.handle, PID: 4242, Start: "s", At: g.now})
	claudeseat.RecordReport(g.root, g.handle, claudeseat.Report{At: g.now.Add(time.Second), Digest: w.Digest, OK: true})
	if r := ExitCheck(g.opts(task.Task{ID: "t1"}), "gone"); !r.OK {
		t.Fatalf("a seat whose hook reported: %+v", r)
	}
}

// D2: gh's environment for the PR calls retains nothing of gh (no
// GH_CONFIG_DIR, no token variable; gh authenticates from the owner's login
// under HOME); the token variables are kept on the https push only.
func TestGhEnvRetainsNothingOfGh(t *testing.T) {
	env := strings.Join(ghEnv([]string{"HOME=/h", "PATH=/bin", "GH_CONFIG_DIR=/seat/gh", "GH_TOKEN=x", "GITHUB_TOKEN=y", "GH_HOST=evil"}), " ")
	for _, gone := range []string{"GH_CONFIG_DIR", "GH_TOKEN", "GITHUB_TOKEN", "GH_HOST"} {
		if strings.Contains(env, gone+"=") {
			t.Fatalf("%s reached gh", gone)
		}
	}
	if !strings.Contains(env, "HOME=/h") {
		t.Fatal("gh lost HOME")
	}
	_, pushEnv, _ := pushCredentials(Options{Env: []string{"GH_TOKEN=x", "GH_CONFIG_DIR=/seat/gh"}}, Ledger{})
	if len(pushEnv) != 0 {
		t.Fatalf("a non-https push got gh's variables: %d", len(pushEnv))
	}
}

// Third pass (pins.go:62, :78; commit.go:784, :798, :835, :843): the pin was
// hashed through a descriptor opened without O_NOFOLLOW, the pathname
// reopened to execute, and verified once per PR sequence. The pin is opened
// through the anchored walk (a symlinked pin path is refused), the hashed
// descriptor is what runs (/dev/fd/3 on Linux, the verified path right after
// the hash on darwin), and every gh invocation re-verifies: a gh swapped
// between the PR list and the create is never run.
func TestPinIsOpenedWithoutFollowingAndReVerifiedPerInvocation(t *testing.T) {
	f := newFixture(t)
	real := filepath.Join(f.dir, "gh-real")
	write(t, real, "#!/bin/sh\necho '[]'\n")
	os.Chmod(real, 0o755)
	sum, _ := registry.FileSHA256(real)
	link := filepath.Join(f.dir, "gh-link")
	os.Symlink(real, link)
	if tool, code, _ := registry.OpenPinned("gh", link, sum); tool != nil || code != registry.PinMismatch {
		t.Fatalf("a symlinked pin path was opened: %v %s", tool, code)
	}
	// A gh that lists no PR, then swaps itself before the create.
	calls := filepath.Join(f.dir, "gh-calls")
	body := "#!/bin/sh\necho \"$1 $2\" >> " + calls + "\nif [ \"$2\" = list ]; then cp " + filepath.Join(f.dir, "gh-evil") + " " + filepath.Join(f.dir, "gh-pinned") + "; echo '[]'; exit 0; fi\necho https://github.com/o/r/pull/9\n"
	gh := filepath.Join(f.dir, "gh-pinned")
	write(t, gh, body)
	os.Chmod(gh, 0o755)
	write(t, filepath.Join(f.dir, "gh-evil"), "#!/bin/sh\necho evil-ran >> "+calls+"\necho https://github.com/o/r/pull/66\n")
	os.Chmod(filepath.Join(f.dir, "gh-evil"), 0o755)
	ghSum, _ := registry.FileSHA256(gh)
	tg := f.target()
	tg.GhPath, tg.GhSHA256 = gh, ghSum
	if _, err := RecordTarget(f.root, f.handle, tg); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// pin\n")
	tk := f.reported("cmd/")
	o := f.opts(tk)
	o.OpenPR = true
	f.storeBinding(&o)
	res := Run(o)
	raw, _ := os.ReadFile(calls)
	if strings.Contains(string(raw), "evil-ran") || res.PRNumber != 0 {
		t.Fatalf("a gh swapped after the list ran: %+v\n%s", res, raw)
	}
	// with --open-pr the failed PR call defers the completion (pr_pending, phase 5 re-pass P2-3)
	if !res.Deferred || res.ReasonCode != ReasonPRPending || !strings.Contains(res.Detail, registry.PinMismatch) {
		t.Fatalf("the swap was not named, or the step completed: %+v", res)
	}
}

// Fourth pass, the pin ruling (commit.go:787-789): git execs the credential
// helper by pathname after the harness's check, which no descriptor can
// close (the recorded same-UID limit). The mitigation: the launch copies
// the verified gh, from its hashed descriptor, into the core-written 0700
// tools directory and the target names that copy, never the registry path
// a seat can see (a registry gh replaced after the launch is not what
// runs); the copy is re-hashed immediately before the push, so a replaced
// copy refuses the push (tool_pin_mismatch).
func TestPinnedGhIsCopiedAtLaunchAndReHashedBeforeThePush(t *testing.T) {
	f := newFixture(t)
	gh := filepath.Join(f.dir, "gh-registry")
	write(t, gh, "#!/bin/sh\necho '[]'\n")
	os.Chmod(gh, 0o755)
	sum, _ := registry.FileSHA256(gh)
	home := filepath.Join(f.dir, "reghome")
	os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700)
	if err := registry.Write(registry.Path(home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "gh", Path: gh, SHA256: sum}}}); err != nil {
		t.Fatal(err)
	}
	tools := ToolsDir(f.root)
	tg, err := LaunchTarget(f.grants, home, tools)
	if err != nil || tg.GhSHA256 != sum || tg.GhPinPath != gh {
		t.Fatalf("launch target: %+v %v", tg, err)
	}
	if filepath.Dir(tg.GhPath) != tools || tg.GhPath == gh {
		t.Fatalf("the helper is not the harness's copy in %s: %s", tools, tg.GhPath)
	}
	if st, err := os.Stat(tools); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the tools directory: %v %v", st, err)
	}
	// The registry gh (a path a seat can see) replaced after the launch:
	// the copy is what the push's helper names.
	write(t, gh, "#!/bin/sh\necho swapped\n")
	l := Ledger{Target: tg}
	l.PushURL = "https://github.com/o/r"
	args, _, why := pushCredentials(Options{}, l)
	if why != "" || !strings.Contains(strings.Join(args, " "), tg.GhPath) {
		t.Fatalf("the push helper: %v %q", args, why)
	}
	// The copy replaced: the pre-push re-hash refuses.
	os.Chmod(tg.GhPath, 0o700)
	write(t, tg.GhPath, "#!/bin/sh\necho swapped\n")
	if _, _, why := pushCredentials(Options{}, l); !strings.Contains(why, registry.PinMismatch) {
		t.Fatalf("a replaced copy was used for the push: %q", why)
	}
}
