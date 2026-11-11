// Brief 31-1's second PR: the read-only screen in the lead's pi, rendered
// from a status fixture against the stand-in pi.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { countsLine, installScreen, pollPaths, renderScreen, Screen, SCREEN_KEY, type ScreenStatus } from "../extensions/lib/screen.ts";
import { truncateToWidth, visibleWidth } from "../extensions/lib/width.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import { installCommands } from "../extensions/lib/commands.ts";
import type { RunResult } from "../extensions/lib/verbs.ts";
import { FakePi, fakeCtx } from "./fakepi.ts";
import { visibleWidth as piVisibleWidth } from "@earendil-works/pi-tui";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtureText = fs.readFileSync(path.join(here, "status-fixture.json"), "utf8");
const fixture = JSON.parse(fixtureText) as ScreenStatus;
const now = new Date("2026-11-01T09:05:00Z");

function cfg(): Config {
	return { bin: "/opt/amq-squad", amq: "", me: "cto", root: "/Users/owner/code/app/.agent-mail/v31", session: "v31", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: true };
}

function ok(stdout: string): RunResult {
	return { code: 0, stdout, stderr: "", timedOut: false, spawnError: "", ms: 1 };
}

function drawScreen(status: ScreenStatus, rows: number, width = 160, error = ""): string[] {
	const ctx = fakeCtx({ hasUI: true });
	ctx.ui.setWidget = (key: string, content: any, opts?: { placement?: string }) => {
		const lines = typeof content === "function" ? content({ terminal: { rows } }, {}).render(width) : content;
		ctx.widgets[key] = { lines, placement: opts?.placement };
	};
	const screen = new Screen(cfg());
	screen.last = status;
	screen.error = error;
	screen.ctx = ctx;
	screen.render();
	return ctx.widgets[SCREEN_KEY].lines ?? [];
}

// pi-tui's differential renderer stops and throws on this condition
// (tui-main-screen.js:469-493). Keep it here so the width row proves the
// crash is gone, rather than proving only that a line count stayed small.
function acceptLikePiTui(lines: string[], width: number): void {
	for (const [i, line] of lines.entries()) {
		const actual = piVisibleWidth(line);
		if (actual > width) throw new Error(`Rendered line ${i} exceeds terminal width (${actual} > ${width}).`);
	}
}

function generatedWidthStrings(): string[] {
	const corpus = [
		"plain ASCII", "\t", "Àéÿ × ÷", "שלום", "שָׁלוֹם",
		"汉字かなカナ", "⭐⭕", "𖿠𛀀", "🙂 👩‍💻 🏳️‍🌈 👨‍👩‍👧‍👦",
		"e\u0301 का क्ष \u093e", "\ud83d", "\ude42", "\ud83d\ude42",
		"\x1b[31mred 红字\x1b[0m tail",
		"\x1b]8;;https://example.test\x1b\\linked 界\x1b]8;;\x1b\\ tail",
		"\x1b]title\x07tail", "\x1b_payload\x1b\\tail",
		"\x1bPq#0\x1b\\tail", "\x1bXsos\x07tail", "\x1b^pm\x1b\\tail", "\x1b(Btail",
		"\x1b[?25lprivate", "\x1b[5 qcursor", "\x1b[", "\x1b[31", "\x1b]8;;open",
		// pi-tui strips escapes before segmenting. These two cases prove that
		// our per-fragment, per-code-point measure remains above the joined
		// keycap cluster and surrogate-pair emoji.
		"1\x1b[31m\ufe0f\u20e3", "\ud83d\x1b[31m\ude42",
	];
	for (let cp = 0x00c0; cp <= 0x00ff; cp++) corpus.push(String.fromCodePoint(cp));
	for (let cp = 0x05d0; cp <= 0x05ea; cp++) corpus.push(String.fromCodePoint(cp));
	for (let final = 0x40; final <= 0x7e; final++) corpus.push(`A\x1b[12;3${String.fromCharCode(final)}界`);

	const atoms = ["A", "é", "א", "\u05b0", "界", "⭐", "🙂", "\ud83d", "\ude42", "1\ufe0f\u20e3", "का", "👨‍👩‍👧‍👦"];
	const escapes = [
		...Array.from({ length: 0x7f - 0x40 }, (_, i) => `\x1b[12;3${String.fromCharCode(0x40 + i)}`),
		"\x1b[?25l", "\x1b[5 q", "\x1b[", "\x1b[31",
		"\x1b]8;;https://example.test\x07", "\x1b]8;;https://example.test\x1b\\",
		"\x1b]title\x07", "\x1b_payload\x1b\\",
	];
	for (const left of atoms) for (const escape of escapes) for (const right of atoms) corpus.push(left + escape + right);
	return corpus;
}

test("the local measure is an upper bound for pi-tui and truncation is safe over the corpus and generated strings", () => {
	const cases = generatedWidthStrings();
	assert.equal(cases.length, 10_406, "the generated matrix lost a class");
	for (const text of cases) {
		const ours = visibleWidth(text);
		assert.ok(ours >= piVisibleWidth(text), `${JSON.stringify(text)}: ours ${ours}, pi-tui ${piVisibleWidth(text)}`);
		for (let width = 0; width <= ours + 1; width++) {
			const actual = truncateToWidth(text, width);
			assert.ok(visibleWidth(actual) <= width, `${JSON.stringify(actual)}: ours exceeds ${width}`);
			assert.ok(piVisibleWidth(actual) <= width, `${JSON.stringify(actual)}: pi-tui exceeds ${width}`);
		}
	}
});

test("truncation never cuts inside an ESC-led CSI run that pi-tui treats as text", () => {
	for (const run of ["\x1b[5 q", "\x1b[?25l"]) {
		const text = `A${run}Z`;
		for (let width = 0; width <= visibleWidth(text) + 1; width++) {
			const actual = truncateToWidth(text, width);
			const material = actual.replaceAll("\x1b[0m", "");
			const escape = material.indexOf("\x1b");
			assert.ok(escape < 0 || material.slice(escape).startsWith(run), `${JSON.stringify(actual)} cut ${JSON.stringify(run)} at ${width}`);
			assert.ok(piVisibleWidth(actual) <= width, `${JSON.stringify(actual)} is wider than ${width}`);
		}
	}
});

test("truncation keeps every ESC-led run whole or moves the cut before it", () => {
	const cases = [
		{ text: "\x1b(Babcdefgh", run: "\x1b(B" },
		{ text: "\x1b7abc", run: "\x1b7" },
		{ text: "\x1bPq#0\x1b\\abc", run: "\x1bPq#0\x1b\\" },
		{ text: "\x1bXsos\x07abc", run: "\x1bXsos\x07" },
		{ text: "\x1b^pm\x1b\\abc", run: "\x1b^pm\x1b\\" },
		{ text: "abc\x1b", run: "\x1b" },
		{ text: "abc\x1b(", run: "\x1b(" },
	];
	for (const { text, run } of cases) {
		for (let width = 0; width <= visibleWidth(text) + 1; width++) {
			const actual = truncateToWidth(text, width);
			const material = actual.replaceAll("\x1b[0m", "");
			const withoutWholeRun = material.includes(run) ? material.replace(run, "") : material;
			assert.ok(!withoutWholeRun.includes("\x1b"), `${JSON.stringify(actual)} cut ${JSON.stringify(run)} at ${width}`);
			assert.ok(piVisibleWidth(actual) <= width, `${JSON.stringify(actual)} is wider than ${width}`);
		}
	}
});

// block returns the lines of one block: from its title to the next block's
// title (a line that starts at column 0).
function block(lines: string[], title: string): string[] {
	const i = lines.findIndex((l) => l.startsWith(title));
	assert.ok(i >= 0, `no ${title} block in\n${lines.join("\n")}`);
	const j = lines.findIndex((l, k) => k > i && !l.startsWith(" "));
	return lines.slice(i, j < 0 ? undefined : j);
}

// below is the record under the screen's rule.
function below(lines: string[]): string[] {
	const i = lines.indexOf("----------");
	return i < 0 ? [] : lines.slice(i + 1, -1);
}

// Row 16, in 3.1.1-3's words: the first line carries the lead handle and
// the root; each seat line says what the seat is doing, each task line why
// it waits or what stopped it and what to do; a block's last stop is a
// plain line and its code is below the rule.
test("the screen renders from a status fixture: the lead and the root, each block in plain words, the codes below the rule", () => {
	const lines = renderScreen(fixture, now);
	assert.match(lines[0], /lead cto/);
	assert.match(lines[0], /root \/Users\/owner\/code\/app\/\.agent-mail\/v31/);
	const seats = block(lines, "seats");
	assert.ok(seats.includes("  qa  codex gpt-5.6-sol high  idle  · up in 12.4s (over the 10s ceiling), first message 57.4s"), seats.join("\n"));
	assert.ok(seats.includes("  scribe  claude sonnet medium  working on t2: Write the notes  · up in 4.7s"), seats.join("\n"));
	assert.equal(seats.at(-1), "  seat old-dev did not stop cleanly after t0: the seat's commit record failed its check; the lead investigates before the seat works again");
	assert.ok(seats.includes("    ! it came up in 12.4s, over the 10s ceiling (slowest step: the seat's own start); nothing to do unless it repeats, and /dismiss hides it once you know"), seats.join("\n"));
	const tasks = block(lines, "tasks");
	assert.ok(tasks.includes("  t1 blocked: the commit step refused it, it changed files outside its task; accept them with /widen or send it back with /reset"), tasks.join("\n"));
	assert.ok(tasks.includes("  t2 in progress on scribe: Write the notes  PR #812"), tasks.join("\n"));
	const train = block(lines, "train");
	assert.ok(train.includes("  running: refused a PR, 13m ago; PRs #811, #812; 1 merged"), train.join("\n"));
	assert.ok(train.includes("  last run: merged a PR, 62m ago; PRs #810; 1 merged"), train.join("\n"));
	assert.ok(train.includes("    ! tag v1.2.0 has no accepted gate"), train.join("\n"));
	assert.equal(train.at(-1), "  the merge train stopped at PR #812: its CI is not green; fix the failing check and the train tries again");
	const waiting = block(lines, "waiting on you (1)");
	assert.deepEqual(waiting.slice(1), ["  Create a tag named v1?  (answer on the phone; /answer shows it)"]);
	// below the rule: the codes and ids for the record
	const rec = below(lines);
	assert.ok(rec.includes("  exit_refused: old-dev ledger_tampered at gone"), rec.join("\n"));
	assert.ok(rec.includes("  ungated_tag"), rec.join("\n"));
	assert.ok(rec.includes("  train r2: refusal PR #812 ci_green: a required check failed"), rec.join("\n"));
	assert.ok(rec.includes("  train running: r2 refusal") && rec.includes("  train last run: r1 merge"), rec.join("\n"));
	assert.ok(rec.some((l) => l.startsWith("  qa: launch_budget_exceeded: 12.400 s from the start to seat live")), rec.join("\n"));
	assert.ok(rec.includes("  g1  tag  8502aa0011223344  age 6m  waiting on Buzz"), rec.join("\n"));
	assert.ok(!lines.some((l) => l.includes("g0")), "an accepted gate is not shown");
	// A document with nothing that stopped an item shows no last-stop line.
	const quiet = renderScreen({ ...fixture, decisions: [], tasks: [], train: { runs: [], alerts: [] } }, now);
	assert.deepEqual(quiet.filter((l) => l.includes("did not stop") || l.includes("train stopped")), []);
});

// Brief 31-4 (the lead side): per phone request, whether the lead's answer
// was relayed to the owner's DM or refused by the relay; the refusal's name
// is below the rule.
test("the requests block marks each phone request relayed or refused, the name below the rule", () => {
	const lines = renderScreen(fixture, now);
	const i = lines.indexOf("phone requests (2)");
	assert.ok(i > 0, lines.join("\n"));
	assert.equal(lines[i + 1], "  a phone request to cto, not answered yet: the relay held back a reply shaped like a gate answer; the lead answers again in words");
	assert.equal(lines[i + 2], "  a phone request to cto, answered 2m ago");
	// the 3.1.0 rows, ids and refusal names, are below the rule
	assert.ok(below(lines).includes("  q-7c41d09e2a55  cto  not relayed  refused: relay_refused_gate_shape"), below(lines).join("\n"));
	assert.ok(below(lines).includes("  q-3f2a9c1e7b04  cto  relayed 2m ago"));
	assert.ok(!lines.slice(0, lines.indexOf("----------")).some((l) => l.includes("q-7c41d09e2a55")));
	assert.ok(!renderScreen({ ...fixture, requests: [] }, now).some((l) => l.startsWith("phone requests")));
});

test("an active seat and task show the lease and budget clocks", () => {
	const clocks = {
		...fixture,
		decisions: [],
		tasks: fixture.tasks.map((t) => t.id === "t2" ? { ...t, lease_expires_at: "2026-11-01T09:07:00Z", budget_ends_at: "2026-11-01T09:35:00Z" } : t),
	};
	const lines = renderScreen(clocks, now);
	assert.ok(lines.some((l) => l.includes("scribe") && l.includes("its lease has 2m left; its budget has 30m left")), lines.join("\n"));
	assert.ok(lines.some((l) => l.includes("t2 in progress") && l.includes("its lease has 2m left; its budget has 30m left")), lines.join("\n"));
});

// Row 19 (the screen's half): a Claude seat stalled at a dialog and a pi
// seat with no key for its provider each show as a line with what the seat
// is doing and its warning; neither is a last stop.
test("a seat stalled at a dialog and a keyless pi seat are screen lines with what the seat is doing, not refusals", () => {
	const lines = renderScreen({ ...fixture, decisions: [] }, now);
	const seats = block(lines, "seats");
	const scribe = seats.findIndex((l) => l.startsWith("  scribe  claude sonnet medium  working on t2"));
	assert.ok(scribe >= 0, seats.join("\n"));
	// the plan reviewer (3.1.1): the field records a permission request made
	// this turn, not a stop; the words say only that
	assert.equal(seats[scribe + 1], "    ! seat scribe asked for a permission 3m0s ago this turn; if it is not moving, check its pane");
	assert.ok(!seats.some((l) => /stalled|stopped at/.test(l)), seats.join("\n"));
	const pi = seats.findIndex((l) => l.startsWith("  pi-dev  pi anthropic/claude-sonnet-5 medium  idle"));
	assert.ok(pi >= 0, seats.join("\n"));
	assert.equal(seats[pi + 1], "    ! its model anthropic/claude-sonnet-5 has no provider key, so it stalls at its first turn; add the key to its env needs or its pi login, then relaunch");
	assert.ok(!seats.some((l) => l.includes("did not stop")), seats.join("\n"));
});

// Row 15 (the screen's half): the poll stats .generation and scheduler.json
// on its timer and runs status --json only when either changed.
test("the poll runs status --json only after .generation or scheduler.json changed", async () => {
	const stats: Record<string, { mtimeMs: number; size: number } | undefined> = {};
	const [gen, sched] = pollPaths(cfg().root);
	assert.equal(gen, "/Users/owner/code/app/.agent-mail/v31/extensions/amq-squad/tasks/.generation");
	assert.equal(sched, "/Users/owner/code/app/.agent-mail/v31/extensions/amq-squad/scheduler.json");
	stats[gen] = { mtimeMs: 1, size: 20 };
	stats[sched] = { mtimeMs: 1, size: 300 };
	const argvs: string[][] = [];
	const screen = new Screen(cfg(), async (_bin, args) => {
		argvs.push(args);
		return ok(fixtureText);
	}, (p) => stats[p]);
	await screen.poll();
	await screen.poll();
	await screen.poll();
	assert.equal(screen.statusRuns, 1, "an unchanged key ran status again");
	assert.deepEqual(argvs[0], ["status", "--root", cfg().root, "--json"]);
	stats[gen] = { mtimeMs: 2, size: 20 };
	await screen.poll();
	await screen.poll();
	assert.equal(screen.statusRuns, 2, "a new .generation did not run status once");
	stats[sched] = { mtimeMs: 2, size: 301 };
	await screen.poll();
	assert.equal(screen.statusRuns, 3, "a rewritten scheduler.json did not run status");
	await screen.poll();
	assert.equal(screen.statusRuns, 3);
	assert.equal(screen.last?.session, "v31");
});

// Row 16 (3.1.1-3): the screen is a widget below the editor of the team
// lead's pi, on by default; /team hide collapses it to one footer line with
// the counts and /team shows it again.
test("/team hide collapses the widget to the counts line; /team shows it", async () => {
	const pi = new FakePi();
	const screen = installScreen(pi as any, cfg(), async () => ok(fixtureText), () => ({ mtimeMs: 1, size: 1 }));
	installCommands(pi as any, { phases: ["team"], phase: () => "team", screen });
	const ctx = fakeCtx({ hasUI: true });
	await pi.emit("session_start", { type: "session_start", reason: "startup" }, ctx);
	for (let i = 0; i < 100 && !screen.last; i++) await new Promise((r) => setTimeout(r, 10));
	screen.stop();
	const shown = ctx.widgets[SCREEN_KEY];
	assert.ok(shown?.lines && shown.lines[0].includes("lead cto"), JSON.stringify(shown));
	assert.equal(shown.placement, "belowEditor");
	assert.equal(ctx.statuses[SCREEN_KEY], undefined);
	const cmd = pi.commands.get("team");
	assert.ok(cmd, "no /team command");
	await cmd.handler("hide", ctx);
	assert.equal(ctx.widgets[SCREEN_KEY].lines, undefined, "collapsed, the widget stays");
	assert.equal(ctx.statuses[SCREEN_KEY], countsLine(fixture));
	assert.match(ctx.statuses[SCREEN_KEY], /^squad v31: 3 seats, 3 tasks; 1 needs you, 1 working, 2 ready, 1 inactive; 1 gates open \(\/team shows the screen\)$/);
	await cmd.handler("", ctx);
	assert.ok(ctx.widgets[SCREEN_KEY].lines?.[0].includes("lead cto"));
	assert.equal(ctx.statuses[SCREEN_KEY], undefined);
});

// Only the team lead gets the screen; a seat's package registers no
// command.
test("a seat that is not the team lead gets no screen", async () => {
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	Object.assign(process.env, { AM_ME: "pi-1", AM_ROOT: "/tmp/amq-squad-screen-root", AM_SESSION: "s", AMQ_SQUAD_BIN: "/bin/false" });
	const seat = new FakePi();
	amqSquad(seat as any);
	assert.equal(seat.commands.size, 0);
	process.env.AMQ_SQUAD_LEAD = "1";
	const lead = new FakePi();
	amqSquad(lead as any);
	assert.equal(lead.commands.has("team"), true);
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
});

// Day 3 (t26 sat four hours on a file lock nobody saw): a pending task
// waiting on a file lock names the tasks holding it, and a file lock no task
// holds any more shows as stale.
test("a pending task waiting on a file lock names its holder and shared declaration", () => {
	const tasks = block(renderScreen(fixture, now), "tasks");
	assert.ok(tasks.includes("  t26 waits for t71: both declare docs/v3-spike/320-evidence/"), tasks.join("\n"));
	const stale = { ...fixture, tasks: fixture.tasks.map((t) => (t.id === "t26" ? { ...t, wait_holders: [], waits_for: [] } : t)) };
	assert.ok(block(renderScreen(stale, now), "tasks").includes("  t26 waiting: for a file lock no task holds any more (a stale wait)"));
});

test("a pin outside its class chain names both chains on the screen", () => {
	const detail = "the pin codex:gpt-5.6-terra:medium is on the task tier (class implement); class plan runs on claude:opus:high, codex:gpt-5.6-sol:high";
	const pinned = { ...fixture.tasks[0], id: "t75", block_reason: "pin_unresolved" };
	const lines = renderScreen({ ...fixture, tasks: [pinned] }, now, undefined, { t75: { block_detail: detail } });
	assert.ok(lines.includes(`  t75 blocked: ${detail}; /reset it onto another pool`), lines.join("\n"));
});

test("task rows put actionable work first and fold closed work into the available height", () => {
	const closed = Array.from({ length: 73 }, (_, i) => ({ ...fixture.tasks[0], id: `c${i + 1}`, task_state: "completed", block_reason: "" }));
	const open = [
		{ ...fixture.tasks[2], id: "t74", task_state: "pending", wait_reason: "seat_cap", wait_holders: [] },
		{ ...fixture.tasks[1], id: "t73", task_state: "in_progress", lease_holder: "reviewer" },
		{ ...fixture.tasks[2], id: "t72", task_state: "blocked", block_reason: "verdict_failed", wait_reason: "" },
	];
	const tasks = block(renderScreen({ ...fixture, tasks: [...closed, ...open] }, now, undefined, {}, 3), "tasks");
	assert.deepEqual(tasks.slice(1, 4).map((line) => line.trim().split(" ")[0]), ["t72", "t73", "t74"]);
	assert.equal(tasks.at(-1), "  73 closed");
	assert.ok(!tasks.some((line) => line.startsWith("  c")), tasks.join("\n"));

	const withRoom = block(renderScreen({ ...fixture, tasks: [...closed.slice(0, 4), ...open.slice(0, 2)] }, now, undefined, {}, 3), "tasks");
	assert.equal(withRoom.at(-1), "  4 closed");

	const exactOpenFit = block(renderScreen({ ...fixture, tasks: [...closed.slice(0, 4), ...open.slice(0, 2)] }, now, undefined, {}, 2), "tasks");
	assert.deepEqual(exactOpenFit.slice(1).map((line) => line.trim().split(" ")[0]), ["t73", "t74", "4"]);
	assert.equal(exactOpenFit.at(-1), "  4 closed");

	const oneOpenManyClosed = { ...fixture, tasks: [...closed, open[1]] };
	assert.deepEqual(
		renderScreen(oneOpenManyClosed, now, undefined, {}, 10),
		renderScreen(oneOpenManyClosed, now, undefined, {}, 73),
		"a cap above the pane can fit only by folding the same closed rows as the pane-sized cap",
	);
});

test("task overflow reserves its last row for the /team summary", () => {
	const pending = Array.from({ length: 5 }, (_, i) => ({ ...fixture.tasks[2], id: `t${80 + i}`, wait_reason: "seat_cap", wait_holders: [] }));
	const tasks = block(renderScreen({ ...fixture, tasks: pending }, now, undefined, {}, 3), "tasks");
	assert.equal(tasks.length, 4);
	assert.equal(tasks.at(-1), "  +3 more (/team for all)");
});

test("the widget bounds its task-cap renders by the smaller of task count and pane rows", () => {
	const closed = Array.from({ length: 200 }, (_, i) => ({ ...fixture.tasks[0], id: `c${i}`, task_state: "completed", block_reason: "" }));
	const open = Array.from({ length: 30 }, (_, i) => ({ ...fixture.tasks[2], id: `t${i}`, task_state: "pending", wait_reason: "seat_cap" }));
	const status = { ...fixture, tasks: [...closed, ...open] };
	const rows = renderScreen(status, now, undefined, {}, 0).length + 2;
	let renders = 0;
	const screen = new Screen(cfg(), async () => ok(fixtureText), () => ({ mtimeMs: 1, size: 1 }), (...args) => {
		renders++;
		return renderScreen(...args);
	});
	const ctx = fakeCtx({ hasUI: true });
	ctx.ui.setWidget = (_key: string, content: any) => {
		if (typeof content === "function") content({ terminal: { rows } }, {}).render(160);
	};
	screen.last = status;
	screen.ctx = ctx;
	screen.render();
	assert.ok(renders <= Math.min(status.tasks.length, rows) + 2, `${renders} renders for ${status.tasks.length} tasks in ${rows} rows`);
});

test("the widget measures the rendered screen with no fixed records, includes content at fixed plus three, and never exceeds the pane", () => {
	const blocked = { ...fixture.tasks[0], id: "t90", task_state: "blocked", block_reason: "custom_one" };
	const status: ScreenStatus = {
		...fixture,
		seats: [],
		tasks: [blocked],
		attention: [],
		gates: [],
		warnings: [],
		decisions: [],
		train: { runs: [], alerts: [] },
		requests: [],
	};
	const fixed = renderScreen(status, now, undefined, {}, 0).length;
	for (const rows of [fixed, fixed + 1, fixed + 2, fixed + 3]) {
		const lines = drawScreen(status, rows);
		assert.ok(lines.length <= rows, `${lines.length} lines in a ${rows}-row pane\n${lines.join("\n")}`);
		if (rows === fixed + 3) {
			assert.ok(lines.includes("  t90 blocked; its reason is below the line"), lines.join("\n"));
			assert.ok(lines.includes("----------"), lines.join("\n"));
			assert.ok(lines.includes("  t90 blocked: custom_one"), lines.join("\n"));
		}
	}
});

test("render(width) keeps long task, record, stale-error, and truncation lines within pi-tui's contract", () => {
	const width = 50;
	const untitled = { ...fixture.tasks[1], id: "t90", title: "", task_state: "in_progress", lease_holder: "reviewer", pr_number: 0, lease_expires_at: null, budget_ends_at: null };
	const baseStatus: ScreenStatus = { ...fixture, root: "/", lead: null, lead_handle: "c", seats: [], tasks: [untitled], attention: [], gates: [], warnings: [], decisions: [], train: { runs: [], alerts: [] }, requests: [] };
	const baseTaskLine = renderScreen(baseStatus, now, undefined, {}, 1).find((line) => line.startsWith("  t90 in progress"))!;
	const longTitle = { ...untitled, title: "x".repeat(width + 1 - visibleWidth(baseTaskLine) - 2) };
	const longRecord = { ...fixture.tasks[0], id: "t91", task_state: "blocked", block_reason: "custom_" + "record_".repeat(30) };
	const status: ScreenStatus = { ...baseStatus, tasks: [longTitle, longRecord] };
	const rawTaskLine = renderScreen(status, now, undefined, {}, 2).find((line) => line.startsWith("  t90 in progress"))!;
	assert.equal(visibleWidth(rawTaskLine), width + 1, "the reverted task line is exactly one column too wide");
	const lines = drawScreen(status, 40, width, "status --json failed: " + "detail ".repeat(20));
	assert.doesNotThrow(() => acceptLikePiTui(lines, width));
	assert.ok(lines.some((line) => line.startsWith("  t90 in progress on reviewer")), lines.join("\n"));
	assert.ok(lines.some((line) => line.startsWith("  t91 blocked: custom_")), lines.join("\n"));
	assert.ok(lines.some((line) => line.startsWith("  (the screen may be stale:")), lines.join("\n"));

	const clipped = drawScreen(status, 1, width, "status --json failed: " + "detail ".repeat(20));
	assert.equal(clipped.length, 1);
	assert.match(clipped[0], /^… \d+ more lines/);
	assert.doesNotThrow(() => acceptLikePiTui(clipped, width));
	assert.deepEqual(drawScreen(status, 0, width), []);
});

test("the widget measures the existing fixture at all three pane heights", () => {
	const blocked = [
		{ ...fixture.tasks[0], id: "t90", task_state: "blocked", block_reason: "custom_one" },
		{ ...fixture.tasks[0], id: "t91", task_state: "blocked", block_reason: "custom_two" },
	];
	const status = { ...fixture, tasks: blocked };
	const fixed = renderScreen(status, now, undefined, {}, 0).length;

	const full = drawScreen(status, fixed);
	assert.ok(full.length <= fixed, `${full.length} lines in a ${fixed}-row pane\n${full.join("\n")}`);
	assert.ok(!full.some((line) => line.includes("more (/team for all)")), full.join("\n"));
	const one = drawScreen(status, fixed + 1);
	assert.ok(one.length <= fixed + 1, `${one.length} lines in a ${fixed + 1}-row pane\n${one.join("\n")}`);
	assert.ok(one.includes("  +2 more (/team for all)"), one.join("\n"));
	const two = drawScreen(status, fixed + 2);
	assert.ok(two.length <= fixed + 2, `${two.length} lines in a ${fixed + 2}-row pane\n${two.join("\n")}`);
	assert.ok(two.includes("  +2 more (/team for all)"), two.join("\n"));
});

test("the widget replaces fixed-content overflow with a truncation line", () => {
	const fixed = renderScreen(fixture, now, undefined, {}, 0);
	const rows = 5;
	const lines = drawScreen(fixture, rows);
	assert.equal(lines.length, rows);
	assert.equal(lines.at(-1), `… ${fixed.length - rows + 1} more lines; enlarge the pane`);
});

test("fixed-content overflow at one row keeps the pane boundary", () => {
	const fixed = renderScreen(fixture, now, undefined, {}, 0);
	assert.deepEqual(drawScreen(fixture, 1), [`… ${fixed.length} more lines; enlarge the pane`]);
});

test("fixed-content overflow at zero rows keeps the pane boundary", () => {
	assert.deepEqual(drawScreen(fixture, 0), []);
});

test("the fixed-overflow message is width-fitted even when the pane is narrower than the message", () => {
	const fixed = renderScreen(fixture, now, undefined, {}, 0);
	const width = 12;
	const message = `… ${fixed.length} more lines; enlarge the pane`;
	const lines = drawScreen(fixture, 1, width);
	assert.deepEqual(lines, [truncateToWidth(message, width)]);
	assert.doesNotThrow(() => acceptLikePiTui(lines, width));
});

test("fixed-content overflow counts a stale-error line before truncating", () => {
	const fixed = renderScreen(fixture, now, undefined, {}, 0);
	const rows = 5;
	const lines = drawScreen(fixture, rows, 160, "status --json failed: exit 1");
	assert.equal(lines.length, rows);
	assert.equal(lines.at(-1), `… ${fixed.length - rows + 2} more lines; enlarge the pane`);
});

// Brief 3.1.1-2: t27 on 2026-11-01 routed to a pool no roster seat served,
// and the scheduler logged it every two seconds. The task line names the
// wait and the pool; the seats block is the running roster only, with no
// line for a stale record under the root.
test("a task waiting no_roster_seat names the pool, and a stale record is no seat line", () => {
	const t27 = { ...fixture.tasks[2], id: "t27", wait_reason: "no_roster_seat", wait_holders: [], wait_pool: "claude:opus:high" };
	const lines = renderScreen({ ...fixture, tasks: [...fixture.tasks, t27] }, now);
	const tasks = block(lines, "tasks");
	assert.ok(tasks.includes("  t27 waiting: no seat serves claude:opus:high"), tasks.join("\n"));
	assert.ok((fixture.stale_records ?? []).some((r) => r.handle === "senior-dev"), "the fixture carries a stale record");
	const seats = block(lines, "seats");
	assert.equal(seats[0], `seats (${fixture.seats.length})`);
	assert.ok(!seats.some((l) => l.includes("senior-dev")), seats.join("\n"));
});
