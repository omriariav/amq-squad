// Brief 3.1.1-3, rows 3, 4 and 7: the screen in plain words. One table maps
// every wait and block reason the scheduler and the task store can emit (Go's
// reason constants, read here from the Go sources at test time) and every
// seat reason code; no refusal or outcome line above its rule carries a
// snake_case code or a hex run; free-text titles pass the redaction and a
// control-character strip before the cut; each empty block shows one plain
// line.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { renderScreen, Screen, type ScreenStatus } from "../extensions/lib/screen.ts";
import { Keys, type KeyOutcome } from "../extensions/lib/keys.ts";
import { refusedPaths } from "../extensions/lib/tools.ts";
import { SoloTeam } from "../extensions/lib/team.ts";
import { BLOCK_WORDS, blockWords, clean, codeIn, RULE, SEAT_DOING, seatDoing, TITLE_MAX, WAIT_WORDS, waitWords } from "../extensions/lib/words.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import type { RunResult } from "../extensions/lib/verbs.ts";
import { fakeCtx, tmpdir } from "./fakepi.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(here, "..", "..");
const fixture = JSON.parse(fs.readFileSync(path.join(here, "status-fixture.json"), "utf8")) as ScreenStatus;
const now = new Date("2026-11-01T09:05:00Z");

function go(rel: string): string {
	return fs.readFileSync(path.join(REPO, rel), "utf8");
}

// constants is every `NAME = "value"` in a Go file whose name matches.
function constants(rel: string, name: RegExp): string[] {
	const out: string[] = [];
	for (const m of go(rel).matchAll(/^\s*(?:const\s+)?(\w+)\s*=\s*"([a-z][a-z0-9_]*)"/gm)) if (name.test(m[1])) out.push(m[2]);
	return out;
}

// literals is every `<field>: "value"` (a Route{Block: ...}, a stage) in a
// Go file.
function literals(rel: string, field: string): string[] {
	return [...go(rel).matchAll(new RegExp(`${field}\\s*[:=]\\s*"([a-z][a-z0-9_]*)"`, "g"))].map((m) => m[1]);
}

// The wait reasons of a pending task: internal/task's Wait* and the
// scheduler's no_roster_seat (3.1.1-2).
function goWaits(): string[] {
	return [...constants("internal/task/class.go", /^Wait/), ...constants("internal/scheduler/scheduler.go", /^WaitNoRosterSeat$/)];
}

// The block reasons: the completion gate's (internal/task), the typed widen
// and growth gate (task.go), grants_invalid (verdict.go), the PR binding
// (scope.go); the commit step's (internal/seatcommit); the scheduler's
// (hooks.go, roster.go); the router's (internal/routing, its Route{Block}
// literals); the pin check (internal/registry); a repeated launch failure's
// stage (internal/seatlaunch run.go and roster.go, the failed stages; the
// stages "prepared" and "applied" are successes and never a block).
function goBlocks(): string[] {
	const stages = [...literals("internal/seatlaunch/run.go", "res.Stage"), ...literals("internal/seatlaunch/roster.go", "Stage"), ...literals("internal/scheduler/roster.go", "Stage")].filter((s) => s !== "prepared" && s !== "applied");
	return [
		...constants("internal/task/class.go", /^Block/),
		...constants("internal/task/task.go", /^Reason/),
		...constants("internal/task/verdict.go", /^Reason/),
		...constants("internal/task/scope.go", /^RefusalPRBoundElsewhere$/),
		...["policy.go", "commit.go", "ledger.go", "verifier.go"].flatMap((f) => constants(`internal/seatcommit/${f}`, /^Reason/)),
		...constants("internal/scheduler/hooks.go", /^Reason/),
		...constants("internal/scheduler/roster.go", /^(Reason|AMQBindingPresent$)/),
		...constants("internal/routing/canon.go", /^Block/),
		...constants("internal/routing/router.go", /^Block/),
		...constants("internal/routing/outcome.go", /^HistoryTampered$/),
		...literals("internal/routing/router.go", "Block"),
		...literals("internal/scheduler/hooks.go", "Block"),
		...constants("internal/registry/pins.go", /^PinMismatch$/),
		...stages,
	];
}

// The seat reason codes (internal/status classifySeat).
function goSeatCodes(): string[] {
	const src = go("internal/status/classify.go");
	const fn = src.slice(src.indexOf("func classifySeat"), src.indexOf("\nfunc ", src.indexOf("func classifySeat") + 1));
	return [...fn.matchAll(/ReasonCode = State\w+, "([a-z_]+)"/g)].map((m) => m[1]);
}

// Row 7: ONE table maps every wait and block reason the scheduler and the
// task store can emit to plain words; a reason the table lacks fails here,
// by name. Reverted (a reason dropped from the table), its row fails.
test("row 7: every wait and block reason in Go's sources maps to plain words in the one table", () => {
	const waits = goWaits();
	const blocks = goBlocks();
	const seats = goSeatCodes();
	assert.ok(waits.length >= 12, `the wait reasons were not found: ${waits}`);
	assert.ok(blocks.length >= 35, `the block reasons were not found: ${blocks}`);
	assert.ok(seats.length >= 8, `the seat codes were not found: ${seats}`);
	for (const r of waits) assert.ok(r in WAIT_WORDS, `wait reason ${r} has no words`);
	for (const r of blocks) assert.ok(r in BLOCK_WORDS, `block reason ${r} has no words`);
	for (const r of seats) assert.ok(r in SEAT_DOING, `seat reason code ${r} has no words`);
	// each mapped reason's words are plain: no code, no hex run
	const t = { id: "t9", task_state: "pending", depends_on: ["t8"], wait_holders: ["t7"], wait_pool: "claude:opus:high" };
	for (const r of Object.keys(WAIT_WORDS)) assert.equal(codeIn(waitWords({ ...t, wait_reason: r }, { max_seats: 3 }).line), "", r);
	for (const r of Object.keys(BLOCK_WORDS)) assert.equal(codeIn(blockWords({ id: "t9", task_state: "blocked", block_reason: r }).line), "", r);
	// an unmapped reason at run time: "waiting" or "blocked", the code below
	const w = waitWords({ id: "t9", task_state: "pending", wait_reason: "made_up_wait" });
	assert.deepEqual(w, { line: "t9 waiting", record: "t9 waits: made_up_wait" });
	const b = blockWords({ id: "t9", task_state: "blocked", block_reason: "needs_decision" });
	assert.deepEqual(b, { line: "t9 blocked; its reason is below the line", record: "t9 blocked: needs_decision" });
	assert.deepEqual(seatDoing({ handle: "dev", state: "online", reason_code: "made_up_code" }, undefined), { text: "waiting", record: "dev: made_up_code" });
	// the lead's hold
	assert.equal(blockWords({ id: "t9", task_state: "blocked", block_reason: "hold" }).line, "t9 held by the lead");
});

// Row 7: the size cap's words are composed from the commit step's recorded
// numbers (its block_detail, read from the task's own record), never
// guessed; the Go format the words read is the commit step's own.
test("row 7: the size cap's numbers come from the recorded detail through task show", async () => {
	const src = go("internal/seatcommit/commit.go");
	assert.ok(src.includes('"size_cap_files: %d changed files over the cap of %d"') && src.includes('"size_cap_lines: %d changed lines over the cap of %d"'), "the commit step's size cap detail changed form");
	const t27 = { ...fixture.tasks[0], id: "t27", block_reason: "size_cap_exceeded", updated_at: "2026-11-01T09:04:30Z" };
	const doc = { ...fixture, tasks: [t27] };
	const calls: string[][] = [];
	const runner = async (_b: string, args: string[]): Promise<RunResult> => {
		calls.push(args);
		const out = args[0] === "status" ? JSON.stringify(doc) : JSON.stringify({ data: { task: { id: "t27", block_detail: "size_cap_exceeded (size_cap_files: 16 changed files over the cap of 8)" } } });
		return { code: 0, stdout: out, stderr: "", timedOut: false, spawnError: "", ms: 1 };
	};
	const cfg: Config = { bin: "/opt/amq-squad", amq: "", me: "cto", root: "/r", session: "s", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: true };
	const screen = new Screen(cfg, runner, () => ({ mtimeMs: 1, size: 1 }));
	await screen.poll();
	const lines = renderScreen(screen.last!, now, undefined, screen.details);
	assert.ok(lines.includes("  t27 blocked: the commit step refused it, 16 files over the cap of 8; ask the owner to raise the cap or split it"), lines.join("\n"));
	assert.deepEqual(calls[1], ["task", "show", "t27", "--json"]);
	// read once per change of the task
	screen.pollKeySeen = undefined;
	await screen.poll();
	assert.equal(calls.filter((c) => c[0] === "task").length, 1);
	// no detail on record: no numbers are made up
	assert.ok(renderScreen(doc, now).includes("  t27 blocked: the commit step refused it, the diff is over the size cap; ask the owner to raise the cap or split it"));
	// the lines form and both halves
	assert.match(BLOCK_WORDS.size_cap_exceeded({ block_detail: "size_cap_lines: 900 changed lines over the cap of 400; size_cap_files: 16 changed files over the cap of 8" })[0], /16 files over the cap of 8 and 900 lines over the cap of 400$/);
});

// Row 7 (#859): status composes the dependency-merge wait in words from the
// recorded id and PR number; the screen shows it verbatim. When the Go
// sources carry the form, its words are the ones the screen matches.
test("row 7: the dependency-merge wait shows verbatim", () => {
	const src = go("internal/task/class.go");
	if (src.includes("dependencyMergeWaitPrefix")) {
		assert.ok(src.includes('dependencyMergeWaitPrefix = "waiting for "') && src.includes('dependencyMergeWaitSuffix = " to merge"'), "the dependency-merge wait changed form");
	}
	assert.equal(waitWords({ id: "t29", task_state: "pending", wait_reason: "waiting for t27's PR #812 to merge" }).line, "t29 waiting for t27's PR #812 to merge");
});

// Row 3: a seat with a lease shows "working on <id>: <title>"; an idle seat
// "idle"; a waiting task its plain reason; a blocked task what happened and
// what to do. Reverted (the 3.1.0 state/reason_code columns), none shows.
test("row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do", () => {
	const doc: ScreenStatus = {
		...fixture,
		seats: [
			{ ...fixture.seats[1], handle: "reviewer", reason_code: "lease_claimed", state: "online", task_id: "t28", warnings: [] },
			{ ...fixture.seats[0], handle: "dev", reason_code: "idle", state: "online", task_id: "", warnings: [] },
			{ ...fixture.seats[0], handle: "new", reason_code: "no_signal", state: "unknown", task_id: "", warnings: [], launch: { ...fixture.seats[0].launch!, outcome: "staged" } },
			{ ...fixture.seats[0], handle: "old", reason_code: "no_live_signal", state: "stale", task_id: "", warnings: [] },
		],
		tasks: [
			{ ...fixture.tasks[1], id: "t28", title: "Review the parser", lease_holder: "reviewer", pr_number: 0 },
			{ ...fixture.tasks[2], id: "t29", wait_reason: "no_roster_seat", wait_pool: "claude:opus:high", wait_holders: [] },
			{ ...fixture.tasks[2], id: "t30", wait_reason: "seat_cap", wait_holders: [] },
			{ ...fixture.tasks[0], id: "t31", block_reason: "verdict_failed" },
		],
	};
	const lines = renderScreen(doc, now);
	for (const want of [
		/^ {2}reviewer {2}claude sonnet medium {2}working on t28: Review the parser( {2}· .*)?$/,
		/^ {2}dev {2}codex gpt-5\.6-sol high {2}idle( {2}· .*)?$/,
		/^ {2}new {2}codex gpt-5\.6-sol high {2}starting( {2}· .*)?$/,
		/^ {2}old {2}codex gpt-5\.6-sol high {2}stopped( {2}· .*)?$/,
		/^ {2}t28 in progress on reviewer: Review the parser$/,
		/^ {2}t29 waiting: no seat serves claude:opus:high$/,
		/^ {2}t30 waiting: every seat is busy and the cap of 3 seats is reached$/,
		/^ {2}t31 blocked: its verifier failed; send it back with \/reset to try again$/,
	]) {
		assert.ok(lines.some((l) => want.test(l)), `no line ${want} in\n${lines.join("\n")}`);
	}
});

// Row 7: a free-text title (the model's) passes the redaction and the
// control-character strip before the cut, on the seat line and the task line.
test("row 7: a title with a canary and a control character shows redacted and stripped", () => {
	const canary = "Ab9" + "xY7q1";
	const title = `Fix\u0007 the parser token=${canary}\u001b[2J and then a long tail that runs past the cut`;
	const doc: ScreenStatus = {
		...fixture,
		seats: [{ ...fixture.seats[1], task_id: "t2", warnings: [] }],
		tasks: [{ ...fixture.tasks[1], title }],
	};
	const lines = renderScreen(doc, now).filter((l) => l.includes("Fix"));
	assert.equal(lines.length, 2, lines.join("\n"));
	for (const l of lines) {
		assert.ok(!l.includes(canary), l);
		assert.ok(!/[\u0000-\u001f\u007f]/.test(l), JSON.stringify(l));
		assert.match(l, /Fix the parser token=<redacted> and then a long tail/);
	}
	assert.equal(clean(title).length, TITLE_MAX);
	assert.ok(clean(title).endsWith("..."));
});

// Row 7: each empty block shows its one plain line, never a blank block;
// solo with no plan says what to do.
test("row 7: each empty block says so in one plain line", () => {
	const empty: ScreenStatus = { ...fixture, seats: [], tasks: [], gates: [], attention: [], decisions: [], train: { runs: [], alerts: [] }, requests: [] };
	const lines = renderScreen(empty, now);
	for (const [title, line] of [
		["seats (0)", "  No seat is running yet."],
		["tasks (0)", "  No tasks yet; add one with /add."],
		["train", "  No merge train has run yet."],
		["waiting on you (0)", "  Nothing is waiting on you."],
	]) {
		const i = lines.indexOf(title);
		assert.ok(i >= 0, `${title} in\n${lines.join("\n")}`);
		assert.equal(lines[i + 1], line);
	}
	const solo = new SoloTeam({ bin: "/b", amq: "", me: "lead", root: tmpdir(), session: "", hookTimeoutMs: 1, holdTimeoutMs: 1, models: [], lead: false, solo: true });
	assert.deepEqual(solo.planLines(), ["amq-squad: solo lead", "  No team yet. Tell me what you want built and I will propose a plan.", "/plan  /confirm  /team hide"]);
});

// plainAbove is a screen's lines above its rule that are refusal, outcome,
// doing and waiting lines: the header (the lead's root path), a seat's
// warning lines and the phone request rows are Go's text and ids, quoted.
function plainAbove(lines: string[]): string[] {
	const rule = lines.indexOf(RULE);
	const above = (rule < 0 ? lines.slice(0, -1) : lines.slice(0, rule)).slice(1);
	const reqAt = above.findIndex((l) => l.startsWith("requests ("));
	return above.filter((l, i) => !l.startsWith("    ! ") && !(reqAt >= 0 && i > reqAt && /^ {2}q-/.test(l)));
}

const res = (code: number, stdout: string, stderr = ""): RunResult => ({ code, stdout, stderr, timedOut: false, spawnError: "", ms: 1 });

// Row 4: no refusal or outcome line above its rule carries a snake_case code
// or a hex run; the code is below the rule. The sweep renders every block
// reason, every wait reason, every seat code, an open gate with its digest,
// and every key's refusals (each verb refusing with a coded line), and the
// solo plan block with its digest and a refused confirm. Reverted (a code
// back above the rule), the line is named.
test("row 4: no line above the rule carries a snake_case code or a hex run; the code is below it", async () => {
	const tasks = [
		...Object.keys(BLOCK_WORDS).map((r, i) => ({ ...fixture.tasks[0], id: `b${i}`, block_reason: r })),
		{ ...fixture.tasks[0], id: "bx", block_reason: "needs_decision" },
		...Object.keys(WAIT_WORDS).map((r, i) => ({ ...fixture.tasks[2], id: `w${i}`, wait_reason: r })),
		{ ...fixture.tasks[2], id: "wx", wait_reason: "made_up_wait" },
	];
	const seats = [...Object.keys(SEAT_DOING), "made_up_code"].map((c, i) => ({ ...fixture.seats[1], handle: `s${i}`, reason_code: c, task_id: "b0", warnings: [] }));
	const doc: ScreenStatus = { ...fixture, tasks, seats, gates: [{ id: "g1", action_class: "github_release", target_digest: "sha256:8502aa0011223344", state: "open" }], warnings: ["gate store: /r/gates/abcdef0123456789.json: unreadable"] };
	const sweep = (lines: string[], what: string) => {
		for (const l of plainAbove(lines)) assert.equal(codeIn(l), "", `${what}: a code above the rule: ${JSON.stringify(l)}\n${lines.join("\n")}`);
	};
	const screen = renderScreen(doc, now);
	sweep(screen, "the screen");
	const rec = screen.slice(screen.indexOf(RULE));
	for (const code of ["needs_decision", "made_up_wait", "made_up_code", "8502aa0011223344", "exit_refused", "ungated_tag"]) assert.ok(rec.some((l) => l.includes(code)), `${code} is not below the rule`);
	// every key's refusals and outcomes, from verbs that refuse with a code
	const coded = res(15, "", "error: task widen t1 refused (widen_not_requested): widen refused (widen_not_requested): task t1 is not blocked (state in_progress, reason needs_decision)\n");
	const keys = new Keys({ bin: "/b", amq: "", me: "cto", root: "/r", session: "s", hookTimeoutMs: 1, holdTimeoutMs: 1, models: [], lead: true }, async (_b, args) => (args[0] === "gate" && args[1] === "status" ? res(0, JSON.stringify({ data: [{ gate_id: "g1", class: "github_release", typed_class: "release", target: "gh release", digest: "sha256:8502aa0011223344aabbccdd", state: "open", question_sent: false }] })) : args[1] === "show" ? res(0, JSON.stringify({ data: { task: { id: "t1", state: "blocked", block_reason: "widen_requested", block_detail: "needed", refused_paths: ["a.go"] } } })) : args[1] === "propose" ? res(0, JSON.stringify({ data: { digest: "sha256:c0ffee@d15c" } })) : coded));
	const ctx = fakeCtx({ hasUI: true });
	const answers: Record<string, string[]> = {};
	ctx.ui.select = async (_t: string, o: string[]) => o[0];
	ctx.ui.input = async (title: string) => (answers[title]?.shift() ?? "x");
	ctx.ui.confirm = async () => true;
	const outcomes: KeyOutcome[] = [];
	const st: ScreenStatus = { ...fixture, tasks: [{ ...fixture.tasks[0], block_reason: "widen_requested" }], gates: doc.gates };
	for (const k of ["add", "reset", "dismiss", "answer", "widen", "foo"]) outcomes.push(await keys.press(k, ctx, st));
	outcomes.push(await keys.press("add", ctx, st, "seat"));
	outcomes.push(await keys.press("add", ctx, { ...st, project_path: undefined }, "seat"));
	outcomes.push(await keys.press("add", ctx, st, "bogus"));
	outcomes.push(await keys.press("reset", ctx, { ...st, tasks: [] }));
	outcomes.push(await keys.press("dismiss", ctx, { ...st, seats: [] }));
	outcomes.push(await keys.press("answer", ctx, { ...st, gates: [] }));
	outcomes.push(await keys.press("widen", ctx, { ...st, tasks: [] }));
	outcomes.push(await keys.press("add", fakeCtx({ hasUI: false }), st));
	outcomes.push(await keys.press("add", ctx, undefined));
	for (const o of outcomes) {
		assert.ok(o.line, JSON.stringify(o));
		sweep(renderScreen({ ...fixture, decisions: [] }, now, o), `/${o.key}`);
	}
	assert.ok(outcomes.filter((o) => o.refused).length >= 10, "the sweep saw too few refusals");
	// the solo plan block: the digest and a refused confirm's code below the rule
	const team = new SoloTeam({ bin: "/b", amq: "", me: "lead", root: tmpdir(), session: "", hookTimeoutMs: 1, holdTimeoutMs: 1, models: [], lead: false, solo: true });
	team.proposal = { digest: "sha256:" + "0123456789abcdef".repeat(4) + "@absent", prefix: "0123456789abcdef", plan: ["goal: x", "seat a (b) pi"], project_dir: "/p" };
	team.show({ key: "confirm", refused: true, line: "The lead did not take the confirmation, so nothing was confirmed.", record: "peer_not_lead_pi: pid 9" }, fakeCtx());
	const plan = team.planLines();
	sweep(plan, "the plan block");
	assert.ok(plan.slice(plan.indexOf(RULE)).some((l) => l.includes("0123456789abcdef")), "the digest is not below the rule");
	assert.ok(plan.slice(plan.indexOf(RULE)).some((l) => l.includes("peer_not_lead_pi")), "the code is not below the rule");
});

// Row 4, the pi fixture: the widen refusal at tools.ts (refusedPaths) says
// what happened and what to do above its rule, the state and the reason
// code below it.
test("row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule", () => {
	for (const t of [
		{ id: "t1", state: "in_progress", block_reason: "" },
		{ id: "t1", state: "blocked", block_reason: "needs_decision" },
		{ id: "t1", state: "blocked", block_reason: "outside_declared_files", refused_paths: [] },
	]) {
		let msg = "";
		try {
			refusedPaths(t);
		} catch (err) {
			msg = (err as Error).message;
		}
		const at = msg.indexOf(`\n\n${RULE}\n`);
		assert.ok(at > 0, `no rule in ${JSON.stringify(msg)}`);
		assert.equal(codeIn(msg.slice(0, at)), "", `a code above the rule: ${msg}`);
		assert.notEqual(codeIn(msg.slice(at)), "", `no code below the rule: ${msg}`);
	}
});
