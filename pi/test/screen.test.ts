// Brief 31-1's second PR: the read-only screen in the lead's pi, rendered
// from a status fixture against the stand-in pi.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { countsLine, installScreen, pollPaths, renderScreen, Screen, SCREEN_KEY, type ScreenStatus } from "../extensions/lib/screen.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import type { RunResult } from "../extensions/lib/verbs.ts";
import { FakePi, fakeCtx } from "./fakepi.ts";

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

// block returns the lines of one block: from its title to its refusal line.
function block(lines: string[], title: string): string[] {
	const i = lines.findIndex((l) => l.startsWith(title));
	assert.ok(i >= 0, `no ${title} block in\n${lines.join("\n")}`);
	const j = lines.findIndex((l, k) => k > i && l.startsWith("  refusal: "));
	return lines.slice(i, j + 1);
}

// Row 16: the first line carries the lead handle and the root; each of the
// four blocks (seats, tasks, train, gates) ends with its refusal line, the
// newest refusal that stopped an item in that block, or none.
test("the screen renders from a status fixture: the lead and the root, four blocks, each with its refusal line", () => {
	const lines = renderScreen(fixture, now);
	assert.match(lines[0], /lead cto/);
	assert.match(lines[0], /root \/Users\/owner\/code\/app\/\.agent-mail\/v31/);
	const seats = block(lines, "seats");
	assert.ok(seats.some((l) => /qa {2}codex\/gpt-5\.6-sol {2}online\/idle {2}live {2}live 12\.4s OVER first-msg 57\.4s/.test(l)), seats.join("\n"));
	assert.equal(seats.at(-1), "  refusal: exit_refused: old-dev ledger_tampered at gone");
	const tasks = block(lines, "tasks");
	assert.ok(tasks.some((l) => l === "  t1  blocked  -  PR -  outside_declared_files"), tasks.join("\n"));
	assert.ok(tasks.some((l) => l === "  t2  in_progress  scribe  PR #812"), tasks.join("\n"));
	assert.equal(tasks.at(-1), "  refusal: commit_refused: t1 outside_declared_files: src/x.go");
	const train = block(lines, "train");
	assert.ok(train.some((l) => l.startsWith("  current: r2 refusal")), train.join("\n"));
	assert.ok(train.some((l) => l.startsWith("  last: r1 merge")), train.join("\n"));
	assert.ok(train.some((l) => l.includes("ungated_tag: tag v1.2.0 has no accepted gate")), train.join("\n"));
	assert.equal(train.at(-1), "  refusal: refusal PR #812 checks: a required check failed");
	const gates = block(lines, "gates (1 open)");
	assert.ok(gates.some((l) => l === "  g1  tag  8502aa001122  age 6m"), gates.join("\n"));
	assert.ok(!gates.some((l) => l.includes("g0")), "an accepted gate is not shown");
	assert.equal(gates.at(-1), "  refusal: none");
	// A document with nothing that stopped an item shows none in every block.
	const quiet = renderScreen({ ...fixture, decisions: [], tasks: [], train: { runs: [], alerts: [] } }, now);
	assert.deepEqual(quiet.filter((l) => l.startsWith("  refusal: ")), ["  refusal: none", "  refusal: none", "  refusal: none", "  refusal: none"]);
});

// Row 19 (the screen's half): a Claude seat stalled at a dialog and a pi
// seat with no key for its provider each show as a line with the seat's
// state and its warning; neither is the seats block's refusal.
test("a seat stalled at a dialog and a keyless pi seat are screen lines with the seat's state, not refusals", () => {
	const lines = renderScreen({ ...fixture, decisions: [] }, now);
	const seats = block(lines, "seats");
	const scribe = seats.findIndex((l) => l.startsWith("  scribe  claude/sonnet  online/lease_claimed  live"));
	assert.ok(scribe >= 0, seats.join("\n"));
	assert.match(seats[scribe + 1], /^ {4}! seat_stalled_at_dialog: /);
	const pi = seats.findIndex((l) => l.startsWith("  pi-dev  pi/anthropic/claude-sonnet-5  online/idle  live"));
	assert.ok(pi >= 0, seats.join("\n"));
	assert.match(seats[pi + 1], /^ {4}! pi_provider_key_missing: /);
	assert.equal(seats.at(-1), "  refusal: none");
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

// Row 16: the screen is a widget below the editor of the team lead's pi;
// /squad toggles it, and collapsed it is one footer line with the counts.
test("/squad toggles the widget; collapsed, one footer line with the counts", async () => {
	const pi = new FakePi();
	const screen = installScreen(pi as any, cfg(), async () => ok(fixtureText), () => ({ mtimeMs: 1, size: 1 }));
	const ctx = fakeCtx({ hasUI: true });
	await pi.emit("session_start", { type: "session_start", reason: "startup" }, ctx);
	for (let i = 0; i < 100 && !screen.last; i++) await new Promise((r) => setTimeout(r, 10));
	screen.stop();
	const shown = ctx.widgets[SCREEN_KEY];
	assert.ok(shown?.lines && shown.lines[0].includes("lead cto"), JSON.stringify(shown));
	assert.equal(shown.placement, "belowEditor");
	assert.equal(ctx.statuses[SCREEN_KEY], undefined);
	const cmd = pi.commands.get("squad");
	assert.ok(cmd, "no /squad command");
	await cmd.handler("", ctx);
	assert.equal(ctx.widgets[SCREEN_KEY].lines, undefined, "collapsed, the widget stays");
	assert.equal(ctx.statuses[SCREEN_KEY], countsLine(fixture));
	assert.match(ctx.statuses[SCREEN_KEY], /^squad v31: 3 seats, 3 tasks; 1 needs you, 1 working, 2 ready, 1 inactive; 1 gates open/);
	await cmd.handler("", ctx);
	assert.ok(ctx.widgets[SCREEN_KEY].lines?.[0].includes("lead cto"));
	assert.equal(ctx.statuses[SCREEN_KEY], undefined);
});

// Only the team lead gets the screen; a seat's package registers no /squad.
test("a seat that is not the team lead gets no screen", async () => {
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	Object.assign(process.env, { AM_ME: "pi-1", AM_ROOT: "/tmp/amq-squad-screen-root", AM_SESSION: "s", AMQ_SQUAD_BIN: "/bin/false" });
	const seat = new FakePi();
	amqSquad(seat as any);
	assert.equal(seat.commands.has("squad"), false);
	process.env.AMQ_SQUAD_LEAD = "1";
	const lead = new FakePi();
	amqSquad(lead as any);
	assert.equal(lead.commands.has("squad"), true);
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
});

// Day 3 (t26 sat four hours on a file lock nobody saw): a pending task
// waiting on a file lock shows the tasks holding it beside its state, and
// a file lock no task holds any more shows as stale.
test("a pending task waiting on a file lock names its holder tasks", () => {
	const tasks = block(renderScreen(fixture, now), "tasks");
	assert.ok(tasks.includes("  t26  pending  -  PR -  waiting on t20, t23"), tasks.join("\n"));
	const stale = { ...fixture, tasks: fixture.tasks.map((t) => (t.id === "t26" ? { ...t, wait_holders: [] } : t)) };
	assert.ok(block(renderScreen(stale, now), "tasks").includes("  t26  pending  -  PR -  waiting on a file lock no task holds (stale)"));
});
