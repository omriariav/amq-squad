//go:build unix

package seatexec

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

const maxFile = 4 << 20

// Run is `amq-squad seat-exec --seat <handle> <provider argv...>` (plan 4.3,
// R4, R52). It verifies the AMQ root from AM_ROOT without following a
// symlink, reads the staged launch record and role.md, checks both sha256
// sums (and the rules file's), publishes the record and role into the seat's
// extension directory, builds the scrubbed environment with the identity
// from the record only, proves the identity with one read-only amq call,
// re-verifies role and rules, and execs the provider in place. It adds no
// provider flag. On success it does not return.
func Run(o Options) error {
	o = o.withDefaults()
	plan, err := prepare(o)
	if plan.published != nil {
		state := plan.rec
		// The raw start stamp lets a later close tell this process from a
		// reused pid (exec keeps the pid and the stamp); prepare refused
		// already when it is unreadable.
		state.Exec = &launch.Exec{At: o.Now().UTC(), Outcome: "exec", PID: os.Getpid(), ProcessStart: plan.started, ProviderArgv: plan.argv, IdentityProof: plan.identityProof}
		var r *Refusal
		if errors.As(err, &r) {
			state.Exec = &launch.Exec{At: o.Now().UTC(), Outcome: "refused", ReasonCode: r.Code, Detail: r.Detail}
		}
		if b, merr := launch.Marshal(state); merr == nil {
			if werr := plan.published.writeFile(launch.FileName, b); werr != nil && err == nil {
				err = werr
			}
		}
		plan.published.close()
	}
	if err != nil {
		return err
	}
	return o.Exec(plan.provider, plan.argv, plan.env)
}

type execPlan struct {
	rec           launch.Record
	provider      string
	env           []string
	identityProof string
	started       string   // procinfo.StartToken of this process
	argv          []string // the provider argv exec'd (Codex gains the sandbox pins)
	published     *anchor
}

func prepare(o Options) (execPlan, error) {
	var plan execPlan
	me, rootPath := Getenv(o.Env, "AM_ME"), Getenv(o.Env, "AM_ROOT")
	if !handlePattern.MatchString(o.Seat) {
		return plan, refusef("seat_invalid", "--seat %q is not a valid handle", o.Seat)
	}
	if me != o.Seat {
		return plan, refusef("identity_mismatch", "--seat %q disagrees with AM_ME=%q", o.Seat, me)
	}
	if len(o.Argv) == 0 {
		return plan, refusef("argv_missing", "no provider argv after --seat")
	}
	root, err := openRoot(rootPath, o.UID)
	if err != nil {
		return plan, err
	}
	defer root.close()

	staged, err := root.walk("extensions/amq-squad/launch/"+o.Seat, false)
	if err != nil {
		return plan, asRefusal("record_missing", err)
	}
	defer staged.close()
	raw, err := staged.readFile(launch.FileName, maxFile)
	if err != nil {
		return plan, asRefusal("record_missing", err)
	}
	rec, err := launch.Decode(raw)
	if err != nil {
		return plan, refusef("record_invalid", "staged launch record: %v", err)
	}
	if err := checkIdentity(rec, o); err != nil {
		return plan, err
	}
	// Right after the identity check, open the seat's extension directory first, so every later refusal is
	// published as the seat's exec record (not left as a missing record).
	ext, err := root.walk("agents/"+o.Seat, false)
	if err != nil {
		return plan, asRefusal("mailbox_missing", err)
	}
	pub, err := ext.walk("extensions/"+launch.LayerName, true)
	ext.close()
	if err != nil {
		return plan, err
	}
	plan.published = pub
	plan.rec = rec
	plan.rec.Proof = nil // the published copy never carries the token
	role, err := staged.readFile(launch.RoleFileName, maxFile)
	if err != nil {
		return plan, asRefusal("role_missing", err)
	}
	if sum := sha(role); sum != rec.RoleSHA256 {
		return plan, refusef("role_mismatch", "role.md sha256 %s does not match the launch record's %s", sum, rec.RoleSHA256)
	}
	rules, err := readRules(rec)
	if err != nil {
		return plan, err
	}
	if err := ValidateNeeds(rec.EnvNeeds, rec.CredNeeds); err != nil {
		return plan, err
	}
	plan.provider, err = providerPath(rec.Runtime, o.Argv[0], o.LookPath)
	if err != nil {
		return plan, err
	}
	plan.argv = o.Argv
	if rec.Runtime == "codex" {
		if err := CheckCodexSandbox(o.Argv); err != nil {
			return plan, err
		}
		plan.argv = codexSandboxPins(o.Argv, slices.Contains(rec.EnvNeeds, CodexNetworkNeed))
	}
	// A seat whose start stamp cannot be read could never be closed with a
	// confirmed identity: refuse it (the identity_incomplete class).
	var ok bool
	if plan.started, ok = o.StartToken(os.Getpid()); !ok {
		return plan, refusef(IdentityIncomplete, "the process start stamp of pid %d is unreadable; a close could never confirm this seat", os.Getpid())
	}
	// The provider gets 0400 snapshots of the verified bytes in the seat's
	// extension directory, never the mutable source paths.
	rolePath := filepath.Join(pub.path, launch.RoleFileName)
	rec.RolePath = rolePath
	plan.rec = rec
	plan.rec.Proof = nil
	if err := pub.writeFileMode(launch.RoleFileName, role, 0o400); err != nil {
		return plan, err
	}
	rulesSnapshot := ""
	if rules != nil {
		rulesSnapshot = filepath.Join(pub.path, rulesSnapshotName)
		if err := pub.writeFileMode(rulesSnapshotName, rules, 0o400); err != nil {
			return plan, err
		}
	}

	if rec.Runtime == "pi" && rec.Resume {
		ledger := filepath.Join(rootPath, "extensions", "amq-squad", "ledger", o.Seat+".jsonl")
		if err := CheckPiResume(ledger, piAgentDir(rec, o.Home), firstNonEmpty(rec.Worktree, rec.CWD), o.Argv); err != nil {
			return plan, err
		}
	}
	if err := writePreflight(o.Home); err != nil {
		return plan, err
	}
	plan.env, _ = SeatEnv(o.Env, rec.EnvNeeds, rec.CredNeeds, overlay(rec, rolePath, rulesSnapshot, o.Home))
	if o.MutateEnv != nil {
		plan.env = o.MutateEnv(plan.env)
	}
	if plan.identityProof, err = identityProof(o, rec, plan.env); err != nil {
		return plan, err
	}
	if o.BeforeRecheck != nil {
		o.BeforeRecheck()
	}
	if err := recheck(rec, staged, pub, rules != nil); err != nil {
		return plan, err
	}
	return plan, sendProof(o, rec, plan.started)
}

// sendProof is the last step before exec (item 4, lead ruling): seat-exec
// sends the launcher its proof over the launch's socket (the ticket from
// the staged record, the identity amq env proved, which equals the
// record's, this pid and its raw start stamp) and execs only on the
// launcher's OK. The token never enters the provider's environment and the
// published record carries no token.
func sendProof(o Options, rec launch.Record, started string) error {
	t := rec.Proof
	if t == nil || t.Dir == "" || t.Name == "" || t.Token == "" {
		return refusef("proof_ticket_missing", "the staged launch record carries no proof ticket")
	}
	switch t.Token {
	case launch.TokenConsumed:
		return refusef("proof_ticket_consumed", "the seat's proof token was already used")
	case launch.TokenExpired:
		return refusef("proof_expired", "the seat's proof token expired")
	}
	msg := launch.ProofMessage{LaunchID: rec.LaunchID, RequestID: rec.RequestID, Handle: rec.Handle, Token: t.Token,
		Root: rec.Root, Me: rec.Handle, Session: rec.Session, PID: os.Getpid(), Start: started}
	reply, err := o.SendProof(*t, msg)
	if err != nil {
		return refusef("proof_unacknowledged", "the launcher did not take the proof: %v", err)
	}
	if !reply.OK {
		return refusef(firstNonEmpty(reply.Code, "proof_refused"), "the launcher refused the proof: %s", reply.Detail)
	}
	return nil
}

const rulesSnapshotName = "rules.md"

func readRules(rec launch.Record) ([]byte, error) {
	if rec.RulesPath == "" {
		return nil, nil
	}
	rules, err := os.ReadFile(rec.RulesPath)
	if err != nil {
		return nil, refusef("rules_missing", "rules file: %v", err)
	}
	if sum := sha(rules); sum != rec.RulesSHA256 {
		return nil, refusef("rules_mismatch", "rules sha256 %s does not match the launch record's %s", sum, rec.RulesSHA256)
	}
	return rules, nil
}

// recheck re-hashes role and rules immediately before exec: the staged role,
// the published role snapshot, the rules source and its snapshot must all
// still match the digests recorded at launch (the identity proof can take
// seconds, and a file changed in that window must not reach the provider).
func recheck(rec launch.Record, staged, pub *anchor, hasRules bool) error {
	for _, f := range []struct {
		dir  *anchor
		name string
	}{{staged, launch.RoleFileName}, {pub, launch.RoleFileName}} {
		if b, err := f.dir.readFile(f.name, maxFile); err != nil || sha(b) != rec.RoleSHA256 {
			return refusef("role_mismatch", "%s changed before exec", filepath.Join(f.dir.path, f.name))
		}
	}
	if !hasRules {
		return nil
	}
	if _, err := readRules(rec); err != nil {
		return err
	}
	if b, err := pub.readFile(rulesSnapshotName, maxFile); err != nil || sha(b) != rec.RulesSHA256 {
		return refusef("rules_mismatch", "the rules snapshot changed before exec")
	}
	return nil
}

// CheckCodexSandbox: for a Codex seat the sandbox is the enforcement of the
// global-config denial (amendment 28; workspace-write refuses writes outside
// the workspace), so a Codex argv without --sandbox workspace-write (or
// read-only), or with a bypass, is refused.
func CheckCodexSandbox(argv []string) error {
	mode := ""
	for i, a := range argv {
		if a == "--" {
			break // what follows is the prompt, not options
		}
		switch {
		case a == "--dangerously-bypass-approvals-and-sandbox" || a == "--yolo" || strings.HasPrefix(a, "--full-auto"):
			return refusef("codex_sandbox_missing", "Codex argv carries %s; seat-exec requires the workspace-write sandbox", a)
		case (a == "--sandbox" || a == "-s") && i+1 < len(argv):
			mode = argv[i+1]
		case strings.HasPrefix(a, "--sandbox="):
			mode = strings.TrimPrefix(a, "--sandbox=")
		case strings.HasPrefix(a, "-s="):
			mode = strings.TrimPrefix(a, "-s=")
		}
		// A config override of the sandbox, in the short or the long form,
		// spaced or joined with "=".
		for _, flag := range []string{"-c", "--config"} {
			value, joined := strings.CutPrefix(a, flag+"=")
			if !joined && a == flag && i+1 < len(argv) {
				value = argv[i+1]
			}
			if v := strings.TrimSpace(value); strings.HasPrefix(v, "sandbox_mode") || strings.HasPrefix(v, "sandbox_workspace_write") {
				return refusef("codex_sandbox_missing", "Codex argv overrides the sandbox with %s %s", flag, value)
			}
		}
	}
	if mode != "workspace-write" && mode != "read-only" {
		return refusef("codex_sandbox_missing", "Codex argv has sandbox %q; seat-exec requires --sandbox workspace-write (or read-only)", mode)
	}
	return nil
}

// checkIdentity: the record names this seat and root, carries a session and
// base root, and any identity variable already set agrees with it.
func checkIdentity(rec launch.Record, o Options) error {
	want := map[string]string{"AM_ME": rec.Handle, "AM_ROOT": rec.Root, "AM_BASE_ROOT": rec.BaseRoot, "AM_SESSION": rec.Session}
	for _, name := range IdentityVars {
		if want[name] == "" {
			return refusef(IdentityIncomplete, "the launch record has no value for %s (R52: the launch sets the complete identity; seat-exec fails closed whatever amq would accept)", name)
		}
		if got := Getenv(o.Env, name); got != "" && got != want[name] {
			return refusef("identity_mismatch", "%s=%q disagrees with the launch record's %q", name, got, want[name])
		}
	}
	return nil
}

func overlay(rec launch.Record, rolePath, rulesSnapshot, home string) map[string]string {
	m := map[string]string{
		"AM_ME": rec.Handle, "AM_ROOT": rec.Root, "AM_BASE_ROOT": rec.BaseRoot, "AM_SESSION": rec.Session,
		"AMQ_SQUAD_SEAT": rec.Handle, "AMQ_SQUAD_ROLE_PATH": rolePath, "AMQ_SQUAD_ROLE_SHA256": rec.RoleSHA256,
		"AMQ_SQUAD_BRANCH": rec.Branch, "AMQ_SQUAD_REQUEST_ID": rec.RequestID, "AMQ_SQUAD_RULES_PATH": rulesSnapshot, "AMQ_SQUAD_RULES_SHA256": rec.RulesSHA256,
	}
	if rec.Runtime == "pi" {
		m["PI_OFFLINE"], m["PI_SKIP_VERSION_CHECK"], m["PI_TELEMETRY"] = "1", "1", "0"
		m["PI_CODING_AGENT_DIR"] = piAgentDir(rec, home)
	}
	return m
}

func piAgentDir(rec launch.Record, home string) string {
	if rec.PiAgentDir != "" {
		return rec.PiAgentDir
	}
	return filepath.Join(home, ".amq-squad", "pi-agent", rec.Handle)
}

// providerPath resolves argv[0] and refuses a provider that is not the
// recorded runtime.
func providerPath(runtime, argv0 string, lookPath func(string) (string, error)) (string, error) {
	if base := filepath.Base(argv0); base != runtime {
		return "", refusef("runtime_mismatch", "provider %q is not the recorded runtime %q", base, runtime)
	}
	path, err := lookPath(argv0)
	if err != nil {
		return "", refusef("provider_missing", "%v", err)
	}
	return path, nil
}

// identityProof runs AMQ's own identity check with the seat's final
// environment (R52 as amended, amendment 28: "one read-only identity proof"):
// `amq env --me H --json` resolves the root and session pin exactly as a
// drain would and refuses a mismatched pin (exit 5, measured on 0.107.0),
// but moves no message, so mail arriving during the proof is never lost.
// (`amq list` only warns on a mismatched pin, so it is not a proof.) The
// resolved root, handle and session must equal the record's.
func identityProof(o Options, rec launch.Record, env []string) (string, error) {
	amq := rec.AMQPath
	if amq == "" {
		amq = "amq"
	}
	args := []string{"env", "--me", rec.Handle, "--json"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := o.RunAMQ(ctx, amq, args, env, firstNonEmpty(rec.Worktree, rec.CWD))
	if err != nil {
		return "", refusef("identity_proof_failed", "amq env failed: %v: %s", err, trimOut(out))
	}
	var got struct {
		Root        string `json:"root"`
		Me          string `json:"me"`
		SessionName string `json:"session_name"`
	}
	if err := json.Unmarshal(firstObject(out), &got); err != nil {
		return "", refusef("identity_proof_failed", "amq env returned no identity: %s", trimOut(out))
	}
	if rec.Session != "" && got.SessionName == "" {
		return "", refusef("identity_proof_failed", "amq env resolved no session; the launch record says %q", rec.Session)
	}
	if filepath.Clean(got.Root) != filepath.Clean(rec.Root) || got.Me != rec.Handle || got.SessionName != rec.Session {
		return "", refusef("identity_proof_failed", "amq resolved root %q, handle %q, session %q; the launch record says %q, %q, %q", got.Root, got.Me, got.SessionName, rec.Root, rec.Handle, rec.Session)
	}
	return "amq env", nil
}

// firstObject returns the first JSON object in b (amq may print a notice
// line before it).
func firstObject(b []byte) []byte {
	if i := strings.IndexByte(string(b), '{'); i >= 0 {
		return b[i:]
	}
	return b
}

func execProvider(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func asRefusal(code string, err error) error {
	var r *Refusal
	if errors.As(err, &r) {
		return err
	}
	return refusef(code, "%v", err)
}

// CodexNetworkNeed is the env_needs entry with which a Codex seat keeps the
// sandbox's network access (the registry entry declares it).
const CodexNetworkNeed = "CODEX_NETWORK_ACCESS"

// codexSandboxPins puts the workspace-write sandbox's own settings on the
// command line, right after the executable, so a trusted project config
// cannot widen them (Codex gives -c precedence over config.toml; fix 3 item
// 5): no extra writable roots, and no network unless the seat declared it.
// This is the one place seat-exec adds provider arguments (lead ruling).
func codexSandboxPins(argv []string, network bool) []string {
	pins := []string{"-c", "sandbox_workspace_write.writable_roots=[]", "-c", "sandbox_workspace_write.network_access=" + strconv.FormatBool(network)}
	out := append([]string{argv[0]}, pins...)
	return append(out, argv[1:]...)
}

// DialProof is seat-exec's proof sender (exported for the launcher's tests): the socket must be
// a socket owned by this user in a directory no one else can write.
func DialProof(uid int) func(t launch.ProofTicket, msg launch.ProofMessage) (launch.ProofReply, error) {
	return func(t launch.ProofTicket, msg launch.ProofMessage) (launch.ProofReply, error) {
		var reply launch.ProofReply
		// The directory through the anchored walk (no symlink, owner and
		// mode checked), the socket stat'ed through its descriptor and
		// compared with the ticket's device and inode (fix 3b (a)).
		dir, err := openRoot(t.Dir, uid)
		if err != nil {
			return reply, err
		}
		defer dir.close()
		var st unix.Stat_t
		if err := unix.Fstatat(dir.fd, t.Name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return reply, fmt.Errorf("stat %s: %w", t.Name, err)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFSOCK || int(st.Uid) != uid || uint64(st.Dev) != t.Dev || uint64(st.Ino) != t.Ino {
			return reply, fmt.Errorf("%s is not the launcher's socket named in the ticket", filepath.Join(t.Dir, t.Name))
		}
		addr := filepath.Join(t.Dir, t.Name)
		if runtime.GOOS == "linux" {
			addr = "/proc/self/fd/" + strconv.Itoa(dir.fd) + "/" + t.Name
		}
		conn, err := net.DialTimeout("unix", addr, 5*time.Second)
		if err != nil {
			return reply, err
		}
		defer conn.Close()
		// The peer must be the launcher the ticket names (pid and raw start
		// stamp), so a socket rebound by another process is never trusted.
		uc, _ := conn.(*net.UnixConn)
		peer, known := 0, false
		if uc != nil {
			if raw, err := uc.SyscallConn(); err == nil {
				raw.Control(func(fd uintptr) { peer, known = procinfo.SocketPeerPID(int(fd)) })
			}
		}
		if !known {
			return reply, fmt.Errorf("the kernel did not report the socket's peer")
		}
		if start, ok := procinfo.StartToken(peer); peer != t.LauncherPID || !ok || start != t.LauncherStart {
			return reply, fmt.Errorf("the socket's peer pid %d is not the launcher the ticket names (pid %d)", peer, t.LauncherPID)
		}
		conn.SetDeadline(time.Now().Add(30 * time.Second))
		b, _ := json.Marshal(msg)
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return reply, err
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return reply, err
		}
		return reply, json.Unmarshal(line, &reply)
	}
}

func sendProofDefault(uid int) func(launch.ProofTicket, launch.ProofMessage) (launch.ProofReply, error) {
	return DialProof(uid)
}
