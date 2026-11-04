// Brief 31-2, PR 1, and brief 3.1.1-3: the lead's actions (add, reset,
// dismiss, answer, widen), each its own command, pressed against the
// stand-in pi. Every verb a key runs goes through the injected runner, so
// the test sees each argv and answers it; nothing reaches the real binary,
// AMQ, tmux or Buzz.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { installScreen, renderScreen, type ScreenStatus } from "../extensions/lib/screen.ts";
import { installCommands } from "../extensions/lib/commands.ts";
import { Keys, refusal, WIDEN_REASON } from "../extensions/lib/keys.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import type { RunOptions, RunResult } from "../extensions/lib/verbs.ts";
import { FakePi, fakeCtx } from "./fakepi.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtureText = fs.readFileSync(path.join(here, "status-fixture.json"), "utf8");
const fixture = JSON.parse(fixtureText) as ScreenStatus;
const BIN = "/opt/amq-squad";

function cfg(): Config {
	return { bin: BIN, amq: "", me: "cto", root: "/Users/owner/code/app/.agent-mail/v31", session: "v31", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: true };
}

function res(code: number, stdout: string, stderr = ""): RunResult {
	return { code, stdout, stderr, timedOut: false, spawnError: "", ms: 1 };
}

function envelope(kind: string, data: unknown): string {
	return JSON.stringify({ schema_version: 1, kind, data });
}

type Call = { bin: string; args: string[] };

// rig is the lead's screen and its commands with a scripted runner and
// scripted dialogs: the status verb answers the current document; every
// other verb is answered by reply (default: refuse, so an unexpected verb
// shows). confirm answers pi's confirm dialog.
function rig(reply: (args: string[]) => RunResult, answers: (string | undefined)[], status: () => ScreenStatus = () => fixture, confirm: boolean | undefined = true) {
	const calls: Call[] = [];
	const runner = async (bin: string, args: string[], _o: RunOptions): Promise<RunResult> => {
		if (args[0] === "status") return res(0, JSON.stringify(status()));
		calls.push({ bin, args });
		return reply(args);
	};
	const pi = new FakePi();
	const screen = installScreen(pi as any, cfg(), runner, () => ({ mtimeMs: 1, size: 1 }));
	installCommands(pi as any, { phases: ["team"], phase: () => "team", screen });
	const queue = [...answers];
	const asked: string[] = [];
	const notes: string[] = [];
	const confirms: { title: string; message: string }[] = [];
	const ctx = fakeCtx({ hasUI: true });
	ctx.ui.input = async (title: string) => {
		asked.push(title);
		return queue.shift();
	};
	ctx.ui.select = async (title: string, options: string[]) => {
		asked.push(`${title} [${options.join(" | ")}]`);
		const a = queue.shift();
		if (a !== undefined && !options.includes(a)) throw new Error(`answer ${a} is not among ${options.join(", ")}`);
		return a;
	};
	ctx.ui.confirm = async (title: string, message: string) => {
		confirms.push({ title, message });
		return confirm as boolean;
	};
	ctx.ui.notify = (m: string) => {
		notes.push(m);
	};
	const press = async (key: string, arg = "") => {
		const cmd = pi.commands.get(key);
		assert.ok(cmd, `no /${key} command`);
		await cmd.handler(arg, ctx);
		return ctx.widgets["amq-squad-screen"]?.lines ?? [];
	};
	return { pi, screen, ctx, calls, asked, notes, confirms, press };
}

const OUTCOME = /^ {2}\/(add|reset|dismiss|answer|widen|squad|team): /;

// keyLine is the outcome line above the rule; recLine its record below.
function keyLine(lines: string[]): string {
	const rule = lines.indexOf("----------");
	return lines.find((l, i) => OUTCOME.test(l) && (rule < 0 || i < rule)) ?? `(no key line in\n${lines.join("\n")})`;
}
function recLine(lines: string[]): string {
	const rule = lines.indexOf("----------");
	return lines.find((l, i) => rule >= 0 && i > rule && OUTCOME.test(l)) ?? "";
}

const T1 = "t1: the commit step refused it, it changed files outside its task";
const T1_SHOW = envelope("task_show", { task: { id: "t1", title: "Fix the parser", state: "blocked", block_reason: "outside_declared_files", block_detail: "outside_declared_files: src/x.go", refused_paths: ["src/x.go"] } });

// Row 1 (the extension's half): every key runs its verb from the lead's own
// process: the extension's runner with the configured amq-squad binary
// itself, no shell, no tmux, no other process between. The dismiss, the
// key that is a scheduler decision, is proved under R4 because that
// process descends from the proved lead (cli
// TestClearKeyIsProvedFromTheLeadsDescendant). Reverted (a key that sends
// its verb through another process), the bin differs.
test("each key runs its verb with the lead's own amq-squad binary from the lead's process", async () => {
	const seen: Call[] = [];
	for (const [key, answers] of [
		["add", ["task", "Parser", "implement", "src/parse.go", ""]],
		["reset", [T1, "keep its pool"]],
		["dismiss", [fixture.seats[0].handle + "  " + fixture.seats[0].warnings![0].slice(0, 97) + "..."]],
		["answer", ["Create a tag named v1?"]],
		["widen", []],
	] as [string, string[]][]) {
		const r = rig((args) => (args[0] === "gate" ? res(0, envelope("gate_status", [{ gate_id: "g1", class: "tag", typed_class: "release", target: "git tag v1.2.0", digest: "sha256:8502aa0011223344", state: "open", question_sent: true }])) : args[1] === "show" ? res(0, T1_SHOW) : res(0, envelope("x", { task: { id: "t9", state: "pending" } }))), answers);
		const lines = await r.press(key);
		assert.ok(!/was not|Nothing|could not|There is no/.test(keyLine(lines)) && !keyLine(lines).startsWith("(no key line"), `${key}: ${keyLine(lines)}`);
		seen.push(...r.calls);
	}
	assert.ok(seen.length >= 6);
	for (const c of seen) assert.equal(c.bin, BIN, JSON.stringify(c));
});

// Row 2: the keys add no tool. The lead's model tool list is exactly the
// tip's (no key verb, no gate-answer verb, no scheduler clear); this fails
// when any tool is added, by the keys or anything else, until the list is
// re-read on purpose.
test("the lead's model tool list carries no key verb and no gate-answer verb", async () => {
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	Object.assign(process.env, { AM_ME: "cto", AM_ROOT: "/tmp/amq-squad-keys-root", AM_SESSION: "s", AMQ_SQUAD_BIN: "/bin/false", AMQ_SQUAD_LEAD: "1" });
	const lead = new FakePi();
	amqSquad(lead as any);
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	assert.deepEqual(
		[...lead.tools.keys()].sort(),
		[
			"amq_drain",
			"amq_reply",
			"amq_repo_list",
			"amq_repo_read",
			"amq_scheduler_relaunch",
			"amq_send",
			"amq_status",
			"amq_task_add",
			"amq_task_block",
			"amq_task_claim",
			"amq_task_done",
			"amq_task_list",
			"amq_task_reset",
			"amq_task_widen",
		],
	);
	for (const [name, def] of lead.tools) {
		const text = `${name} ${def.description ?? ""}`;
		assert.doesNotMatch(text, /gate answer|scheduler clear|\bclear\b/i, name);
	}
	for (const k of ["add", "reset", "dismiss", "answer", "widen"]) {
		assert.ok(lead.commands.has(k), `/${k} is a command`);
		assert.ok(![...lead.tools.keys()].some((t) => t.includes(k) && !["amq_task_add", "amq_task_reset", "amq_task_widen"].includes(t)), `/${k} is no tool`);
	}
});

// Row 3: the add key. The owner types the title, picks the class, types
// the declared files and the pool; the key runs `task add` with exactly
// them (cli TestAddKeysArgvAddsTheTaskWithItsFilesAndPool runs this argv
// against a store and finds the task with its files and pool).
test("add: the task goes to task add with its title, class, declared files and pool", async () => {
	const r = rig(() => res(0, envelope("task_add", { root: "/r", task: { id: "t9", state: "pending" } })), ["task", "Parser", "implement", "src/parse.go, src/parse_test.go", "codex:gpt-5.6-sol:high"]);
	const lines = await r.press("add");
	assert.deepEqual(r.calls.map((c) => c.args), [["task", "add", "--title", "Parser", "--class", "implement", "--files", "src/parse.go,src/parse_test.go", "--pool", "codex:gpt-5.6-sol:high", "--json"]]);
	assert.equal(keyLine(lines), '  /add: Added t9 "Parser"; it is pending.');
	// "/add task" skips the task-or-seat dialog
	const direct = rig(() => res(0, envelope("task_add", { task: { id: "t9", state: "pending" } })), ["Parser", "implement", "", ""]);
	assert.equal(keyLine(await direct.press("add", "task")), '  /add: Added t9 "Parser"; it is pending.');
	assert.ok(!direct.asked.some((a) => a.startsWith("add: a task or a seat")), direct.asked.join("\n"));
	// A refusal says what happened in words; the verb's text is below the rule.
	const bad = rig(() => res(2, "", "error: task add: class \"x\" is not one of ..."), ["task", "Parser", "implement", "", ""]);
	const badLines = await bad.press("add");
	assert.equal(keyLine(badLines), '  /add: The task was not added: class "x" is not one of ...');
	assert.equal(recLine(badLines), '  /add: task add: class "x" is not one of ...');
});

// Row 6: the reset key. On a blocked task it runs `task reset` (the lead's
// own, --me the lead); with a picked pool it runs --pool; a refusal shows
// in words, the verb's text below the rule (the re-pin rule's
// repin_after_attempt, the lead check's reset refused).
test("reset: task reset, --pool for a picked pool, and a refusal by name", async () => {
	const keep = rig(() => res(0, envelope("task_reset", { task: { id: "t1", state: "pending" } })), [T1, "keep its pool"]);
	assert.equal(keyLine(await keep.press("reset")), "  /reset: t1 reset; it goes back to the queue.");
	assert.deepEqual(keep.calls[0].args, ["task", "reset", "t1", "--me", "cto", "--json"]);
	const picked = rig(() => res(0, envelope("task_reset", {})), [T1, "claude:sonnet:medium"]);
	assert.equal(keyLine(await picked.press("reset")), "  /reset: t1 reset onto claude:sonnet:medium; it goes back to the queue.");
	assert.deepEqual(picked.calls[0].args, ["task", "reset", "t1", "--me", "cto", "--pool", "claude:sonnet:medium", "--json"]);
	const typed = rig(() => res(0, envelope("task_reset", {})), [T1, "type a pool (runtime:model:effort)", "pi:openai/gpt-6:high"]);
	await typed.press("reset");
	assert.deepEqual(typed.calls[0].args, ["task", "reset", "t1", "--me", "cto", "--pool", "pi:openai/gpt-6:high", "--json"]);
	const repin = rig(() => res(1, "", "error: task reset: repin_after_attempt: task t1 was granted attempt 2; ...\n"), [T1, "claude:sonnet:medium"]);
	const repinLines = await repin.press("reset");
	assert.equal(keyLine(repinLines), "  /reset: t1 was not reset: task t1 was granted attempt 2; ...");
	assert.equal(recLine(repinLines), "  /reset: t1: task reset: repin_after_attempt: task t1 was granted attempt 2; ...");
	const notLead = rig(() => res(1, "", "error: task reset: reset refused: task t1 belongs to qa, not cto"), [T1, "keep its pool"]);
	assert.equal(keyLine(await notLead.press("reset")), "  /reset: t1 was not reset: reset refused: task t1 belongs to qa, not cto");
	// Nothing blocked: no verb runs.
	const none = rig(() => res(1, ""), [], () => ({ ...fixture, tasks: fixture.tasks.filter((t) => t.task_state !== "blocked") }));
	assert.equal(keyLine(await none.press("reset")), "  /reset: Nothing to reset: no task is blocked.");
	assert.equal(none.calls.length, 0);
});

const gateDocForCancel: ScreenStatus = { ...fixture, gates: [{ id: "g1", action_class: "tag", target_digest: "sha256:8502aa0011223344", state: "open", raised_by: "qa" }] };

// Codex P2 2 on #831: a cancel at any dialog aborts the key with one
// "cancelled, nothing run" line and runs no verb. The add key cancelled at
// each of its dialogs (task or seat, title, class, files, pool) runs nothing; the
// reset key cancelled at the pool dialog, or at the typed pool, runs
// nothing (a reset with the old pool would return the task to dispatch).
// Reverted (a cancelled files or pool dialog read as empty), task add or
// task reset runs.
test("a cancel at any dialog aborts the key and runs no verb", async () => {
	const addAnswers: (string | undefined)[][] = [
		[undefined],
		["task", undefined],
		["task", "Parser", undefined],
		["task", "Parser", "implement", undefined],
		["task", "Parser", "implement", "src/parse.go", undefined],
	];
	for (const answers of addAnswers) {
		const r = rig(() => res(0, envelope("task_add", { task: { id: "t9", state: "pending" } })), answers);
		assert.equal(keyLine(await r.press("add")), "  /add: Cancelled; nothing was run.", JSON.stringify(answers));
		assert.equal(r.calls.length, 0, JSON.stringify(answers));
	}
	for (const answers of [[T1, undefined], [T1, "type a pool (runtime:model:effort)", undefined], [undefined]]) {
		const r = rig(() => res(0, envelope("task_reset", {})), answers);
		assert.equal(keyLine(await r.press("reset")), "  /reset: Cancelled; nothing was run.", JSON.stringify(answers));
		assert.equal(r.calls.length, 0, JSON.stringify(answers));
	}
	for (const key of ["dismiss", "answer"]) {
		const r = rig(() => res(0, ""), [undefined], () => gateDocForCancel);
		assert.equal(keyLine(await r.press(key)), `  /${key}: Cancelled; nothing was run.`);
		assert.equal(r.calls.length, 0);
	}
});

// Row 7 (the extension's half): /dismiss (the 3.1.0 clear key) on a warning line runs
// `scheduler clear` naming the seat, the warning's code and the seat's
// launch; the screen reads status again at once, and the line is gone
// (status lists it under acknowledged_warnings); the seat's own line, its
// state, is unchanged. A later launch with the same condition is a line
// again. A refusal (a stale launch) shows by name.
test("dismiss: the acknowledgement names the warning and the launch; the line leaves the screen; the seat's state does not", async () => {
	let doc: ScreenStatus = fixture;
	const scribeWarning = fixture.seats.find((s) => s.handle === "scribe")!.warnings![0];
	const r = rig(
		(args) => {
			if (args[0] === "scheduler" && args[1] === "clear") {
				doc = { ...fixture, seats: fixture.seats.map((s) => (s.handle === "scribe" ? { ...s, warnings: [], acknowledged_warnings: [scribeWarning] } : s)) };
				return res(0, envelope("scheduler_clear", { ok: true }));
			}
			return res(1, "");
		},
		["scribe  " + scribeWarning.slice(0, 97) + "..."],
		() => doc,
	);
	await r.screen.poll();
	const before = renderScreen(fixture);
	const scribeLine = before.find((l) => l.startsWith("  scribe  "))!;
	const lines = await r.press("dismiss");
	assert.deepEqual(r.calls.map((c) => c.args), [["scheduler", "clear", "--seat", "scribe", "--warning", "seat_stalled_at_dialog", "--launch", "L1", "--json"]]);
	assert.equal(keyLine(lines), "  /dismiss: Seat scribe's warning is dismissed for its current launch; the seat itself does not change.");
	assert.equal(recLine(lines), "  /dismiss: scribe seat_stalled_at_dialog acknowledged for launch L1");
	assert.ok(!lines.some((l) => l.includes("! seat_stalled_at_dialog")), lines.join("\n"));
	assert.ok(lines.includes(scribeLine), "the seat's line changed:\n" + lines.join("\n"));
	// the other seats' lines stay
	assert.ok(lines.some((l) => l.startsWith("    ! pi_provider_key_missing: ")));
	// the next launch with the same condition: a new line
	const relaunched = { ...fixture, seats: fixture.seats.map((s) => (s.handle === "scribe" ? { ...s, launch: { ...s.launch!, launch_id: "L2" } } : s)) };
	assert.ok(renderScreen(relaunched).some((l) => l.startsWith("    ! seat_stalled_at_dialog: ")));
	const stale = rig(() => res(15, envelope("scheduler_clear", { ok: false, code: "clear_launch_stale" }), "error: scheduler clear refused (clear_launch_stale): seat scribe's current launch is L2, not L1"), ["scribe  " + scribeWarning.slice(0, 97) + "..."]);
	const staleLines = await stale.press("dismiss");
	assert.equal(keyLine(staleLines), "  /dismiss: Seat scribe's warning was not dismissed: scheduler clear refused: seat scribe's current launch is L2, not L1");
	assert.equal(recLine(staleLines), "  /dismiss: scribe seat_stalled_at_dialog: scheduler clear refused (clear_launch_stale): seat scribe's current launch is L2, not L1");
	// "/dismiss SEAT" on a seat with no warning: nothing runs
	const quiet = rig(() => res(1, ""), []);
	assert.equal(keyLine(await quiet.press("dismiss", "pi-dev-2")), "  /dismiss: Nothing to dismiss: seat pi-dev-2 has no warning showing.");
	assert.equal(quiet.calls.length, 0);
});

// The four typed gates (tag and release, raised as tag and github_release;
// external send; credential change; growth, the spawn action), one open
// each, for rows 8 and 9.
const KINDS: [string, string, string][] = [
	["tag", "release", "git tag v3.1.0"],
	["github_release", "release", "gh release create v3.1.0"],
	["external_send", "send", "mail to the owner's list"],
	["credential_change", "credential", "rotate the deploy key"],
	["spawn", "grow", "seat qa over max_seats"],
];
const gateDoc: ScreenStatus = {
	...fixture,
	gates: KINDS.map(([action], i) => ({ id: `g${i + 1}`, action_class: action, target_digest: `sha256:${String(i + 1).repeat(16)}aa`, state: "open", raised_by: "cto" })),
};
function gateRow(i: number, state = "open", questionSent = true) {
	const [action, typed, target] = KINDS[i];
	return { gate_id: `g${i + 1}`, class: action, typed_class: typed, target, digest: `sha256:${String(i + 1).repeat(16)}aa`, state, question_sent: questionSent };
}

// Row 9 (Q7 answered no): each of the four typed gate kinds shows "waiting
// on Buzz" on the screen with its 16-hex prefix, and the answer key on each
// says so and writes no answer: the only verbs it runs are gate status (and
// gate post for a question not yet on Buzz), never gate answer.
test("answer, Q7 no: every typed gate kind waits on Buzz and no terminal answer is written", async () => {
	const lines = renderScreen(gateDoc);
	const rule = lines.indexOf("----------");
	const gates = lines.slice(rule + 1).filter((l) => /^ {2}g\d/.test(l));
	assert.equal(gates.length, KINDS.length);
	KINDS.forEach(([action], i) => {
		assert.match(gates[i], new RegExp(`^  g${i + 1}  ${action}  ${String(i + 1).repeat(16)}  age .*  waiting on Buzz$`));
		// above the rule: the gate in words, no id and no prefix
		assert.ok(lines.slice(0, rule).includes(`  an owner gate (${action.replace(/_/g, " ")}) is open  (answer on the phone; /answer shows it)`), lines.join("\n"));
	});
	for (let i = 0; i < KINDS.length; i++) {
		const r = rig((args) => (args[0] === "gate" && args[1] === "status" ? res(0, envelope("gate_status", [gateRow(i)])) : res(1, "", "error: unexpected")), [`an owner gate (${KINDS[i][0].replace(/_/g, " ")}) is open`], () => gateDoc);
		await r.screen.poll();
		const out = await r.press("answer");
		assert.equal(keyLine(out), `  /answer: Waiting on your answer on the phone to "the ${KINDS[i][1]} gate": reply yes or no in the question's Buzz thread; the terminal cannot answer a typed gate.`);
		assert.equal(recLine(out), `  /answer: g${i + 1} ${KINDS[i][0]} (${KINDS[i][1]}) ${KINDS[i][2]} prefix ${String(i + 1).repeat(16)}`);
		assert.deepEqual(r.calls.map((c) => c.args), [["gate", "status", "--gate", `g${i + 1}`, "--json"]]);
		assert.ok(r.notes.some((n) => n.includes("Buzz thread")));
	}
});

// Row 8 (its Q7-no form): a question that has not reached Buzz is posted
// there by the key (gate post, which sends it once); a gate answered over
// Buzz is closed on the screen (only open gates are listed), and the answer
// key on it, read fresh, is refused by its state with no verb after it; a
// failed post shows the verb's refusal. A model tool call cannot write an
// answer: no tool carries the verb (row 2).
test("answer, Q7 no: an unposted question is posted over Buzz; a gate answered over Buzz is closed and refused", async () => {
	const G1 = "an owner gate (tag) is open";
	const post = rig((args) => (args[1] === "status" ? res(0, envelope("gate_status", [gateRow(0, "open", false)])) : res(0, envelope("gate_post", { event_id: "e1" }))), [G1], () => gateDoc);
	await post.screen.poll();
	assert.match(keyLine(await post.press("answer")), /: the question was posted over Buzz; reply yes or no/);
	assert.deepEqual(post.calls.map((c) => c.args), [["gate", "status", "--gate", "g1", "--json"], ["gate", "post", "--gate", "g1", "--json"]]);
	// Answered over Buzz since the screen last read status: refused by state.
	const answered = rig((args) => (args[1] === "status" ? res(0, envelope("gate_status", [gateRow(0, "accepted")])) : res(0, "")), [G1], () => gateDoc);
	await answered.screen.poll();
	const answeredLines = await answered.press("answer");
	assert.equal(keyLine(answeredLines), "  /answer: That gate is no longer open: it is accepted, since an answer at either door closes it.");
	assert.equal(recLine(answeredLines), "  /answer: g1: gate_not_open (accepted)");
	assert.deepEqual(answered.calls.map((c) => c.args), [["gate", "status", "--gate", "g1", "--json"]]);
	// The screen lists open gates only: once status says accepted, g1 is gone.
	const closed = renderScreen({ ...gateDoc, gates: gateDoc.gates.map((g) => (g.id === "g1" ? { ...g, state: "accepted" } : g)) });
	assert.ok(!closed.some((l) => l.startsWith("  g1  ")), closed.join("\n"));
	const failedPost = rig((args) => (args[1] === "status" ? res(0, envelope("gate_status", [gateRow(0, "open", false)])) : res(1, "", "error: gate post: no enrolled share for the binding")), [G1], () => gateDoc);
	await failedPost.screen.poll();
	const failedLines = await failedPost.press("answer");
	assert.equal(keyLine(failedLines), '  /answer: The question "the release gate" did not reach the phone: no enrolled share for the binding');
	assert.match(recLine(failedLines), /the question was not posted over Buzz: gate post: no enrolled share for the binding$/);
});

test("an unknown key, an old /squad subcommand, or a key without the interactive screen, runs nothing", async () => {
	const r = rig(() => res(0, ""), []);
	await r.screen.poll();
	const lines = await r.press("squad", "clear");
	assert.equal(keyLine(lines), "  /squad: There is no /squad clear any more: each action is its own command. The commands you can use now: /add /reset /dismiss /answer /widen /team hide (/squad goes in 3.2)");
	assert.equal(r.calls.length, 0);
	const keys = new Keys(cfg(), async () => res(0, ""));
	const unknown = await keys.press("foo", fakeCtx({ hasUI: true }), fixture);
	assert.equal(unknown.line, "There is no /foo. The commands you can use now are listed on the screen's last line.");
	assert.equal(unknown.record, "unknown_command: foo");
	const noUI = await keys.press("add", fakeCtx({ hasUI: false }), fixture);
	assert.equal(noUI.line, "/add needs the lead's interactive screen; this pi has none.");
	assert.equal(refusal({ code: null, stdout: "", stderr: "", timedOut: false, spawnError: "ENOENT", ms: 0 }), "could not start: ENOENT");
});

// Brief 3.1.1-1, row 4, in 3.1.1-3's form: the screen's waiting-on-you
// block shows the gate question's first plain line, the ids below the
// rule; the answer key's notice shows the text above the rule verbatim
// (the lines the owner reads on the phone), then the identifiers below.
// Reverted (the 3.1.0 identifiers only), neither shows the question.
test("brief 3.1.1-1: the waiting block and the answer key show the question above the rule", async () => {
	const plain = "Create a tag for v3.1.0-rc.1 in amq-squad?\nThe prepare found its commit on v3.\nYes creates the tag. No does nothing.";
	const text = `${plain}\n\n----------\namq-squad gate g1\nclass: release (tag)\ntarget: git tag v3.1.0-rc.1 4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d on omriariav/amq-squad\ndigest: sha256:8502aa0011223344`;
	const doc = { ...fixture, gates: [{ ...fixture.gates[0], question: plain }, fixture.gates[1]] } as ScreenStatus;
	const lines = renderScreen(doc, new Date("2026-11-01T09:10:00Z"));
	const at = lines.indexOf("waiting on you (1)");
	assert.equal(lines[at + 1], "  Create a tag for v3.1.0-rc.1 in amq-squad?  (answer on the phone; /answer shows it)", lines.join("\n"));
	const rule = lines.indexOf("----------");
	assert.ok(lines.slice(rule).some((l) => /^ {2}g1 {2}tag {2}8502aa0011223344 /.test(l)), lines.join("\n"));
	const r = rig(
		(args) => res(0, envelope("gate_status", [{ gate_id: "g1", class: "tag", typed_class: "release", target: "git tag v3.1.0-rc.1", digest: "sha256:8502aa0011223344", state: "open", question_sent: true, text }])),
		["Create a tag for v3.1.0-rc.1 in amq-squad?"],
		() => doc,
	);
	await r.press("answer");
	const note = r.notes.find((n) => n.includes("Buzz thread")) ?? "";
	assert.ok(note.startsWith(plain + "\n"), note);
	assert.doesNotMatch(note.slice(0, plain.length), /[0-9a-f]{8,}|g1/);
});

// Row 8 (brief 3.1.1-3): /widen shows the paths the seat's typed widen
// request recorded and the seat's reason, from the task's own record, in
// pi's confirm dialog; only the dialog's yes runs task widen, with exactly
// those paths as --path each; a no runs nothing; several requests: the
// owner picks one first; none: a plain refusal and no verb.
const WIDEN_DOC: ScreenStatus = {
	...fixture,
	tasks: [
		...fixture.tasks.filter((t) => t.id !== "t1"),
		{ ...fixture.tasks[0], id: "t5", title: "The helper", block_reason: "widen_requested" },
		{ ...fixture.tasks[0], id: "t6", title: "The docs", block_reason: "widen_requested" },
	],
};
const showOf = (id: string, paths: string[], reason: string) => envelope("task_show", { task: { id, title: id === "t5" ? "The helper" : "The docs", state: "blocked", block_reason: "widen_requested", block_detail: reason, refused_paths: paths } });

test("widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen", async () => {
	const one: ScreenStatus = { ...WIDEN_DOC, tasks: WIDEN_DOC.tasks.filter((t) => t.id !== "t6") };
	const reply = (args: string[]) => (args[1] === "show" ? res(0, showOf(args[2], ["src/helper.go", "docs/a, b.md"], "the parser needs the shared helper")) : res(0, envelope("task_widen", { ok: true })));
	const yes = rig(reply, [], () => one, true);
	const lines = await yes.press("widen");
	assert.deepEqual(yes.calls.map((c) => c.args), [
		["task", "show", "t5", "--json"],
		["task", "widen", "t5", "--path", "src/helper.go", "--path", "docs/a, b.md", "--reason", WIDEN_REASON, "--json"],
	]);
	assert.equal(yes.confirms.length, 1);
	assert.equal(yes.confirms[0].title, "widen t5?");
	for (const want of ["t5: The helper", "Its seat asks to touch these files outside its task:", "  src/helper.go", "  docs/a, b.md", "The seat's reason: the parser needs the shared helper"]) {
		assert.ok(yes.confirms[0].message.split("\n").includes(want), `the dialog lacks ${JSON.stringify(want)}:\n${yes.confirms[0].message}`);
	}
	assert.equal(keyLine(lines), "  /widen: t5 widened by 2 paths: src/helper.go, docs/a, b.md.");
	// the dialog's no: task show only, no widen
	const no = rig(reply, [], () => one, false);
	assert.equal(keyLine(await no.press("widen")), "  /widen: Cancelled; nothing was widened.");
	assert.deepEqual(no.calls.map((c) => c.args), [["task", "show", "t5", "--json"]]);
	// two requests: the owner picks one, then the dialog
	const two = rig(reply, ["t6: The docs"], () => WIDEN_DOC, true);
	await two.press("widen");
	assert.ok(two.asked.some((a) => a.startsWith("widen: which task [t5: The helper | t6: The docs]")), two.asked.join("\n"));
	assert.deepEqual(two.calls.map((c) => c.args[2]), ["t6", "t6"]);
	assert.equal(two.confirms.length, 1);
	// none blocked with a widen request: refused in words, nothing runs
	const none = rig(reply, [], () => ({ ...fixture, tasks: fixture.tasks.filter((t) => t.task_state !== "blocked") }));
	assert.equal(keyLine(await none.press("widen")), "  /widen: Nothing to widen: no task is blocked with a widen request.");
	assert.equal(none.calls.length, 0);
	// a task blocked for another reason is no widen request
	const other = rig(reply, [], () => ({ ...fixture, tasks: [{ ...fixture.tasks[0], block_reason: "verdict_failed" }] }));
	assert.equal(keyLine(await other.press("widen")), "  /widen: Nothing to widen: no task is blocked with a widen request.");
	assert.equal(other.calls.length, 0);
	// a record with no recorded paths: refused, no widen
	const empty = rig((args) => (args[1] === "show" ? res(0, showOf("t5", [], "x")) : res(0, "")), [], () => one, true);
	assert.equal(keyLine(await empty.press("widen")), "  /widen: t5 has no widen request to accept: its block names no paths, or it is no longer blocked for them.");
	assert.deepEqual(empty.calls.map((c) => c.args[1]), ["show"]);
	assert.equal(empty.confirms.length, 0);
	// the verb's refusal: in words, its text below the rule
	const refused = rig((args) => (args[1] === "show" ? res(0, showOf("t5", ["src/helper.go"], "r")) : res(15, "", "error: task widen t5 was refused: the widened set overlaps another active task's files\n\n----------\nrefused (file_lock_conflict): src/helper.go is t7's\n")), [], () => one, true);
	const refusedLines = await refused.press("widen");
	assert.equal(keyLine(refusedLines), "  /widen: t5 was not widened: task widen t5 was refused: the widened set overlaps another active task's files");
	// the verb's code, printed below its own rule, is below the screen's
	assert.equal(recLine(refusedLines), "  /widen: t5: task widen t5 was refused: the widened set overlaps another active task's files (refused (file_lock_conflict): src/helper.go is t7's)");
});
