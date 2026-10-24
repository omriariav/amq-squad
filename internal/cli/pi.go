package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/role"
)

// The built-in pi (plan 4.1, R1, R2, brief 4-1). `amq-squad pi run` is the
// kept command path that starts the embedded pi for a seat or the lead: it
// verifies the unpacked runtime against the embedded manifest, writes the
// seat's pi config, scrubs the environment and execs Node from the runtime.
// It never runs a pi or node from PATH. seat-exec (2b-2) hands a pi seat to
// the same code path (piLaunch) once both branches meet.

const piUsage = `amq-squad pi - the built-in pi runtime

Usage:
  amq-squad pi run [--seat H] [--model PROVIDER/ID]... [--thinking LEVEL]
                   [--lead] [--approve-tools T,...] [--resume] [--no-package] [--dry-run] [-- PI-ARGS...]
  amq-squad pi verify [--json]
  amq-squad pi path [--json]

run      start the embedded pi. The seat is --seat, else AM_ME, else "lead";
         its pi config lives in ~/.amq-squad/pi-agent/<seat>. The amq-squad
         package is loaded unless --no-package (probes). --model restricts the
         seat to these models (repeatable; the registry entry's models).
         --resume applies the amendment 23 check: a resume with a tool result
         in the seat's ledger and no session file for --session-id is refused.
verify   re-hash the unpacked runtime against the embedded manifest (doctor).
path     print the runtime directory and the node + pi entry argv.

The runtime unpacks once to ~/.amq-squad/runtime/<version>-<digest>/; every
launch re-hashes it. AMQ_SQUAD_PI_TIMING=1 prints the launch timing to stderr.
`

// piRun runs the embedded pi as a child and returns its exit code (tests
// replace it); piExit ends the launcher with pi's exit code.
var (
	piRun  = runProcess
	piExit = os.Exit
)

// piLoad is the payload seam (tests replace it with a fixture).
var piLoad = piruntime.Load

func runPi(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, piUsage)
		if len(args) == 0 {
			return usageErrorf("pi needs a subcommand: run, verify or path")
		}
		return flag.ErrHelp
	}
	switch args[0] {
	case "run":
		return runPiRun(args[1:])
	case "verify":
		return runPiVerify(args[1:])
	case "path":
		return runPiPath(args[1:])
	}
	return unknownSubcommandError("pi", args[0], "run", "verify", "path")
}

type piRunFlags struct {
	seat, thinking, approve string
	models                  multiFlag
	lead, resume, noPackage bool
	dryRun                  bool
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func runPiRun(args []string) error {
	fs := flag.NewFlagSet("pi run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f piRunFlags
	fs.StringVar(&f.seat, "seat", "", "seat handle (default AM_ME, else lead)")
	fs.Var(&f.models, "model", "PROVIDER/ID the seat may use (repeatable)")
	fs.StringVar(&f.thinking, "thinking", "", "pi thinking level (the registry entry's effort)")
	fs.BoolVar(&f.lead, "lead", false, "the team lead: adds the dispatch tool")
	fs.StringVar(&f.approve, "approve-tools", "", "tools held for approval (local or amq-remote)")
	fs.BoolVar(&f.resume, "resume", false, "this start resumes a seat (amendment 23 check)")
	fs.BoolVar(&f.noPackage, "no-package", false, "start pi without the amq-squad package")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the launch as JSON and do not exec")
	fs.Usage = func() { fmt.Fprint(os.Stderr, piUsage) }
	if err := parseFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.Usage()
		}
		return err
	}
	seat := firstNonEmptyStr(f.seat, os.Getenv("AM_ME"), "lead")
	if me := os.Getenv("AM_ME"); me != "" && me != seat {
		return usageErrorf("pi run: --seat %q disagrees with AM_ME=%q", seat, me)
	}
	return piLaunch(piLaunchSpec{Seat: seat, Flags: f, PiArgs: fs.Args()})
}

type piLaunchSpec struct {
	Seat    string
	Flags   piRunFlags
	PiArgs  []string
	Overlay map[string]string // identity and role for the lead
}

func piLaunch(s piLaunchSpec) error {
	start := time.Now()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	e, err := piLoad()
	if err != nil {
		return err
	}
	rt, tm, err := piruntime.Ensure(home, e)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	agentDir := piruntime.AgentDirFor(home, s.Seat)
	if s.Flags.resume {
		root := firstNonEmptyStr(s.Overlay["AM_ROOT"], os.Getenv("AM_ROOT"))
		if root == "" {
			return usageErrorf("pi run --resume needs the seat's AMQ root (AM_ROOT)")
		}
		if err := piruntime.CheckResume(piruntime.LedgerPath(root, s.Seat), agentDir, cwd, s.PiArgs); err != nil {
			return err
		}
	}
	models, err := piModels(s.Flags.models)
	if err != nil {
		return err
	}
	overlay := map[string]string{}
	for k, v := range s.Overlay {
		overlay[k] = v
	}
	if self, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(self); err == nil {
			self = real
		}
		overlay["AMQ_SQUAD_BIN"] = self
	}
	if amq, err := exec.LookPath("amq"); err == nil {
		if abs, err := filepath.Abs(amq); err == nil {
			overlay["AMQ_SQUAD_AMQ"] = abs
		}
	}
	if s.Flags.lead {
		overlay["AMQ_SQUAD_LEAD"] = "1"
	}
	if s.Flags.approve != "" {
		overlay["AMQ_SQUAD_APPROVE_TOOLS"] = s.Flags.approve
	}
	// The seat's TMPDIR: a 0700 directory under its own extension directory
	// in the AMQ root (reviewer ruling), else under its pi agent dir.
	tmp := ""
	if root := firstNonEmptyStr(s.Overlay["AM_ROOT"], os.Getenv("AM_ROOT")); root != "" {
		tmp = filepath.Join(role.ExtensionDir(filepath.Join(root, "agents", s.Seat)), "tmp")
	}
	piArgs := s.PiArgs
	if s.Flags.lead {
		// Amendment 26: the team lead's tool set is exactly the native AMQ
		// and task tools, the repository-scoped read and list, and status;
		// anything that runs a command is dispatched to a seat.
		piArgs = append([]string{"--tools", strings.Join(piruntime.TeamLeadTools, ",")}, piArgs...)
	}
	l, err := piruntime.Prepare(piruntime.Spec{
		Runtime: rt, Home: home, Seat: s.Seat, AgentDir: agentDir, Parent: os.Environ(), TmpDir: tmp,
		Models: models, Thinking: s.Flags.thinking, Overlay: overlay, Package: !s.Flags.noPackage, Args: piArgs,
	})
	if err != nil {
		return err
	}
	// Review item 2 (a): right before exec, every file node opens to start
	// pi must still be the file the re-hash hashed.
	checkStart := time.Now()
	if err := rt.CheckLaunch(); err != nil {
		return err
	}
	check := time.Since(checkStart)
	rest := time.Since(start) - tm.Rehash
	if os.Getenv("AMQ_SQUAD_PI_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "amq-squad pi: cold=%t rehash_ms=%.1f rest_ms=%.1f launch_check_ms=%.1f files=%d bytes=%d runtime=%s\n",
			tm.Cold, ms(tm.Rehash), ms(rest), ms(check), tm.Files, tm.Bytes, rt.Dir)
	}
	// The in-process self-check reports the window from here to its end.
	l.Env = append(l.Env, "AMQ_SQUAD_EXEC_AT_MS="+strconv.FormatInt(time.Now().UnixMilli(), 10))
	if s.Flags.dryRun {
		names := make([]string, 0, len(l.Env))
		for _, kv := range l.Env {
			name, _, _ := strings.Cut(kv, "=")
			names = append(names, name)
		}
		return printJSONEnvelope("pi_launch", map[string]any{
			"runtime": rt.Dir, "argv": l.Argv, "agent_dir": l.AgentDir, "env_names": names, "scrubbed": l.Removed,
			"overlay": overlay, "cold": tm.Cold, "rehash_ms": ms(tm.Rehash), "rest_ms": ms(rest), "files": tm.Files,
		})
	}
	code, err := piRun(l.Path, l.Argv, l.Env)
	if err != nil {
		return err
	}
	if code == piruntime.ExitRuntimeMismatch {
		// Review 2, item 2 (c): the in-process refusal is the launcher's.
		// It closes the seat (the launcher ends here, so the seat's process
		// is gone) and writes the named reason to the seat's ledger.
		root := firstNonEmptyStr(overlay["AM_ROOT"], os.Getenv("AM_ROOT"))
		recordRuntimeMismatch(root, s.Seat, rt.Dir)
		fmt.Fprintf(os.Stderr, "amq-squad pi: seat %s closed: %s (the runtime changed after the launch check; run amq-squad doctor)\n", s.Seat, piruntime.RuntimeMismatchReason)
	}
	if code != 0 {
		piExit(code)
	}
	return nil
}

// recordRuntimeMismatch writes the launcher's refusal to the seat's
// evidence ledger (the same line format evidence ingest writes).
func recordRuntimeMismatch(root, seat, runtimeDir string) {
	if root == "" || !hookHandlePattern.MatchString(seat) {
		return
	}
	raw, _ := json.Marshal(map[string]any{
		"type": piruntime.RuntimeMismatchReason, "outcome": "refused", "exit": piruntime.ExitRuntimeMismatch,
		"seat": seat, "runtime": runtimeDir, "action": "seat_closed",
	})
	id := hookIdentity{Handle: seat, Root: root, Session: os.Getenv("AM_SESSION")}
	line := newHookLedgerLine(id, raw, hookNow())
	if _, err := appendHookLedger(hookExtPath(root, "ledger", seat+".jsonl"), line); err != nil {
		fmt.Fprintf(os.Stderr, "amq-squad pi: could not record %s in the ledger: %v\n", piruntime.RuntimeMismatchReason, err)
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// piModels turns --model PROVIDER/ID into the seat's model list. An ollama
// model gets the local OpenAI-compatible endpoint (S3, S5) and asks Ollama
// whether it thinks; other providers are pi built-ins whose credentials the
// seat's runtime must declare (EnvNeeds, R16).
func piModels(refs []string) ([]piruntime.Model, error) {
	var out []piruntime.Model
	for _, ref := range refs {
		provider, id, ok := strings.Cut(ref, "/")
		if !ok || provider == "" || id == "" {
			return nil, usageErrorf("--model %q: want PROVIDER/ID, for example ollama/qwen3.6:27b", ref)
		}
		m := piruntime.Model{Provider: provider, ID: id}
		if provider == "ollama" {
			host := strings.TrimRight(firstNonEmptyStr(os.Getenv("OLLAMA_HOST"), "http://localhost:11434"), "/")
			if !strings.Contains(host, "://") {
				host = "http://" + host
			}
			m.BaseURL, m.API, m.APIKey = host+"/v1", "openai-completions", "ollama"
			m.ContextWindow, m.MaxTokens = 131072, 16384
			m.Reasoning = ollamaThinks(host, id)
		}
		out = append(out, m)
	}
	return out, nil
}

// ollamaThinks asks /api/show for the "thinking" capability (1 s bound);
// unknown reads as false.
func ollamaThinks(host, model string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, "POST", host+"/api/show", bytes.NewReader(body))
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var show struct {
		Capabilities []string `json:"capabilities"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&show) != nil {
		return false
	}
	for _, c := range show.Capabilities {
		if c == "thinking" {
			return true
		}
	}
	return false
}

func runPiVerify(args []string) error {
	fs := flag.NewFlagSet("pi verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	e, err := piLoad()
	if err != nil {
		return err
	}
	dir := filepath.Join(piruntime.DefaultBase(home), e.Manifest.DirName())
	tm, verr := piruntime.Verify(home, e)
	if _, statErr := os.Lstat(dir); errors.Is(statErr, os.ErrNotExist) {
		verr = fmt.Errorf("runtime %s is not unpacked yet (the first `amq-squad pi run` or `amq-squad lead` unpacks it)", dir)
	}
	res := map[string]any{"runtime": dir, "ok": verr == nil, "files": tm.Files, "bytes": tm.Bytes, "rehash_ms": ms(tm.Rehash),
		"pi_version": e.Manifest.PiVersion, "node_version": e.Manifest.NodeVersion, "manifest_sha256": e.Digest}
	if verr != nil {
		res["error"] = verr.Error()
	}
	if *jsonOut {
		if err := printJSONEnvelope("pi_verify", res); err != nil {
			return err
		}
	} else if verr == nil {
		fmt.Printf("pi runtime %s ok: %d files, %d bytes re-hashed in %.1f ms\n", dir, tm.Files, tm.Bytes, ms(tm.Rehash))
	}
	if verr != nil {
		return verr
	}
	return nil
}

func runPiPath(args []string) error {
	fs := flag.NewFlagSet("pi path", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	e, err := piLoad()
	if err != nil {
		return err
	}
	rt, tm, err := piruntime.Ensure(home, e)
	if err != nil {
		return err
	}
	if os.Getenv("AMQ_SQUAD_PI_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "amq-squad pi: cold=%t rehash_ms=%.1f total_ms=%.1f files=%d bytes=%d runtime=%s\n",
			tm.Cold, ms(tm.Rehash), ms(tm.Total), tm.Files, tm.Bytes, rt.Dir)
	}
	if *jsonOut {
		return printJSONEnvelope("pi_path", map[string]any{"runtime": rt.Dir, "pi_argv": rt.PiArgv(), "package": rt.PackageDir(),
			"pi_version": e.Manifest.PiVersion, "node_version": e.Manifest.NodeVersion})
	}
	fmt.Println(rt.Dir)
	fmt.Println(strings.Join(rt.PiArgv(), " "))
	return nil
}
