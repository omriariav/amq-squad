//go:build unix

package seatexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

const maxFile = 4 << 20

// Run is `amq-squad seat-exec --seat <handle> <provider argv...>` (plan 4.3,
// R4, R52). It verifies the AMQ root from AM_ROOT without following a
// symlink, reads the staged launch record and role.md, checks both sha256
// sums (and the rules file's), publishes the record and role into the seat's
// extension directory, builds the scrubbed environment with the identity
// from the record only, proves one amq drain, and execs the provider in
// place. It adds no provider flag. On success it does not return.
func Run(o Options) error {
	o = o.withDefaults()
	plan, err := prepare(o)
	if plan.published != nil {
		state := plan.rec
		state.Exec = &launch.Exec{At: o.Now().UTC(), Outcome: "exec", PID: os.Getpid(), ProviderArgv: o.Argv, DrainProof: plan.drainProof}
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
	return o.Exec(plan.provider, o.Argv, plan.env)
}

type execPlan struct {
	rec        launch.Record
	provider   string
	env        []string
	drainProof string
	published  *anchor
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
	role, err := staged.readFile(launch.RoleFileName, maxFile)
	if err != nil {
		return plan, asRefusal("role_missing", err)
	}
	if sum := sha(role); sum != rec.RoleSHA256 {
		return plan, refusef("role_mismatch", "role.md sha256 %s does not match the launch record's %s", sum, rec.RoleSHA256)
	}
	if rec.RulesPath != "" {
		rules, err := os.ReadFile(rec.RulesPath)
		if err != nil {
			return plan, refusef("rules_missing", "rules file: %v", err)
		}
		if sum := sha(rules); sum != rec.RulesSHA256 {
			return plan, refusef("rules_mismatch", "rules sha256 %s does not match the launch record's %s", sum, rec.RulesSHA256)
		}
	}
	plan.provider, err = providerPath(rec.Runtime, o.Argv[0], o.LookPath)
	if err != nil {
		return plan, err
	}

	// Publish role and record into the seat mailbox (AMQ created it).
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
	rolePath := filepath.Join(pub.path, launch.RoleFileName)
	rec.RolePath = rolePath
	plan.rec = rec
	if err := pub.writeFile(launch.RoleFileName, role); err != nil {
		return plan, err
	}
	if back, err := pub.readFile(launch.RoleFileName, maxFile); err != nil || sha(back) != rec.RoleSHA256 {
		return plan, refusef("role_mismatch", "published role.md does not match the launch record's sha256")
	}

	if rec.Runtime == "pi" && rec.Resume {
		ledger := filepath.Join(rootPath, "extensions", "amq-squad", "ledger", o.Seat+".jsonl")
		if err := CheckPiResume(ledger, piAgentDir(rec, o.Home), o.Argv); err != nil {
			return plan, err
		}
	}
	if err := writePreflight(o.Home); err != nil {
		return plan, err
	}
	plan.env, _ = SeatEnv(o.Env, rec.EnvNeeds, overlay(rec, rolePath, o.Home))
	if o.MutateEnv != nil {
		plan.env = o.MutateEnv(plan.env)
	}
	plan.drainProof, err = drainProof(o, rec, plan.env)
	return plan, err
}

// checkIdentity: the record names this seat and root, carries a session and
// base root, and any identity variable already set agrees with it.
func checkIdentity(rec launch.Record, o Options) error {
	want := map[string]string{"AM_ME": rec.Handle, "AM_ROOT": rec.Root, "AM_BASE_ROOT": rec.BaseRoot, "AM_SESSION": rec.Session}
	for _, name := range IdentityVars {
		if want[name] == "" {
			return refusef("record_incomplete", "the launch record has no value for %s (R52: the launch sets the complete identity)", name)
		}
		if got := Getenv(o.Env, name); got != "" && got != want[name] {
			return refusef("identity_mismatch", "%s=%q disagrees with the launch record's %q", name, got, want[name])
		}
	}
	return nil
}

func overlay(rec launch.Record, rolePath, home string) map[string]string {
	m := map[string]string{
		"AM_ME": rec.Handle, "AM_ROOT": rec.Root, "AM_BASE_ROOT": rec.BaseRoot, "AM_SESSION": rec.Session,
		"AMQ_SQUAD_SEAT": rec.Handle, "AMQ_SQUAD_ROLE_PATH": rolePath, "AMQ_SQUAD_ROLE_SHA256": rec.RoleSHA256,
		"AMQ_SQUAD_BRANCH": rec.Branch, "AMQ_SQUAD_REQUEST_ID": rec.RequestID, "AMQ_SQUAD_RULES_PATH": rec.RulesPath, "AMQ_SQUAD_RULES_SHA256": rec.RulesSHA256,
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

// drainProof runs one amq drain with the seat's final environment (R52). An
// inbox that already holds new mail is proved with a read-only `amq list
// --new` instead, so the proof never consumes a message meant for the seat.
func drainProof(o Options, rec launch.Record, env []string) (string, error) {
	amq := rec.AMQPath
	if amq == "" {
		amq = "amq"
	}
	args := []string{"drain", "--me", rec.Handle, "--json"}
	if entries, err := os.ReadDir(filepath.Join(rec.Root, "agents", rec.Handle, "inbox", "new")); err == nil && len(entries) > 0 {
		args = []string{"list", "--me", rec.Handle, "--new", "--json"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := o.RunAMQ(ctx, amq, args, env, firstNonEmpty(rec.Worktree, rec.CWD))
	if err != nil {
		return "", refusef("drain_failed", "amq %s failed: %v: %s", args[0], err, trimOut(out))
	}
	return "amq " + args[0], nil
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
