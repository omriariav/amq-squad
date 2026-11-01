// Brief 31-2, PR 1: the keys on the lead's screen (add, reset, clear,
// answer), pressed as `/squad <key>` against the stand-in pi. Every verb a
// key runs goes through the injected runner, so the test sees each argv and
// answers it; nothing reaches the real binary, AMQ, tmux or Buzz.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { installScreen, renderScreen, type ScreenStatus } from "../extensions/lib/screen.ts";
import { refusal } from "../extensions/lib/keys.ts";
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

// rig is the lead's screen with a scripted runner and scripted dialogs: the
// status verb answers the current document; every other verb is answered by
// reply (default: refuse, so an unexpected verb shows).
function rig(reply: (args: string[]) => RunResult, answers: (string | undefined)[], status: () => ScreenStatus = () => fixture) {
	const calls: Call[] = [];
	const runner = async (bin: string, args: string[], _o: RunOptions): Promise<RunResult> => {
		if (args[0] === "status") return res(0, JSON.stringify(status()));
		calls.push({ bin, args });
		return reply(args);
	};
	const pi = new FakePi();
	const screen = installScreen(pi as any, cfg(), runner, () => ({ mtimeMs: 1, size: 1 }));
	const queue = [...answers];
	const asked: string[] = [];
	const notes: string[] = [];
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
	ctx.ui.notify = (m: string) => {
		notes.push(m);
	};
	const press = async (key: string) => {
		const cmd = pi.commands.get("squad");
		assert.ok(cmd, "no /squad command");
		await cmd.handler(key, ctx);
		return ctx.widgets["amq-squad-screen"]?.lines ?? [];
	};
	return { pi, screen, ctx, calls, asked, notes, press };
}

function keyLine(lines: string[]): string {
	return lines.find((l) => l.startsWith("  key ")) ?? `(no key line in\n${lines.join("\n")})`;
}

// Row 1 (the extension's half): every key runs its verb from the lead's own
// process: the extension's runner with the configured amq-squad binary
// itself, no shell, no tmux, no other process between. The clear, the key
// that is a scheduler decision, is proved under R4 because that process
// descends from the proved lead (cli TestClearKeyIsProvedFromTheLeadsDescendant).
// Reverted (a key that sends its verb through another process), the bin
// differs.
test("each key runs its verb with the lead's own amq-squad binary from the lead's process", async () => {
	const seen: Call[] = [];
	for (const [key, answers] of [
		["add", ["task", "Parser", "implement", "src/parse.go", ""]],
		["reset", ["t1  outside_declared_files", "keep its pool"]],
		["clear", [fixture.seats[0].handle + "  " + fixture.seats[0].warnings![0].slice(0, 97) + "..."]],
		["answer", ["g1  tag  8502aa0011223344"]],
	] as [string, string[]][]) {
		const r = rig((args) => (args[0] === "gate" ? res(0, envelope("gate_status", [{ gate_id: "g1", class: "tag", typed_class: "release", target: "git tag v1.2.0", digest: "sha256:8502aa0011223344", state: "open", question_sent: true }])) : res(0, envelope("x", { task: { id: "t9", state: "pending" } }))), answers);
		const lines = await r.press(key);
		assert.ok(!keyLine(lines).includes("refused"), `${key}: ${keyLine(lines)}`);
		seen.push(...r.calls);
	}
	assert.ok(seen.length >= 4);
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
	assert.ok(lead.commands.has("squad"), "the keys are the /squad command, not tools");
});

// Row 3: the add key. The owner types the title, picks the class, types
// the declared files and the pool; the key runs `task add` with exactly
// them (cli TestAddKeysArgvAddsTheTaskWithItsFilesAndPool runs this argv
// against a store and finds the task with its files and pool).
test("add: the task goes to task add with its title, class, declared files and pool", async () => {
	const r = rig(() => res(0, envelope("task_add", { root: "/r", task: { id: "t9", state: "pending" } })), ["task", "Parser", "implement", "src/parse.go, src/parse_test.go", "codex:gpt-5.6-sol:high"]);
	const lines = await r.press("add");
	assert.deepEqual(r.calls.map((c) => c.args), [["task", "add", "--title", "Parser", "--class", "implement", "--files", "src/parse.go,src/parse_test.go", "--pool", "codex:gpt-5.6-sol:high", "--json"]]);
	assert.equal(keyLine(lines), '  key add: added t9 "Parser" (pending)');
	// A refusal shows as the verb printed it.
	const bad = rig(() => res(2, "", "error: task add: class \"x\" is not one of ..."), ["task", "Parser", "implement", "", ""]);
	assert.equal(keyLine(await bad.press("add")), '  key add refused: task add: class "x" is not one of ...');
});

// Row 6: the reset key. On a blocked task it runs `task reset` (the lead's
// own, --me the lead); with a picked pool it runs --pool; a refusal shows
// on the screen by its name as the verb printed it (the re-pin rule's
// repin_after_attempt, the lead check's reset refused).
test("reset: task reset, --pool for a picked pool, and a refusal by name", async () => {
	const keep = rig(() => res(0, envelope("task_reset", { task: { id: "t1", state: "pending" } })), ["t1  outside_declared_files", "keep its pool"]);
	assert.equal(keyLine(await keep.press("reset")), "  key reset: t1 reset");
	assert.deepEqual(keep.calls[0].args, ["task", "reset", "t1", "--me", "cto", "--json"]);
	const picked = rig(() => res(0, envelope("task_reset", {})), ["t1  outside_declared_files", "claude:sonnet:medium"]);
	assert.equal(keyLine(await picked.press("reset")), "  key reset: t1 reset onto claude:sonnet:medium");
	assert.deepEqual(picked.calls[0].args, ["task", "reset", "t1", "--me", "cto", "--pool", "claude:sonnet:medium", "--json"]);
	const typed = rig(() => res(0, envelope("task_reset", {})), ["t1  outside_declared_files", "type a pool (runtime:model:effort)", "pi:openai/gpt-6:high"]);
	await typed.press("reset");
	assert.deepEqual(typed.calls[0].args, ["task", "reset", "t1", "--me", "cto", "--pool", "pi:openai/gpt-6:high", "--json"]);
	const repin = rig(() => res(1, "", "error: task reset: repin_after_attempt: task t1 was granted attempt 2; ...\n"), ["t1  outside_declared_files", "claude:sonnet:medium"]);
	assert.equal(keyLine(await repin.press("reset")), "  key reset refused: t1: task reset: repin_after_attempt: task t1 was granted attempt 2; ...");
	const notLead = rig(() => res(1, "", "error: task reset: reset refused: task t1 belongs to qa, not cto"), ["t1  outside_declared_files", "keep its pool"]);
	assert.match(keyLine(await notLead.press("reset")), /refused: t1: task reset: reset refused: task t1 belongs to qa, not cto$/);
	// Nothing blocked: no verb runs.
	const none = rig(() => res(1, ""), [], () => ({ ...fixture, tasks: fixture.tasks.filter((t) => t.task_state !== "blocked") }));
	assert.equal(keyLine(await none.press("reset")), "  key reset refused: no blocked task");
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
		assert.equal(keyLine(await r.press("add")), "  key add: cancelled, nothing run", JSON.stringify(answers));
		assert.equal(r.calls.length, 0, JSON.stringify(answers));
	}
	for (const answers of [["t1  outside_declared_files", undefined], ["t1  outside_declared_files", "type a pool (runtime:model:effort)", undefined], [undefined]]) {
		const r = rig(() => res(0, envelope("task_reset", {})), answers);
		assert.equal(keyLine(await r.press("reset")), "  key reset: cancelled, nothing run", JSON.stringify(answers));
		assert.equal(r.calls.length, 0, JSON.stringify(answers));
	}
	for (const key of ["clear", "answer"]) {
		const r = rig(() => res(0, ""), [undefined], () => gateDocForCancel);
		assert.equal(keyLine(await r.press(key)), `  key ${key}: cancelled, nothing run`);
		assert.equal(r.calls.length, 0);
	}
});

// Row 7 (the extension's half): the clear key on a warning line runs
// `scheduler clear` naming the seat, the warning's code and the seat's
// launch; the screen reads status again at once, and the line is gone
// (status lists it under acknowledged_warnings); the seat's own line, its
// state, is unchanged. A later launch with the same condition is a line
// again. A refusal (a stale launch) shows by name.
test("clear: the acknowledgement names the warning and the launch; the line leaves the screen; the seat's state does not", async () => {
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
	const lines = await r.press("clear");
	assert.deepEqual(r.calls.map((c) => c.args), [["scheduler", "clear", "--seat", "scribe", "--warning", "seat_stalled_at_dialog", "--launch", "L1", "--json"]]);
	assert.equal(keyLine(lines), "  key clear: scribe seat_stalled_at_dialog acknowledged for launch L1");
	assert.ok(!lines.some((l) => l.includes("! seat_stalled_at_dialog")), lines.join("\n"));
	assert.ok(lines.includes(scribeLine), "the seat's line changed:\n" + lines.join("\n"));
	// the other seats' lines stay
	assert.ok(lines.some((l) => l.startsWith("    ! pi_provider_key_missing: ")));
	// the next launch with the same condition: a new line
	const relaunched = { ...fixture, seats: fixture.seats.map((s) => (s.handle === "scribe" ? { ...s, launch: { ...s.launch!, launch_id: "L2" } } : s)) };
	assert.ok(renderScreen(relaunched).some((l) => l.startsWith("    ! seat_stalled_at_dialog: ")));
	const stale = rig(() => res(15, envelope("scheduler_clear", { ok: false, code: "clear_launch_stale" }), "error: scheduler clear refused (clear_launch_stale): seat scribe's current launch is L2, not L1"), ["scribe  " + scribeWarning.slice(0, 97) + "..."]);
	assert.equal(keyLine(await stale.press("clear")), "  key clear refused: scribe seat_stalled_at_dialog: scheduler clear refused (clear_launch_stale): seat scribe's current launch is L2, not L1");
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
	const gates = renderScreen(gateDoc).filter((l) => /^ {2}g\d/.test(l));
	assert.equal(gates.length, KINDS.length);
	KINDS.forEach(([action], i) => {
		assert.match(gates[i], new RegExp(`^  g${i + 1}  ${action}  ${String(i + 1).repeat(16)}  age .*  waiting on Buzz$`));
	});
	for (let i = 0; i < KINDS.length; i++) {
		const r = rig((args) => (args[0] === "gate" && args[1] === "status" ? res(0, envelope("gate_status", [gateRow(i)])) : res(1, "", "error: unexpected")), [`g${i + 1}  ${KINDS[i][0]}  ${String(i + 1).repeat(16)}`], () => gateDoc);
		await r.screen.poll();
		const line = keyLine(await r.press("answer"));
		assert.match(line, new RegExp(`^  key answer: g${i + 1} ${KINDS[i][0]} \\(${KINDS[i][1]}\\) ${KINDS[i][2]} prefix ${String(i + 1).repeat(16)}: waiting on Buzz: the terminal door is not built \\(Q7\\)`));
		assert.deepEqual(r.calls.map((c) => c.args), [["gate", "status", "--gate", `g${i + 1}`, "--json"]]);
		assert.ok(r.notes.some((n) => n.includes("waiting on Buzz")));
	}
});

// Row 8 (its Q7-no form): a question that has not reached Buzz is posted
// there by the key (gate post, which sends it once); a gate answered over
// Buzz is closed on the screen (only open gates are listed), and the answer
// key on it, read fresh, is refused by its state with no verb after it; a
// failed post shows the verb's refusal. A model tool call cannot write an
// answer: no tool carries the verb (row 2).
test("answer, Q7 no: an unposted question is posted over Buzz; a gate answered over Buzz is closed and refused", async () => {
	const post = rig((args) => (args[1] === "status" ? res(0, envelope("gate_status", [gateRow(0, "open", false)])) : res(0, envelope("gate_post", { event_id: "e1" }))), ["g1  tag  1111111111111111"], () => gateDoc);
	await post.screen.poll();
	assert.match(keyLine(await post.press("answer")), /: question posted over Buzz; waiting on Buzz: /);
	assert.deepEqual(post.calls.map((c) => c.args), [["gate", "status", "--gate", "g1", "--json"], ["gate", "post", "--gate", "g1", "--json"]]);
	// Answered over Buzz since the screen last read status: refused by state.
	const answered = rig((args) => (args[1] === "status" ? res(0, envelope("gate_status", [gateRow(0, "accepted")])) : res(0, "")), ["g1  tag  1111111111111111"], () => gateDoc);
	await answered.screen.poll();
	assert.equal(keyLine(await answered.press("answer")), "  key answer refused: g1: gate_not_open: the gate is accepted; an answer at either door closes it");
	assert.deepEqual(answered.calls.map((c) => c.args), [["gate", "status", "--gate", "g1", "--json"]]);
	// The screen lists open gates only: once status says accepted, g1 is gone.
	const closed = renderScreen({ ...gateDoc, gates: gateDoc.gates.map((g) => (g.id === "g1" ? { ...g, state: "accepted" } : g)) });
	assert.ok(!closed.some((l) => l.startsWith("  g1  ")), closed.join("\n"));
	const failedPost = rig((args) => (args[1] === "status" ? res(0, envelope("gate_status", [gateRow(0, "open", false)])) : res(1, "", "error: gate post: no enrolled share for the binding")), ["g1  tag  1111111111111111"], () => gateDoc);
	await failedPost.screen.poll();
	assert.match(keyLine(await failedPost.press("answer")), /refused: .*the question was not posted over Buzz: gate post: no enrolled share for the binding$/);
});

test("an unknown key, or a key without the interactive screen, runs nothing", async () => {
	const r = rig(() => res(0, ""), []);
	await r.screen.poll();
	assert.equal(keyLine(await r.press("widen")), '  key widen refused: unknown key "widen"; the keys are add, reset, clear, answer');
	assert.equal(r.calls.length, 0);
	assert.equal(refusal({ code: null, stdout: "", stderr: "", timedOut: false, spawnError: "ENOENT", ms: 0 }), "could not start: ENOENT");
});
