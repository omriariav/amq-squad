// Brief 31-1's second PR: the read-only screen in the lead's pi, rendered
// from a status fixture against the stand-in pi.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { countsLine, installScreen, pollPaths, renderScreen, Screen, SCREEN_KEY, type ScreenStatus } from "../extensions/lib/screen.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import { installCommands } from "../extensions/lib/commands.ts";
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
	assert.equal(seats.at(-1), "  seat old-dev did not stop cleanly after t0: the commits ledger does not verify; ask the owner to inspect the commits ledger before anything commits");
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

// Row 19 (the screen's half): a Claude seat stalled at a dialog and a pi
// seat with no key for its provider each show as a line with what the seat
// is doing and its warning; neither is a last stop.
test("a seat stalled at a dialog and a keyless pi seat are screen lines with what the seat is doing, not refusals", () => {
	const lines = renderScreen({ ...fixture, decisions: [] }, now);
	const seats = block(lines, "seats");
	const scribe = seats.findIndex((l) => l.startsWith("  scribe  claude sonnet medium  working on t2"));
	assert.ok(scribe >= 0, seats.join("\n"));
	assert.equal(seats[scribe + 1], "    ! it is stopped at a harness dialog open for 3m0s; answer the dialog in its pane");
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
test("a pending task waiting on a file lock names its holder tasks", () => {
	const tasks = block(renderScreen(fixture, now), "tasks");
	assert.ok(tasks.includes("  t26 waiting: t20, t23 hold its files"), tasks.join("\n"));
	const stale = { ...fixture, tasks: fixture.tasks.map((t) => (t.id === "t26" ? { ...t, wait_holders: [] } : t)) };
	assert.ok(block(renderScreen(stale, now), "tasks").includes("  t26 waiting: for a file lock no task holds any more (a stale wait)"));
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
