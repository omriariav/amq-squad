#!/usr/bin/env python3
"""The revert runs of brief 3.1.1-3, the lead's screen and its commands
(docs/v3-spike/311-evidence/311-squad-ux.md, section "Revert runs").

Each case puts one change back in the working tree, runs the row that must
catch it, and expects it to fail; the touched file is restored with git
checkout after every run, in a finally. Run it from a clean checkout of the
branch:
  python3 docs/v3-spike/311-evidence/311-squad-ux-revert-runs.py [N ...]
It refuses a tree with any change. No network, no real AMQ, tmux or Buzz:
the rows run on the stand-in pi and Go's in-process fakes. It prints one
JSON line per case: its number, the row, the revert, caught or NOT CAUGHT,
and the failing output's first line."""
import subprocess, sys, re, json
W = subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip()
if subprocess.run(["git", "status", "--porcelain", "--untracked-files=all"], cwd=W, capture_output=True, text=True).stdout:
    sys.exit("refused: the tree has changes; the runs rewrite files and restore them")
PI = "cd pi && node --test test/%s.test.ts"
def PIN(f, pat): return "cd pi && node --test --test-name-pattern='%s' test/%s.test.ts" % (pat, f)
GO = "go test -count=1 -run '%s' ./%s/"
L = "pi/extensions/lib/"
R = [
 # row 1
 ("1", "the actions not registered (only /squad and /team)", L+"commands.ts",
  'const names = Object.keys(COMMANDS).filter((n) => COMMANDS[n].phases.some((p) => host.phases.includes(p)));',
  'const names = Object.keys(COMMANDS).filter((n) => ["squad", "team"].includes(n) && COMMANDS[n].phases.some((p) => host.phases.includes(p)));', PI % "commands"),
 ("1", "a command named for a pi built-in (/copy for /dismiss)", L+"commands.ts",
  '	dismiss: { description:', '	copy: { description:', PI % "commands"),
 ("1", "the 3.1.0 description (presses a key)", L+"commands.ts",
  'add: { description: "add a task or a seat to the running team"', 'add: { description: "The squad screen: /squad add|reset|clear|answer presses a key"', PI % "commands"),
 ("1", "bare /squad toggles the screen", L+"commands.ts",
  '				if (name === "squad") {', '				if (name === "squad") {\n					if (!arg) { if (phase === "team" && host.screen) host.screen.setExpanded(!host.screen.expanded); else host.solo?.setExpanded(!host.solo.expanded); return; }', PI % "commands"),
 # row 2
 ("2", "the 3.1.0 keys line on the team screen", L+"screen.ts",
  '	out.push(commandsLine("team"));', '	out.push("keys: /squad add | /squad reset | /squad clear | /squad answer");', PI % "commands"),
 ("2", "the plan block without the commands line", L+"team.ts",
  '		out.push(commandsLine("solo"));', '', PI % "commands"),
 # row 3
 ("3", "the seat line's state/reason_code (3.1.0)", L+"words.ts",
  '	if (s.state === "blocked" && s.task_id) return', '	return { text: `${s.state}/${s.reason_code}` };\n	if (s.state === "blocked" && s.task_id) return', PIN("words", "row 3")),
 ("3", "a blocked task's line by its code", L+"words.ts",
  '	const [happened, todo] = f(d);\n	return { line: `${t.id} blocked: ${happened}; ${todo}` };', '	return { line: `${t.id}  blocked  ${r}` };', PIN("words", "row 3")),
 # row 4
 ("4", "a refusal's verb text above the rule (sentence without the strip)", L+"words.ts",
  '	const d = plainDetail(detail);', '	const d = detail.trim();', PI % "words"),
 ("4", "the gate line with its id and prefix above the rule", L+"screen.ts",
  '		out.push(`  ${gateLabel(g)}  (answer on the phone; /answer shows it)`);', '		out.push(`  ${gateLabel(g)}  ${g.id}  ${typedPrefix(g.target_digest)}`);', PI % "words"),
 ("4", "tools.ts:143, the state and reason first (3.1.1-2 form)", L+"tools.ts",
  'so there is nothing to widen; to send it back to a seat, the lead returns the task with amq-squad task reset ${t.id ?? "ID"} --pool RUNTIME:MODEL:EFFORT\\n\\n${RULE}\\nstate ${t.state ?? "?"}, reason ${t.block_reason || "none"}`',
  '(state ${t.state ?? "?"}, reason ${t.block_reason || "none"}); there is nothing to widen; to send it back to a seat, the lead returns the task with amq-squad task reset ${t.id ?? "ID"} --pool RUNTIME:MODEL:EFFORT`', PI % "words"),
 ("4", "task_widen.go widenRefused.Error, the `widen refused (%s): %s` form", "internal/cli/task_widen.go",
  '	return fmt.Sprintf("%s\\n\\n%s\\nwiden refused (%s): %s", widenPlain(e.code), gatestore.Rule, e.code, e.msg)', '	return fmt.Sprintf("widen refused (%s): %s", e.code, e.msg)', GO % ("TestTheWidenRefusalPutsItsCodeBelowTheRule", "internal/cli")),
 ("4", "task_widen.go the verb's `refused (%s)` form from the socket reply", "internal/cli/task_widen.go",
  '		return &codedExitError{code: ExitCommitRefused, msg: widenReplyRefusal(id, reply)}', '		return &codedExitError{code: ExitCommitRefused, msg: fmt.Sprintf("task widen %s refused (%s): %s", id, reply.Code, reply.Detail)}',
  "sed -i '' 's|msg := widenReplyRefusal(\"t9\", reply)|msg := fmt.Sprintf(\"task widen %s refused (%s): %s\", \"t9\", reply.Code, reply.Detail)|' internal/cli/task_widen_test.go && sed -i '' 's|^import (|import (\\n\\t\"fmt\"|' internal/cli/task_widen_test.go && " + GO % ("TestTheWidenVerbsRefusalPutsItsCodeBelowTheRule", "internal/cli") + "; s=$?; git checkout -q -- internal/cli/task_widen_test.go; exit $s"),
 ("4", "drift.go the gate_store_unreadable alert with the path and error in its line", "internal/mergetrain/drift.go",
  'Detail: fmt.Sprintf("a gate store of this squad could not be read, so no gate in it clears an alert; fix or remove the record named below the line\\n\\n%s\\nthe gate store %s could not be read (%v)", gatestore.Rule, u.path, u.err)',
  'Detail: fmt.Sprintf("the gate store %s could not be read (%v); no gate in it clears an alert", u.path, u.err)', GO % ("TestTheGateStoreUnreadableAlertPutsThePathBelowTheRule", "internal/mergetrain")),
 ("4", "a verb's below-rule code dropped from the record", L+"keys.ts",
  '	return below ? `${refusal(r)} (${below})` : refusal(r);', '	return refusal(r);', PI % "keys"),
 # row 4, the plan reviewer's additions on ed3d1e65: warnings, stops, requests
 ("4", "a seat warning above the rule as the 3.1.0 line (! code: detail)", L+"screen.ts",
  '			out.push(`    ! ${short(ww.line, 200)}`);', '			out.push(`    ! ${short(w)}`);', PIN("words", "row 4: every seat warning")),
 ("4", "a warning code dropped from the table (wake_off)", L+"words.ts",
  '	wake_off: () =>', '	wake_offX: () =>', PIN("words", "row 4: every seat warning")),
 ("4", "an unmapped warning shown by its code above the rule", L+"words.ts",
  '	if (!f) return { line: "warning; its code is below the line", record };', '	if (!f) return { line: w, record };', PIN("words", "row 4: every seat warning")),
 ("4", "a warning's code and detail not kept below the rule", L+"screen.ts",
  '			rec(short(ww.record, 200));', '', PIN("words", "row 4: every seat warning")),
 ("4", "/dismiss without the seat choice", L+"keys.ts",
  '		if (warned.length > 1) {', '		if (warned.length > 99) {', PIN("keys", "dismiss")),
 ("4", "/dismiss offers a seat's warnings by their codes", L+"keys.ts",
  '			const labels = ws.map((x, i) => `${i + 1}. ${warningWords(s.handle, x, s).line}`);', '			const labels = ws.map((x, i) => `${i + 1}. ${x}`);', PIN("keys", "dismiss")),
 ("4", "the seat's stop line as the 3.1.0 'last stop' words", L+"words.ts",
  '	if (!f) return `${happened}; ${todo}`;', '	return `last stop: ${happened}`;\n	if (!f) return `${happened}; ${todo}`;', PIN("words", "row 4: a seat's stop")),
 ("4", "the seat's stop line without what to do", L+"words.ts",
  '	return `${happened}: ${why}; ${then}`;', '	return `${happened}: ${why}`;', PIN("words", "row 4: a seat's stop")),
 ("4", "the train's stop line as the 3.1.0 refusal text", L+"screen.ts",
  '		out.push(`  ${short(trainStop(trainRefusal.refusal), 200)}`);', '		out.push(`  last stop: the merge train stopped: ${short(trainRefusal.refusal)}`);', PIN("words", "row 4")),
 ("4", "a phone request's id and refusal names above the rule", L+"screen.ts",
  '			out.push(`  ${requestWords(r, r.relayed_at ? age(r.relayed_at, now) : "")}`);', '			out.push(`  ${r.request_id}  ${r.lead || "-"}  ${mark}${r.refused.length ? `  refused: ${r.refused.join(", ")}` : ""}`);', PIN("words", "row 4: no line")),
 ("7", "a train rule dropped from the table (ci_green)", L+"words.ts",
  '	ci_green: [', '	ci_greenX: [', PIN("words", "row 7: every wait")),
 ("7", "a relay refusal name dropped from the table", L+"words.ts",
  '	relay_refused_gate_thread: ', '	relay_refused_gate_threadX: ', PIN("words", "row 7: every wait")),
 # row 5
 ("5", "the confirm reads the newest plan after the dialog", L+"team.ts",
  '		if (!yes) return { key: k, refused: false, line: "Cancelled; nothing was confirmed." };\n		let r: LeadReply;', '		if (!yes) return { key: k, refused: false, line: "Cancelled; nothing was confirmed." };\n		p = this.proposal ?? p;\n		let r: LeadReply;', PI % "team"),
 ("5", "the dialog's no confirms anyway", L+"team.ts",
  '		if (!yes) return { key: k, refused: false, line: "Cancelled; nothing was confirmed." };\n		let r: LeadReply;', '		let r: LeadReply;', PI % "team"),
 ("5", "two live plans: no select, the newest confirmed", L+"team.ts",
  '		if (live.length > 1) {', '		if (live.length > 99) {', PI % "team"),
 ("5", "a confirm tool beside the command (a model message could confirm)", L+"team.ts",
  '	pi.registerTool({\n		name: "amq_team_apply",', '	pi.registerTool({ name: "amq_team_confirm", label: "c", description: "c", parameters: Type.Object({}), async execute() { return { content: [{ type: "text", text: "" }], details: undefined }; } });\n	pi.registerTool({\n		name: "amq_team_apply",', PI % "team"),
 # row 6
 ("6", "an action registered as a tool too", L+"commands.ts",
  '	for (const name of names) {\n		const def = COMMANDS[name];', '	for (const name of names) {\n		pi.registerTool({ name, label: name, description: name, parameters: {} as any, async execute() { return { content: [], details: undefined }; } } as any);\n		const def = COMMANDS[name];', PI % "commands"),
 # row 7
 ("7", "a block reason dropped from the table (verdict_failed)", L+"words.ts",
  '	verdict_failed: () => ["its verifier failed", `${RESET} to try again`],\n', '', PI % "words"),
 ("7", "a wait reason dropped from the table (seat_cap)", L+"words.ts",
  '	seat_cap: (_t, c) =>', '	seat_capX: (_t, c) =>', PI % "words"),
 ("7", "a seat code dropped from the table (idle)", L+"words.ts",
  '	idle: () => "idle",\n', '', PI % "words"),
 ("7", "the size cap's numbers guessed away (no detail read)", L+"words.ts",
  '	if (files) parts.push(`${files[1]} files over the cap of ${files[2]}`);', '', PI % "words"),
 ("7", "the screen reads no task record (no detail fetch)", L+"screen.ts",
  '	async readDetails(st: ScreenStatus): Promise<void> {', '	async readDetails(st: ScreenStatus): Promise<void> {\n		return;', PI % "words"),
 ("7", "a title without the redaction", L+"words.ts",
  '	const one = redactFact(String(s ?? ""))', '	const one = String(s ?? "")', PI % "words"),
 ("7", "a title without the control-character strip", L+"words.ts",
  '		.replace(/[\\u0000-\\u001f\\u007f-\\u009f]/g, " ")\n', '', PI % "words"),
 ("7", "an empty seats block shows nothing", L+"screen.ts",
  '	if (!st.seats.length) out.push("  No seat is running yet.");', '', PI % "words"),
 ("7", "the hold not mapped (held by the lead)", L+"words.ts",
  '	if (r === HOLD) return { line: `${t.id} held by the lead` };', '', PI % "words"),
 # row 8
 ("8", "/widen runs task widen whatever the dialog answers", L+"keys.ts",
  '		if (!yes) return { key: k, refused: false, line: "Cancelled; nothing was widened." };', '', PI % "keys"),
 ("8", "the dialog without the seat's reason", L+"keys.ts",
  '			typed ? `The seat\'s reason: ${reason || "(none given)"}` : "",', '', PI % "keys"),
 ("8", "/widen offers every blocked task, not only a widen request", L+"keys.ts",
  '		const blocked = st.tasks.filter((t) => t.task_state === "blocked" && WIDENABLE.includes(t.block_reason));', '		const blocked = st.tasks.filter((t) => t.task_state === "blocked");', PI % "keys"),
]
only = sys.argv[1:] 
out = []
for i, (row, what, f, old, new, cmd) in enumerate(R, 1):
    if only and str(i) not in only: continue
    p = f"{W}/{f}"
    s = open(p).read()
    if old not in s:
        out.append((i, row, what, "PATCH MISSING", "")); continue
    open(p, "w").write(s.replace(old, new, 1))
    try:
        r = subprocess.run(cmd, shell=True, cwd=W, capture_output=True, text=True, timeout=600)
    finally:
        subprocess.run(["git", "checkout", "-q", "--", f], cwd=W)
    text = r.stdout + r.stderr
    first = ""
    for pat in [r"^not ok \d+ - .*", r"^--- FAIL: .*", r"^FAIL.*", r"^SyntaxError.*", r"^.*Error.*"]:
        m = re.search(pat, text, re.M)
        if m: first = m.group(0); break
    # the failing assertion's message line after a not ok
    msg = ""
    m = re.search(r"^not ok \d+ .*\n(?:.*\n){0,8}?\s+error: (.*)", text, re.M)
    if m: msg = m.group(1).strip()
    m2 = re.search(r"^\s+\S+_test\.go:\d+: (.*)", text, re.M)
    if m2: msg = m2.group(1).strip()
    out.append((i, row, what, "caught" if r.returncode != 0 else "NOT CAUGHT", (first + (" | " + msg if msg else ""))[:300]))
for o in out:
    print(json.dumps(o))
dirty = subprocess.run(["git", "status", "--short"], cwd=W, capture_output=True, text=True).stdout
print("DIRTY:" + dirty if dirty.strip() else "clean")
