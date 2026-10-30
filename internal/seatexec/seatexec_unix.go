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
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/registry"
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
		state.Exec = &launch.Exec{At: o.Now().UTC(), Outcome: "exec", PID: os.Getpid(), ProcessStart: plan.started, ProviderArgv: plan.providerArgv, LauncherArgv: plan.launcherArgv, IdentityProof: plan.identityProof}
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
	argv          []string // the argv exec'd
	providerArgv  []string // the provider argv the adapter composed (recorded)
	launcherArgv  []string // a pi seat's embedded-runtime launcher argv (recorded)
	published     *anchor
	token         string // a Claude seat's credential (never logged or recorded)
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
	if rec.Runtime == "claude" {
		// A Claude seat runs the registry's claude pin, the bytes the launch
		// probed (the narrow pass's HIGH): argv[0] must be the pinned path
		// and hash to the pin; checked again immediately before exec.
		if base := filepath.Base(o.Argv[0]); base != rec.Runtime {
			err = refusef("runtime_mismatch", "provider %q is not the recorded runtime %q", base, rec.Runtime)
		} else {
			plan.provider, err = pinnedProvider(o.Home, o.Argv[0], rec.ClaudePin)
		}
	} else if rec.Runtime == "pi" {
		// A pi seat runs the verified embedded runtime, never a pi from PATH
		// (R2): argv[0] must be this binary's pi launch shim, the executable
		// AMQ's pi adapter probed and planned (brief 5-amq-A).
		plan.provider, err = piShimProvider(o, rec)
	} else {
		plan.provider, err = providerPath(rec.Runtime, o.Argv[0], o.LookPath)
	}
	if err != nil {
		return plan, err
	}
	plan.argv, plan.providerArgv = o.Argv, o.Argv
	if rec.Runtime == "pi" {
		plan.argv = PiLauncherArgv(o.Self, rec, o.Argv)
		plan.launcherArgv = plan.argv
	}
	if rec.Runtime == "codex" {
		if err := CheckCodexSandbox(o.Argv); err != nil {
			return plan, err
		}
		// The pins travel in the launch request (AMQ v0.110.0 admits them,
		// upstream ask 17): seat-exec checks the admitted argv carries them
		// as the record requires and adds no provider argument.
		if err := CheckCodexSandboxPins(o.Argv, slices.Contains(rec.EnvNeeds, CodexNetworkNeed)); err != nil {
			return plan, err
		}
	}
	// A seat whose start stamp cannot be read could never be closed with a
	// confirmed identity: refuse it (the identity_incomplete class).
	var ok bool
	if plan.started, ok = o.StartToken(os.Getpid()); !ok {
		return plan, refusef(IdentityIncomplete, "the process start stamp of pid %d is unreadable; a close could never confirm this seat", os.Getpid())
	}
	if rec.Runtime == "claude" {
		if err := CheckClaudePermission(rec.PermissionMode, o.Argv[1:]); err != nil {
			return plan, err
		}
		if err := CheckClaudeHooks(o.Argv[1:]); err != nil {
			return plan, err
		}
		// The seat's own CLAUDE_CONFIG_DIR, written by the core for this
		// launch: the hook plugin and the permission mode as one unit.
		dir := claudeseat.Dir(rec.Root, rec.Handle, rec.LaunchID)
		if code, detail := claudeseat.Check(dir, o.UID, rec.ClaudeConfig, rec.PermissionMode); code != "" {
			return plan, refusef(code, "%s", detail)
		}
		// The launch admitted an AMQ wake for this seat from a scratch copy
		// of its config dir (3.0.1); the real dir must carry the guard deny
		// of AskUserQuestion and ExitPlanMode too, or a doorbell could
		// answer a dialog.
		if rec.WakeRequired {
			if ok, why := ClaudeDialogGuardWired(dir); !ok {
				return plan, refusef(ClaudeDialogGuardMissing, "the launch admitted an AMQ wake for this seat, but its config dir does not run the guard deny of %s: %s", strings.Join(SeatDeniedDialogTools, " and "), why)
			}
		}
		// The worktree's settings files still merge into the seat's config.
		if rules := claudeseat.WorktreeAllowRules(firstNonEmpty(rec.Worktree, rec.CWD)); len(rules) > 0 && rec.AllowRules != "permit" {
			return plan, refusef(claudeseat.AllowRulesPresent, "the worktree's Claude settings carry permission allow rules, which would run a call unguarded when a hook fails open: %s", strings.Join(rules, "; "))
		}
		tok, why := claudeseat.ReadToken(o.Home)
		if why != "" {
			return plan, refusef(claudeseat.TokenInsecure, "%s", why)
		}
		plan.token = tok
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
	// The Claude token comes only from the owner's token file and only for a
	// Claude seat: a parent value is dropped whatever the runtime declares.
	plan.env, _ = SeatEnv(dropVar(o.Env, claudeseat.TokenVar), rec.EnvNeeds, rec.CredNeeds, overlay(rec, rolePath, rulesSnapshot, o.Home, o.Self, plan.token))
	if o.MutateEnv != nil {
		plan.env = o.MutateEnv(plan.env)
	}
	// The proof is a harness subprocess: it gets the seat's identity, never
	// a credential (the token, a provider key the runtime declared).
	if plan.identityProof, err = identityProof(o, rec, redact.StripEnv(plan.env)); err != nil {
		return plan, err
	}
	if o.BeforeRecheck != nil {
		o.BeforeRecheck()
	}
	if err := recheck(rec, staged, pub, rules != nil, o.UID); err != nil {
		return plan, err
	}
	if err := sendProof(o, rec, plan.started); err != nil {
		return plan, err
	}
	// P1-10: the config dir is hashed through descriptors once more as the
	// very last step before exec (after the proof's round trip).
	if o.BeforeFinalCheck != nil {
		o.BeforeFinalCheck()
	}
	if rec.Runtime == "claude" {
		dir := claudeseat.Dir(rec.Root, rec.Handle, rec.LaunchID)
		if code, detail := claudeseat.Check(dir, o.UID, rec.ClaudeConfig, rec.PermissionMode); code != "" {
			return plan, refusef(code, "the seat's Claude config dir changed before exec: %s", detail)
		}
		// The pin re-opened and re-hashed as the last step: a file replaced
		// after the launch's probe is refused (the window to execve is the
		// same-UID limit, as for the pinned gh).
		if _, err := pinnedProvider(o.Home, o.Argv[0], rec.ClaudePin); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// ToolUnpinned refuses a Claude seat whose argv[0] is not the registry's
// claude pin, or whose pinned file does not hash to the pin.
const ToolUnpinned = "tool_unpinned"

// PinChangedSinceProbe refuses a Claude seat whose registry claude pin is no
// longer the digest the launch probed (phase 5 release prep).
const PinChangedSinceProbe = "pin_changed_since_probe"

// ClaudePinMismatch refuses a Claude seat whose pinned claude, at the pin's
// path, does not hash to the registry pin (3.0.1 t6, reviewer addition):
// the seat's config checks are facts about the pinned 2.1.314 binary.
const ClaudePinMismatch = "claude_pin_mismatch"

// pinnedProvider is a Claude seat's executable: argv[0] must be the path the
// registry pins for claude, opened through the anchored walk and hashed to
// the pin's sha256 (registry.OpenPinned).
//
// probed is the digest of the claude pin the launch probed, from the staged
// record: a registry whose claude pin differs now (re-pinned between the
// launch and this exec, to another file or another digest) is refused
// pin_changed_since_probe before the pin's own file is checked, because the
// probe's verdict about the permission mode belongs to the probed bytes.
func pinnedProvider(home, argv0, probed string) (string, error) {
	reg, err := registry.Read(registry.Path(home))
	if err != nil {
		return "", refusef(ToolUnpinned, "the registry: %v", err)
	}
	pin, ok := reg.PinFor("claude")
	if !ok {
		return "", refusef(ToolUnpinned, "the registry pins no claude")
	}
	if probed == "" || pin.SHA256 != probed {
		return "", refusef(PinChangedSinceProbe, "the registry's claude pin is not the one the launch probed (the staged record carries digest %q, the pin is %q)", probed, pin.SHA256)
	}
	if filepath.Clean(argv0) != pin.Path {
		return "", refusef(ToolUnpinned, "the provider %s is not the pinned claude %s", argv0, pin.Path)
	}
	// The bytes about to run are the pin's (registry.OpenPinned, the check
	// doctor's pin rows run). claudeseat's bookkeeping sets and permission-
	// reader walk are facts about the pinned binary, so a binary that
	// hashes otherwise (an upgrade in place) fails closed here, before any
	// config check, until the owner re-pins and the sets are re-walked.
	tool, code, detail := registry.OpenPinned("claude", pin.Path, pin.SHA256)
	if tool == nil {
		if code == registry.PinMismatch {
			return "", refusef(ClaudePinMismatch, "%s", detail)
		}
		return "", refusef(ToolUnpinned, "%s: %s", code, detail)
	}
	tool.Close()
	return pin.Path, nil
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
// A Claude seat's config dir is checked again too: its digest, mode and
// owner, the plugin and the permission mode (milestone B first pass P1).
func recheck(rec launch.Record, staged, pub *anchor, hasRules bool, uid int) error {
	if rec.Runtime == "claude" {
		dir := claudeseat.Dir(rec.Root, rec.Handle, rec.LaunchID)
		if code, detail := claudeseat.Check(dir, uid, rec.ClaudeConfig, rec.PermissionMode); code != "" {
			return refusef(code, "the seat's Claude config dir changed before exec: %s", detail)
		}
	}
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
// read-only), or with a bypass, is refused. 3-2 adds the approval policy
// beside it (plan 7.3): a Codex argv without --ask-for-approval on-request,
// or with an approval_policy override, is refused (codex_approval_missing).
func CheckCodexSandbox(argv []string) error {
	mode, approval := "", ""
	for _, o := range codexOptions(argv) {
		switch o.name {
		case "--dangerously-bypass-approvals-and-sandbox", "--yolo", "--full-auto":
			return refusef("codex_sandbox_missing", "Codex argv carries %s; seat-exec requires the workspace-write sandbox", o.raw)
		case "--sandbox":
			mode = o.value
		case "--ask-for-approval":
			approval = o.value
		case "--config":
			// A config override of the sandbox or the approval policy, in
			// every form Codex admits.
			// The two sandbox pins are the one admitted sandbox_workspace_write
			// form, and only with a value that narrows (CheckCodexSandboxPins
			// checks the record's value).
			if v := strings.TrimSpace(o.value); strings.HasPrefix(v, "sandbox_mode") || (strings.HasPrefix(v, "sandbox_workspace_write") && !narrowingPin(v)) {
				return refusef("codex_sandbox_missing", "Codex argv overrides the sandbox with %s", o.raw)
			} else if strings.HasPrefix(v, "approval_policy") {
				return refusef("codex_approval_missing", "Codex argv overrides the approval policy with %s", o.raw)
			}
		}
	}
	if mode != "workspace-write" && mode != "read-only" {
		return refusef("codex_sandbox_missing", "Codex argv has sandbox %q; seat-exec requires --sandbox workspace-write (or read-only)", mode)
	}
	if approval != "on-request" {
		return refusef("codex_approval_missing", "Codex argv has approval policy %q; seat-exec requires --ask-for-approval on-request", approval)
	}
	return nil
}

// ownGroup puts the identity proof's amq in its own process group, and a
// context end kills that whole group (P2-5).
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		killGroup(cmd)
		return nil
	}
}

// killGroup kills the proof's process group: amq and any descendant still
// holding its output.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// codexOption is one option occurrence in a Codex argv, named in its long
// form.
type codexOption struct{ name, value, raw string }

// codexValueOptions are the Codex value options the sandbox check reads,
// short letter to long name.
var codexValueOptions = map[byte]string{'c': "--config", 's': "--sandbox", 'a': "--ask-for-approval"}

// codexOptions scans a Codex argv as Codex's parser reads it, up to "--"
// (what follows is the prompt). A value option is read in every form Codex
// admits: spaced (-c X, --config X), joined with "=" (-c=X, --config=X)
// and, for a short option, attached (-cX). A bypass flag is returned by
// name (any --full-auto prefix as --full-auto).
func codexOptions(argv []string) []codexOption {
	var out []codexOption
	for i, a := range argv {
		if a == "--" {
			break
		}
		switch {
		case a == "--dangerously-bypass-approvals-and-sandbox" || a == "--yolo":
			out = append(out, codexOption{name: a, raw: a})
		case strings.HasPrefix(a, "--full-auto"):
			out = append(out, codexOption{name: "--full-auto", raw: a})
		case strings.HasPrefix(a, "--"):
			for _, long := range codexValueOptions {
				if v, ok := strings.CutPrefix(a, long+"="); ok {
					out = append(out, codexOption{name: long, value: v, raw: a})
				} else if a == long && i+1 < len(argv) {
					out = append(out, codexOption{name: long, value: argv[i+1], raw: a + " " + argv[i+1]})
				}
			}
		case len(a) >= 2 && a[0] == '-':
			long, ok := codexValueOptions[a[1]]
			switch {
			case !ok:
			case len(a) == 2 && i+1 < len(argv):
				out = append(out, codexOption{name: long, value: argv[i+1], raw: a + " " + argv[i+1]})
			case len(a) > 2 && a[2] == '=':
				out = append(out, codexOption{name: long, value: a[3:], raw: a})
			case len(a) > 2: // the attached form, -cX
				out = append(out, codexOption{name: long, value: a[2:], raw: a})
			}
		}
	}
	return out
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

func overlay(rec launch.Record, rolePath, rulesSnapshot, home, self, token string) map[string]string {
	m := map[string]string{
		"AM_ME": rec.Handle, "AM_ROOT": rec.Root, "AM_BASE_ROOT": rec.BaseRoot, "AM_SESSION": rec.Session,
		"AMQ_SQUAD_SEAT": rec.Handle, "AMQ_SQUAD_ROLE_PATH": rolePath, "AMQ_SQUAD_ROLE_SHA256": rec.RoleSHA256,
		"AMQ_SQUAD_BRANCH": rec.Branch, "AMQ_SQUAD_REQUEST_ID": rec.RequestID, "AMQ_SQUAD_RULES_PATH": rulesSnapshot, "AMQ_SQUAD_RULES_SHA256": rec.RulesSHA256,
		// The seat's hooks call the binary that launched it (4-2: the hook
		// script honors AMQ_SQUAD_BIN, else finds amq-squad on PATH).
		"AMQ_SQUAD_BIN": self,
	}
	if rec.Runtime == "claude" {
		m["CLAUDE_CONFIG_DIR"] = claudeseat.Dir(rec.Root, rec.Handle, rec.LaunchID)
		// The seat's credential, from the owner's token file only; a value from
		// the parent environment never survives (the overlay replaces it).
		m[claudeseat.TokenVar] = token
		// The registry's Claude seat environment (3.0.1 t6, the reviewer's
		// ruling): nonessential traffic off, so the flag client never writes
		// its cache into the seat's config dir, where it would be drift.
		for _, kv := range registry.ClaudeSeatEnv {
			if k, v, ok := strings.Cut(kv, "="); ok {
				m[k] = v
			}
		}
	}
	if rec.Runtime == "pi" {
		// The launcher (amq-squad pi run) starts the seat's pi with the
		// launch record's amq; it forces the rest of the pi environment.
		m["AMQ_SQUAD_AMQ"] = rec.AMQPath
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
	if err != nil && (ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay)) {
		// P2-5: amq, or a descendant holding its output, outlived the bound
		// and its group was killed. A timeout is a refusal, never a pass.
		return "", refusef("identity_proof_timeout", "amq env did not finish within the proof bound: %v", err)
	}
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

// narrowingPin reports a sandbox_workspace_write override that is one of the
// two pins with a value the pin admits: writable_roots=[] and
// network_access=true or false. Any other key or value widens or replaces
// the sandbox and is refused as before (amendment 28).
func narrowingPin(v string) bool {
	return v == CodexWritableRootsPin+"=[]" || v == CodexNetworkAccessPin+"=false" || v == CodexNetworkAccessPin+"=true"
}

// CheckCodexSandboxPins refuses a Codex argv that does not carry each of the
// two sandbox pins exactly once, with the value the launch record requires:
// writable_roots=[], and network_access=true only when the seat declared
// CODEX_NETWORK_ACCESS (sandbox_pins_missing). The pins are provider
// arguments in the admitted launch request, so a missing pin means the argv
// is not the one the launch compiled.
func CheckCodexSandboxPins(argv []string, network bool) error {
	want := map[string]string{CodexWritableRootsPin: "[]", CodexNetworkAccessPin: strconv.FormatBool(network)}
	seen := map[string]int{}
	for _, o := range codexOptions(argv) {
		if o.name != "--config" {
			continue
		}
		key, value, _ := strings.Cut(strings.TrimSpace(o.value), "=")
		if _, pin := want[key]; !pin {
			continue
		}
		seen[key]++
		if value != want[key] {
			return refusef(SandboxPinsMissing, "Codex argv sets %s=%s; the launch record requires %s=%s", key, value, key, want[key])
		}
	}
	for _, key := range []string{CodexWritableRootsPin, CodexNetworkAccessPin} {
		if seen[key] != 1 {
			return refusef(SandboxPinsMissing, "Codex argv carries %s %d times; the admitted launch request carries it once as %s=%s", key, seen[key], key, want[key])
		}
	}
	return nil
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
