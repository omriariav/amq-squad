// Brief 3.1.1-3, rows 1, 2 and 6: one pi command per action, each with a
// one-line plain description, none a pi built-in; the screen's last line
// lists exactly the commands of the current phase, in both phases; bare
// /squad toggles nothing and prints the phase's commands; the lead's team
// tools are Go's eleven and no command is a tool.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { COMMANDS, commandsLine, phaseCommands, type Phase } from "../extensions/lib/commands.ts";
import { PLAN_KEY } from "../extensions/lib/team.ts";
import { SCREEN_KEY, type ScreenStatus } from "../extensions/lib/screen.ts";
import { TEAM_LEAD_TOOLS } from "../extensions/lib/tools.ts";
import { codeIn } from "../extensions/lib/words.ts";
import { FakePi, fakeCtx, tmpdir } from "./fakepi.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtureText = fs.readFileSync(path.join(here, "status-fixture.json"), "utf8");

// pi's own built-in command names, read from the installed pi at test time
// (dist/core/slash-commands.js is not in the package's exports map, so it
// is imported by its file URL).
async function builtins(): Promise<string[]> {
	const url = pathToFileURL(path.join(here, "..", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "core", "slash-commands.js")).href;
	const mod = (await import(url)) as { BUILTIN_SLASH_COMMANDS: { name: string }[] };
	return mod.BUILTIN_SLASH_COMMANDS.map((c) => c.name);
}

// load runs the whole extension as a lead (lead --team) or the solo lead,
// with a fake amq-squad that answers status from the fixture.
async function load(kind: "lead" | "solo"): Promise<FakePi> {
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	const dir = tmpdir();
	const fixture = path.join(dir, "status.json");
	fs.writeFileSync(fixture, fixtureText);
	const bin = path.join(dir, "amq-squad");
	fs.writeFileSync(bin, `#!/bin/sh\nif [ "$1" = status ]; then cat '${fixture}'; exit 0; fi\nexit 1\n`, { mode: 0o755 });
	Object.assign(process.env, { AM_ME: "cto", AM_ROOT: path.join(dir, "root"), AM_SESSION: "s", AMQ_SQUAD_BIN: bin }, kind === "lead" ? { AMQ_SQUAD_LEAD: "1" } : { AMQ_SQUAD_SOLO_LEAD: "1", AMQ_SQUAD_PROJECT: dir });
	const pi = new FakePi(["read", "bash", "edit", "write"]);
	amqSquad(pi as any);
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	return pi;
}

// Row 1: every action has its own registered command with a one-line plain
// description; no name is a pi built-in; bare /squad toggles nothing and
// prints the phase's commands. Reverted (the 3.1.0 /squad with keys), the
// actions are not registered and /squad toggles the screen.
test("row 1: one command per action, a one-line plain description each, no pi built-in name", async () => {
	const names = await builtins();
	assert.ok(names.includes("settings") && names.includes("quit") && names.length >= 20, `pi's built-ins: ${names}`);
	const lead = await load("lead");
	const solo = await load("solo");
	assert.deepEqual([...lead.commands.keys()].sort(), ["add", "answer", "dismiss", "reset", "squad", "team", "widen"]);
	assert.deepEqual([...solo.commands.keys()].sort(), ["add", "answer", "confirm", "dismiss", "plan", "reset", "squad", "team", "widen"]);
	for (const pi of [lead, solo]) {
		for (const [name, def] of pi.commands) {
			assert.ok(!names.includes(name), `/${name} is a pi built-in`);
			assert.ok(!name.includes(" "), `/${name} is a subcommand`);
			const d = String(def.description ?? "");
			assert.ok(d && !d.includes("\n") && d.length <= 100, `/${name}'s description is not one line: ${JSON.stringify(d)}`);
			assert.equal(codeIn(d), "", `/${name}'s description carries a code: ${d}`);
			assert.doesNotMatch(d, /presses a key|\/squad (add|reset|clear|answer|confirm)/, `/${name}: ${d}`);
		}
	}
	// /add offers task and seat
	assert.deepEqual(lead.commands.get("add").getArgumentCompletions("").map((c: any) => c.value), ["task", "seat"]);
	assert.deepEqual(lead.commands.get("add").getArgumentCompletions("s").map((c: any) => c.value), ["seat"]);
	// bare /squad: the screen stays as it is, and the phase's commands print
	for (const [pi, phase, key] of [[lead, "team", SCREEN_KEY], [solo, "solo", PLAN_KEY]] as [FakePi, Phase, string][]) {
		const ctx = fakeCtx({ hasUI: true });
		const notes: string[] = [];
		ctx.ui.notify = (m: string) => notes.push(m);
		await pi.emit("session_start", { type: "session_start", reason: "startup" }, ctx);
		for (let i = 0; i < 100 && !(ctx.widgets[key]?.lines ?? []).some((l: string) => l.startsWith("amq-squad: lead") || l.startsWith("amq-squad: solo")); i++) await new Promise((r) => setTimeout(r, 10));
		const before = ctx.widgets[key]?.lines;
		assert.ok(before, `${phase}: no screen`);
		await pi.commands.get("squad").handler("", ctx);
		const after = ctx.widgets[key]?.lines ?? [];
		assert.ok(after.length > 0, `${phase}: /squad hid the screen`);
		assert.equal(ctx.statuses[key], undefined, `${phase}: /squad collapsed the screen`);
		const said = [...notes, ...after].join("\n");
		assert.ok(said.includes(`The commands you can use now: ${commandsLine(phase)}`.replace(/ {2}/g, " ")) || said.includes(`The commands you can use now: ${commandsLine(phase)}`), `${phase}: /squad did not print the commands:\n${said}`);
		assert.match(said, /goes in 3\.2/);
		await pi.emit("session_shutdown", {}, ctx);
	}
});

// Row 2: the screen's last line lists exactly the commands registered for
// the current phase, in both phases: the solo plan block and the team
// screen, and the solo lead's screen after its switch to the team phase.
test("row 2: the screen's last line lists exactly the current phase's commands", async () => {
	for (const phase of ["solo", "team"] as Phase[]) {
		const registered = Object.keys(COMMANDS).filter((n) => COMMANDS[n].phases.includes(phase) && COMMANDS[n].listed);
		assert.deepEqual(phaseCommands(phase), registered.map((n) => COMMANDS[n].listed));
	}
	assert.equal(commandsLine("solo"), "/plan  /confirm  /team hide");
	assert.equal(commandsLine("team"), "/add  /reset  /dismiss  /answer  /widen  /team hide");
	const lead = await load("lead");
	const ctx = fakeCtx({ hasUI: true });
	await lead.emit("session_start", { type: "session_start", reason: "startup" }, ctx);
	for (let i = 0; i < 100 && !(ctx.widgets[SCREEN_KEY]?.lines ?? [])[0]?.startsWith("amq-squad: lead"); i++) await new Promise((r) => setTimeout(r, 10));
	const team = ctx.widgets[SCREEN_KEY].lines as string[];
	assert.equal(team.at(-1), "/add  /reset  /dismiss  /answer  /widen  /team hide");
	for (const c of team.at(-1)!.split(/ {2}/)) assert.ok(lead.commands.has(c.slice(1).split(" ")[0]), `${c} is not registered`);
	await lead.emit("session_shutdown", {}, ctx);
	const solo = await load("solo");
	const sctx = fakeCtx({ hasUI: true });
	await solo.emit("session_start", { type: "session_start", reason: "startup" }, sctx);
	const plan = sctx.widgets[PLAN_KEY].lines as string[];
	assert.equal(plan.at(-1), "/plan  /confirm  /team hide");
	for (const c of plan.at(-1)!.split(/ {2}/)) assert.ok(solo.commands.has(c.slice(1).split(" ")[0]), `${c} is not registered`);
	// a team command in the solo phase refuses in words, naming the commands of now
	await solo.commands.get("add").handler("", sctx);
	assert.ok((sctx.widgets[PLAN_KEY].lines as string[]).includes("  /add: There is no team yet, so /add has nothing to act on. The commands you can use now: /plan  /confirm  /team hide"));
	// /team hide and /team on the plan block
	await solo.commands.get("team").handler("hide", sctx);
	assert.equal(sctx.widgets[PLAN_KEY].lines, undefined);
	assert.match(sctx.statuses[PLAN_KEY], /\/team shows it/);
	await solo.commands.get("team").handler("", sctx);
	assert.equal((sctx.widgets[PLAN_KEY].lines as string[]).at(-1), "/plan  /confirm  /team hide");
	await solo.emit("session_shutdown", {}, sctx);
});

// Row 2, the switch: the solo lead's screen after the owner's yes is the
// team screen with the team phase's commands; the plan block goes.
test("row 2: after the switch to the team phase the team screen shows the team's commands", async () => {
	const { SoloTeam } = await import("../extensions/lib/team.ts");
	const { installCommands } = await import("../extensions/lib/commands.ts");
	const { Screen } = await import("../extensions/lib/screen.ts");
	const cfg = { bin: "/b", amq: "", me: "lead", root: tmpdir(), session: "", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: false, solo: true };
	const doc = JSON.parse(fixtureText) as ScreenStatus;
	const runner = async (_b: string, args: string[]) => ({ code: args[0] === "status" ? 0 : 1, stdout: args[0] === "status" ? JSON.stringify(doc) : "", stderr: "", timedOut: false, spawnError: "", ms: 1 });
	const pi = new FakePi();
	const team = new SoloTeam(cfg, runner, () => new Date(), "", [], pi as any);
	const screen = new Screen(cfg, runner, () => ({ mtimeMs: 1, size: 1 }));
	const ctx = fakeCtx({ hasUI: true });
	team.ctx = ctx;
	team.onTeam = () => screen.start(ctx, 60000);
	installCommands(pi as any, { phases: ["solo", "team"], phase: () => team.phase, screen, solo: team });
	team.render();
	assert.equal((ctx.widgets[PLAN_KEY].lines as string[]).at(-1), "/plan  /confirm  /team hide");
	team.switchToTeam({ root: path.join(tmpdir(), "s1"), handle: "cto", session: "s1" });
	for (let i = 0; i < 100 && !screen.last; i++) await new Promise((r) => setTimeout(r, 10));
	screen.stop();
	assert.equal(ctx.widgets[PLAN_KEY].lines, undefined, "the plan block stayed");
	assert.equal((ctx.widgets[SCREEN_KEY].lines as string[]).at(-1), "/add  /reset  /dismiss  /answer  /widen  /team hide");
	// /confirm now refuses in words; /add reaches the team screen
	await pi.commands.get("confirm").handler("", ctx);
	assert.ok((ctx.widgets[SCREEN_KEY].lines as string[]).some((l) => l.startsWith("  /confirm: The team is already running")), (ctx.widgets[SCREEN_KEY].lines as string[]).join("\n"));
});

// Row 6: the lead's team tool list is unchanged (Go's eleven) and no
// command is a tool.
test("row 6: the team lead's tools are Go's eleven and no command is a tool", async () => {
	assert.equal(TEAM_LEAD_TOOLS.length, 11);
	for (const kind of ["lead", "solo"] as const) {
		const pi = await load(kind);
		const tools = [...pi.tools.keys()];
		for (const c of pi.commands.keys()) assert.ok(!tools.some((t) => t === c || t === `amq_${c}`), `/${c} is also a tool`);
		if (kind === "lead") assert.deepEqual(pi.getActiveTools().filter((t) => t.startsWith("amq_")).sort(), [...TEAM_LEAD_TOOLS].sort());
	}
});
