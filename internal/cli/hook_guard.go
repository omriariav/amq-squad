package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

const guardUsage = `amq-squad guard - one verdict for one tool call: allow, needs_gate or deny

Usage:
  amq-squad guard --me HANDLE --stdin     (the hook payload: Claude Code PreToolUse or pi tool_call)
  amq-squad guard --me HANDLE --tool NAME [--arg KEY=VALUE]... [--cwd DIR]     (tests, manual checks)

Guard is the in-session advisory check. Enforcement stays at the harness-side
commit and the core merge; the model is never the enforcement point.

Rules, in order: the four typed gates (tag and release, external send,
credential change, growth beyond the grant) are needs_gate unless the gate
store holds an answer whose operatorauth envelope verifies against the owner's
trust store and binds the gate id, action class and target digest; a merge to
the default branch is deny (seats never merge); reading the owner's GitHub or
release credential (gh auth token, security find-*-password -w, git credential
fill, launchctl getenv SSH_AUTH_SOCK) is deny; a force push or a push to any
branch but the seat's own is deny; a write to a protected path is deny;
everything else is allow. For Write/Edit-style tools the target is
tool_input.file_path; a Bash command is parsed best-effort. Deterministic, no
model call.

Output: one JSON object on stdout {verdict, reason_code, reason, action_class,
target_digest, ...}, exit 0 for allow, needs_gate and deny alike. Any error
exits 1 with no verdict (the hook's fail-open path, R36). Mapping a verdict to
a hook decision is the plugin's job.

Examples:
  amq-squad guard --me cto --stdin < pretooluse.json
  amq-squad guard --me cto --tool Bash --arg "command=git push origin HEAD"
`

type guardVerdict struct {
	Verdict          string `json:"verdict"` // allow | needs_gate | deny
	ReasonCode       string `json:"reason_code"`
	Reason           string `json:"reason"`
	ActionClass      string `json:"action_class,omitempty"`
	TargetDigest     string `json:"target_digest,omitempty"`
	Target           string `json:"target,omitempty"`
	Grants           string `json:"grants"`
	GrantsReasonCode string `json:"grants_reason_code,omitempty"`
}

type guardCall struct {
	Tool string
	Args map[string]string
	CWD  string
}

type guardArgs []string

func (a *guardArgs) String() string     { return strings.Join(*a, ",") }
func (a *guardArgs) Set(v string) error { *a = append(*a, v); return nil }

// guardEvaluate is evaluateGuard behind a seam, so a test can prove that a
// panic still maps to exit 1.
var guardEvaluate = evaluateGuard

// runGuard maps every error, and any panic, to exit 1 with no verdict on
// stdout: guard never exits 2, by construction, so the hook can tell "no
// verdict" from a verdict (R36 fail-open).
func runGuard(args []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = UsageError(fmt.Sprintf("guard: internal error: %v", r))
		}
	}()
	err = runGuardVerdict(args)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return err
	}
	return UsageError(err.Error())
}

func runGuardVerdict(args []string) error {
	h := newHookFlags("guard", guardUsage)
	tool := h.fs.String("tool", "", "tool name")
	cwd := h.fs.String("cwd", "", "working directory of the call (default: payload cwd, else the current dir)")
	stdin := h.fs.Bool("stdin", false, "read the hook payload from stdin")
	var kv guardArgs
	h.fs.Var(&kv, "arg", "tool argument KEY=VALUE (repeatable)")
	if err := h.parse(args); err != nil {
		return err
	}
	id, err := h.identity()
	if err != nil {
		return err
	}
	call := guardCall{Tool: *tool, Args: map[string]string{}, CWD: *cwd}
	if *stdin {
		raw, err := readHookPayload("guard")
		if err != nil {
			return err
		}
		// Claude Code PreToolUse (tool_name, tool_input, cwd) or pi tool_call
		// (toolName, input); scalar inputs become strings, others compact JSON.
		var p struct {
			ToolName  string                     `json:"tool_name"`
			PiTool    string                     `json:"toolName"`
			ToolInput map[string]json.RawMessage `json:"tool_input"`
			PiInput   map[string]json.RawMessage `json:"input"`
			CWD       string                     `json:"cwd"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return hookErrorf("guard: payload is not a JSON object: %v", err)
		}
		call.Tool = firstOf(call.Tool, p.ToolName+p.PiTool)
		call.CWD = firstOf(call.CWD, p.CWD)
		for _, in := range []map[string]json.RawMessage{p.ToolInput, p.PiInput} {
			for k, v := range in {
				var s string
				if json.Unmarshal(v, &s) != nil {
					s = string(v)
				}
				call.Args[k] = s
			}
		}
	}
	for _, pair := range kv {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" {
			return hookErrorf("guard: --arg %q is not KEY=VALUE", pair)
		}
		call.Args[k] = v
	}
	if call.Tool == "" {
		return hookErrorf("guard: --tool or a payload tool name is required")
	}
	if call.CWD == "" {
		call.CWD, _ = os.Getwd()
	}
	b, _ := json.Marshal(guardEvaluate(id, call, hookNow()))
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}

// guardAction is one effect of a call; evaluateGuard applies the rules to
// all of a call's actions in rule order. env is the checkout the action runs
// in: the call's, or the -C directory of a git segment.
type guardAction struct {
	env            *guardEnv
	action, target string // typed gate: the operatorauth action and the target it binds
	merge          string // "gh" (gh pr merge, merge API) or "git" (git merge)
	credRead       string // a read of the owner's GitHub or release credential (3-2)
	push           guardPush
	writes         []string
	blockSafe      bool // a Write/Edit known to keep the file's managed block
}

type guardPush struct {
	force, all, del bool
	dsts            []string // "" is the current branch
}

func evaluateGuard(id hookIdentity, call guardCall, now time.Time) guardVerdict {
	env := &guardEnv{id: id, cwd: call.CWD}
	env.readGit()
	grants, err := loadHookGrants(env.repo)
	if err != nil {
		return guardVerdict{Verdict: "deny", ReasonCode: "grants_invalid", Reason: err.Error(), Grants: grants.Source}
	}
	env.grants = grants
	out := func(verdict, code, reason string) guardVerdict {
		return guardVerdict{Verdict: verdict, ReasonCode: code, Reason: reason, Grants: grants.Source, GrantsReasonCode: grants.ReasonCode}
	}
	actions := classifyGuardCall(env, call)
	for i := range actions { // a write to a credential path is the credential gate
		for _, w := range actions[i].writes {
			if p, ok := env.credentialPath(w); ok {
				actions = append(actions, guardAction{action: "credential_change", target: gatetarget.Write(p)})
			}
		}
	}
	var gates []hookGate
	for _, a := range actions {
		if a.action == "" {
			continue
		}
		if gates == nil {
			gates, _ = readHookGates(id.Root, now)
		}
		digest, accepted := gatetarget.Digest(a.action, a.target), false
		for _, g := range gates {
			accepted = accepted || (g.State == "accepted" && g.Class == a.action && g.Digest == digest)
		}
		if accepted && grants.Insecure {
			v := out("needs_gate", grants.ReasonCode, "the owner grants file is present but untrusted ("+grants.InsecureWhy+"); typed gates stay closed until it is fixed")
			v.ActionClass, v.TargetDigest, v.Target = a.action, digest, a.target
			return v
		}
		if !accepted {
			v := out("needs_gate", "gate_"+gatetarget.Class(a.action), "no verified gate answer for this action and target; raise the gate")
			v.ActionClass, v.TargetDigest, v.Target = a.action, digest, a.target
			return v
		}
	}
	for i := range actions {
		if actions[i].env == nil {
			actions[i].env = env
		}
	}
	for _, a := range actions {
		if a.credRead != "" {
			return out("deny", "credential_read", a.credRead+" reads the owner's credential; seats hold none (R33): the harness pushes and the merge train merges under the owner's identity")
		}
	}
	for _, a := range actions {
		def := a.env.defaultBranch()
		if a.merge == "gh" || (a.merge == "git" && a.env.currentBranch() == def) {
			return out("deny", "seat_merge", "seats never merge to the default branch; the harness does (plan 7.2)")
		}
		for _, d := range a.push.dsts {
			if a.env.branchOf(d) == def {
				return out("deny", "seat_merge", "a push to the default branch "+def+" is a merge")
			}
		}
	}
	for _, a := range actions {
		switch p := a.push; {
		case p.force:
			return out("deny", "force_push", "force pushes are denied")
		case p.del:
			return out("deny", "push_delete", "branch deletion is denied")
		case p.all:
			return out("deny", "push_other_branch", "--all and --mirror push more than the seat branch")
		}
		for _, d := range a.push.dsts {
			if own, b := a.env.ownBranch(), a.env.branchOf(d); own == "" || b != own {
				return out("deny", "push_other_branch", fmt.Sprintf("push to %q; the seat branch is %q", b, own))
			}
		}
	}
	for _, a := range actions {
		for _, w := range a.writes {
			if entry, ok := a.env.protectedPath(w, a.blockSafe); ok {
				return out("deny", "protected_path", w+" is under protected "+entry)
			}
		}
	}
	return out("allow", "allowed", "")
}

var (
	guardSendWords = map[string]bool{"send": true, "post": true, "reply": true, "forward": true, "tweet": true, "publish": true}
	guardMergeAPI  = regexp.MustCompile(`pulls/[0-9]+/merge\b`)
	guardWrappers  = map[string]bool{"sudo": true, "env": true, "command": true, "exec": true, "nohup": true, "time": true, "nice": true}
	// guardWrapperOperandFlags are the wrapper flags that take the next word.
	guardWrapperOperandFlags = map[string]bool{
		"sudo -u": true, "sudo -g": true, "sudo -h": true, "sudo -p": true, "sudo -C": true, "sudo -D": true, "sudo -r": true, "sudo -t": true, "sudo -U": true,
		"env -u": true, "env -C": true, "nice -n": true, "exec -a": true, "time -o": true, "time -f": true,
	}
)

func classifyGuardCall(env *guardEnv, call guardCall) []guardAction {
	switch call.Tool {
	case "Bash", "bash", "shell", "exec_command":
		return classifyShell(env, call.Args["command"]+call.Args["cmd"], 0)
	case "Write", "Edit", "MultiEdit", "NotebookEdit", "write", "edit":
		writes := positionals([]string{call.Args["file_path"], call.Args["notebook_path"], call.Args["path"]})
		safe := len(writes) == 1 && managedBlockKept(call.Tool, writes[0], call.CWD, call.Args)
		return []guardAction{{writes: writes, blockSafe: safe}}
	}
	if rest, ok := strings.CutPrefix(call.Tool, "mcp__"); ok {
		for _, w := range strings.FieldsFunc(strings.ToLower(rest[strings.LastIndex(rest, "__")+1:]), func(r rune) bool { return r == '_' || r == '-' }) {
			if guardSendWords[w] {
				to := call.Args["to"] + call.Args["recipient"] + call.Args["channel"] + call.Args["channel_id"] + call.Args["chat_id"] + call.Args["email"]
				return []guardAction{{action: "external_send", target: gatetarget.MCPSend(call.Tool, to)}}
			}
		}
	}
	return nil
}

// guardShells run a command string given with -c (also inside a short
// cluster such as -lc).
var guardShells = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true}

// stripWrappers drops leading env assignments and wrapper commands (sudo,
// env, nice ...) with their flags, including the operand of the wrapper flags
// that take one (sudo -u root, env -u NAME, nice -n 5).
func stripWrappers(words []string) []string {
	wrapper := ""
	for len(words) > 0 {
		w := words[0]
		switch {
		case isEnvAssign(w):
		case guardWrappers[filepath.Base(w)]:
			wrapper = filepath.Base(w)
		case wrapper != "" && strings.HasPrefix(w, "-"):
			if guardWrapperOperandFlags[wrapper+" "+w] && len(words) > 1 {
				words = words[1:]
			}
		case wrapper == "nice" && strings.HasPrefix(w, "+"):
		default:
			return words
		}
		words = words[1:]
	}
	return words
}

func classifyShell(env *guardEnv, command string, depth int) []guardAction {
	var out []guardAction
	for _, seg := range splitShell(command) {
		words := stripWrappers(seg.words)
		a := guardAction{env: env, writes: seg.writes}
		if len(words) > 0 {
			classifyCommand(env, filepath.Base(words[0]), words[1:], &a)
			if script, ok := shellScript(words); ok && depth < 3 {
				out = append(out, classifyShell(env, script, depth+1)...)
			}
		}
		out = append(out, a)
	}
	return out
}

// shellScript returns the command string of `sh -c STRING`, `/bin/bash -lc
// STRING` and the like: the word after the first option cluster with a c.
func shellScript(words []string) (string, bool) {
	if !guardShells[filepath.Base(words[0])] {
		return "", false
	}
	for i := 1; i < len(words) && strings.HasPrefix(words[i], "-"); i++ {
		if !strings.HasPrefix(words[i], "--") && strings.Contains(words[i], "c") && i+1 < len(words) {
			return words[i+1], true
		}
	}
	return "", false
}

func classifyCommand(env *guardEnv, cmd string, args []string, a *guardAction) {
	if cmd == "git" { // drop git's global options (-C DIR, -c K=V, --no-pager ...)
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			if (args[0] == "-C" || args[0] == "-c") && len(args) > 1 {
				if args[0] == "-C" { // the segment runs in that checkout
					dir := args[1]
					if !filepath.IsAbs(dir) {
						dir = filepath.Join(a.env.cwd, dir)
					}
					a.env = &guardEnv{id: env.id, grants: env.grants, cwd: filepath.Clean(dir)}
				}
				args = args[1:]
			}
			args = args[1:]
		}
	}
	pos := positionals(args)
	words := append([]string{cmd}, pos...)
	for n := min(3, len(words)); n >= 1; n-- {
		key := strings.Join(words[:n], " ")
		if action, ok := gatetarget.Commands[key]; ok {
			if action == "tag" && (len(words) == n || hasFlagPrefix(args, "-l") || hasFlagPrefix(args, "--list")) {
				break // listing tags
			}
			a.action, a.target = action, gatetarget.Command(key, strings.Join(words[n:min(n+1, len(words))], ""))
			break
		}
	}
	sub := append(pos, "", "")
	switch {
	case cmd == "gh" && sub[0] == "pr" && sub[1] == "merge", cmd == "gh" && sub[0] == "api" && guardMergeAPI.MatchString(strings.Join(args, " ")):
		a.merge = "gh"
	case cmd == "gh" && sub[0] == "auth" && (sub[1] == "token" || sub[1] == "status" && (hasFlagPrefix(args, "-t") || hasFlagPrefix(args, "--show-token"))),
		cmd == "security" && strings.HasPrefix(sub[0], "find-") && strings.HasSuffix(sub[0], "-password") && (hasFlagPrefix(args, "-w") || hasFlagPrefix(args, "-g")),
		cmd == "git" && (sub[0] == "credential" && sub[1] == "fill" || strings.HasPrefix(sub[0], "credential-")),
		cmd == "launchctl" && sub[0] == "getenv" && sub[1] == "SSH_AUTH_SOCK":
		a.credRead = strings.Join(append([]string{cmd}, pos[:min(2, len(pos))]...), " ")
	case cmd == "git" && sub[0] == "merge" && !hasFlagPrefix(args, "--abort"):
		a.merge = "git"
	case cmd == "git" && sub[0] == "push":
		classifyPush(args[1:], a)
	case cmd == "git" && sub[0] == "config" && (strings.HasPrefix(sub[1], "credential.") || sub[1] == "user.signingkey"):
		a.action, a.target = "credential_change", gatetarget.GitConfig(sub[1])
	case cmd == "gws" && (containsString(pos, "send") || containsString(pos, "reply") || containsString(pos, "forward")):
		a.action, a.target = "external_send", gatetarget.GWSSend(sub[0], sub[1])
	case (cmd == "amq" || cmd == "amq-squad") && (sub[0] == "launch" || sub[0] == "coop" && sub[1] == "exec"):
		if seats := env.seatCount(); seats+1 > env.grants.MaxSeats {
			a.action, a.target = "spawn", gatetarget.Grow(env.id.Root, seats+1)
		}
	case cmd == "cp" || cmd == "install" || cmd == "ln":
		a.writes = append(a.writes, pos[max(0, len(pos)-1):]...)
	case cmd == "rm" || cmd == "mv" || cmd == "tee" || cmd == "touch" || cmd == "chmod" || cmd == "truncate" || cmd == "mkdir" || ((cmd == "sed" || cmd == "perl") && hasFlagPrefix(args, "-i")):
		a.writes = append(a.writes, pos...)
	case cmd == "dd":
		for _, w := range args {
			if p, ok := strings.CutPrefix(w, "of="); ok {
				a.writes = append(a.writes, p)
			}
		}
	}
}

// classifyPush reads git push arguments: force and delete forms, --all and
// --mirror, tag pushes (the release gate) and the destination branches.
func classifyPush(args []string, a *guardAction) {
	p := &guardPush{}
	var refs []string
	repo := false // with --repo every positional is a refspec
	for i := 0; i < len(args); i++ {
		switch w := args[i]; {
		case w == "--force" || strings.HasPrefix(w, "--force-with-lease") || w == "--force-if-includes":
			p.force = true
		case w == "--all" || w == "--mirror" || w == "--branches":
			p.all = true
		case w == "--delete" || w == "--prune":
			p.del = true
		case w == "--tags" || w == "--follow-tags":
			a.action, a.target = "tag", gatetarget.TagPush("--tags")
		case strings.HasPrefix(w, "--repo="):
			repo = true
		case w == "--repo":
			repo, i = true, i+1
		case w == "--push-option" || w == "--receive-pack" || w == "--exec":
			i++
		case strings.HasPrefix(w, "--"):
		case strings.HasPrefix(w, "-") && len(w) > 1: // a short cluster such as -vf or -uo X
			p.force = p.force || strings.Contains(w, "f")
			p.del = p.del || strings.Contains(w, "d")
			if strings.HasSuffix(w, "o") {
				i++
			}
		default:
			refs = append(refs, w)
		}
	}
	if !repo && len(refs) > 0 {
		refs = refs[1:] // the remote
	}
	for i := 0; i < len(refs); i++ {
		r := refs[i]
		if r == "tag" && i+1 < len(refs) {
			i++
			r = "refs/tags/" + refs[i]
		}
		if strings.HasPrefix(r, "+") {
			p.force, r = true, r[1:]
		}
		src, dst, colon := strings.Cut(r, ":")
		if !colon {
			dst = src
		}
		p.del = p.del || (colon && src == "")
		if strings.HasPrefix(dst, "refs/tags/") {
			a.action, a.target = "tag", gatetarget.TagPush(dst)
			continue
		}
		p.dsts = append(p.dsts, dst)
	}
	if len(p.dsts) == 0 && a.action != "tag" {
		p.dsts = []string{""}
	}
	a.push = *p
}

// positionals returns the non-empty words that are not flags.
func positionals(args []string) []string {
	var out []string
	for _, w := range args {
		if w != "" && !strings.HasPrefix(w, "-") {
			out = append(out, w)
		}
	}
	return out
}

func hasFlagPrefix(args []string, prefix string) bool {
	for _, w := range args {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}

func isEnvAssign(w string) bool {
	k, _, ok := strings.Cut(w, "=")
	return ok && k != "" && !strings.ContainsAny(k, "/-.")
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
