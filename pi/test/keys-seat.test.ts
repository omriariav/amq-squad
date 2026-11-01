// Brief 31-2, PR 2: the seat half of the add key, pressed as `/squad add`
// against the stand-in pi. Every verb goes through the injected runner;
// nothing reaches the real binary, AMQ, tmux or Buzz.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { installScreen, type ScreenStatus } from "../extensions/lib/screen.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import type { RunOptions, RunResult } from "../extensions/lib/verbs.ts";
import { FakePi, fakeCtx } from "./fakepi.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixture = JSON.parse(fs.readFileSync(path.join(here, "status-fixture.json"), "utf8")) as ScreenStatus;
const BIN = "/opt/amq-squad";
const PROJECT = "/Users/owner/code/app";
const SPEC = "rev-1:implementer:codex:gpt-5.4:high";

function cfg(): Config {
	return { bin: BIN, amq: "", me: "cto", root: "/Users/owner/code/app/.agent-mail/v31", session: "v31", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: true };
}

function res(code: number, stdout: string, stderr = ""): RunResult {
	return { code, stdout, stderr, timedOut: false, spawnError: "", ms: 1 };
}

function envelope(kind: string, data: unknown): string {
	return JSON.stringify({ schema_version: 1, kind, data });
}

// the three verbs of the seat half answered as the binary answers them
function verbs(over: Partial<Record<"propose" | "apply" | "relaunch", RunResult>> = {}) {
	return (args: string[]): RunResult => {
		if (args[0] === "team" && args[1] === "propose") return over.propose ?? res(0, envelope("team_propose", { digest: "sha256:c0ffee@d15c", seats: 6 }));
		if (args[0] === "team" && args[1] === "apply") return over.apply ?? res(0, envelope("team_apply", { digest: "sha256:c0ffee@d15c", seats: 6 }));
		if (args[0] === "scheduler" && args[1] === "relaunch") return over.relaunch ?? res(0, envelope("scheduler_relaunch", { ok: true, detail: "accepted" }));
		return res(1, "", "error: unexpected verb");
	};
}

function rig(reply: (args: string[]) => RunResult, answers: (string | undefined)[], confirm = true, status: ScreenStatus = fixture) {
	const calls: string[][] = [];
	const runner = async (bin: string, args: string[], _o: RunOptions): Promise<RunResult> => {
		if (args[0] === "status") return res(0, JSON.stringify(status));
		assert.equal(bin, BIN, "a verb ran with another binary than the lead's own");
		calls.push(args);
		return reply(args);
	};
	const pi = new FakePi();
	installScreen(pi as any, cfg(), runner, () => ({ mtimeMs: 1, size: 1 }));
	const queue = [...answers];
	const confirms: { title: string; message: string; at: number }[] = [];
	const ctx = fakeCtx({ hasUI: true });
	ctx.ui.input = async () => queue.shift();
	ctx.ui.select = async (_t: string, options: string[]) => {
		const a = queue.shift();
		if (a !== undefined && !options.includes(a)) throw new Error(`answer ${a} is not among ${options.join(", ")}`);
		return a;
	};
	ctx.ui.confirm = async (title: string, message: string) => {
		confirms.push({ title, message, at: Date.now() });
		return confirm;
	};
	const press = async () => {
		await pi.commands.get("squad")!.handler("add", ctx);
		const lines: string[] = ctx.widgets["amq-squad-screen"]?.lines ?? [];
		return lines.find((l) => l.startsWith("  key ")) ?? `(no key line in\n${lines.join("\n")})`;
	};
	return { calls, confirms, press };
}

const SEAT = ["seat", "rev-1", "implementer", "codex", "gpt-5.4", "high"];

// Row 4 (the extension's half): the seat add writes the squad.yaml line
// through team propose and team apply --add-seat with the proposal's
// digest, asks once, naming the roster relaunch, and requests one relaunch
// carrying the key press (the confirmation's moment) as --start, which cli
// TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool shows
// reaching the new seat's launch request and schedproc and seatlaunch show
// becoming its marks.json start. Reverted (the key adding tasks only), the
// seat answers run task add.
test("add, seat: team apply with the digest, one confirmation, one relaunch carrying the key press", async () => {
	const r = rig(verbs(), SEAT);
	const before = Date.now();
	const line = await r.press();
	const after = Date.now();
	assert.equal(r.confirms.length, 1, "the confirmation is asked once");
	assert.match(r.confirms[0].title, /rev-1/);
	assert.equal(r.confirms[0].message, "this relaunches every seat; continue?");
	assert.equal(r.calls.length, 3, JSON.stringify(r.calls));
	assert.deepEqual(r.calls[0], ["team", "propose", "--project", PROJECT, "--add-seat", SPEC, "--json"]);
	assert.deepEqual(r.calls[1], ["team", "apply", "--digest", "sha256:c0ffee@d15c", "--project", PROJECT, "--add-seat", SPEC, "--json"]);
	const relaunch = r.calls[2];
	assert.deepEqual(relaunch.slice(0, 4), ["scheduler", "relaunch", "--reason", "add seat rev-1 (the screen's add key)"]);
	assert.equal(relaunch[4], "--start");
	assert.equal(relaunch[6], "--json");
	const pressed = Date.parse(relaunch[5]);
	assert.ok(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/.test(relaunch[5]), relaunch[5]);
	assert.ok(pressed >= r.confirms[0].at && pressed >= before && pressed <= after, `the start ${relaunch[5]} is not the confirmation's press`);
	assert.equal(line, `  key add: seat rev-1 added; the roster relaunches with it (every seat finishes what it holds first); its launch budget counts from ${relaunch[5]}`);
	// a pi seat with the role, the model's colon and the effort left empty
	const pi = rig(verbs(), ["seat", "local-1", "", "pi", "ollama/qwen3:32b", ""]);
	await pi.press();
	assert.deepEqual(pi.calls[0], ["team", "propose", "--project", PROJECT, "--add-seat", "local-1::pi:ollama/qwen3:32b:", "--json"]);
});

// The refusals by name: a squad.yaml that does not take the seat is
// squad_invalid before any confirmation, and nothing is written or
// relaunched; a declined confirmation writes nothing; a stale digest at
// apply shows its code and relaunches nothing; the relaunch's refusal
// shows as the verb printed it (squad_invalid from the scheduler's reload,
// or not_lead).
test("add, seat: squad_invalid and the relaunch's refusal by name; a declined confirmation writes nothing", async () => {
	const invalid = rig(verbs({ propose: res(2, "", "error: squad.yaml: seat rev-1: runtime codex is not a permitted runtime\n") }), SEAT);
	assert.equal(await invalid.press(), "  key add refused: squad_invalid: squad.yaml: seat rev-1: runtime codex is not a permitted runtime");
	assert.equal(invalid.confirms.length, 0);
	assert.equal(invalid.calls.length, 1);
	const declined = rig(verbs(), SEAT, false);
	assert.equal(await declined.press(), "  key add: cancelled, nothing written");
	assert.deepEqual(declined.calls.map((c) => c[1]), ["propose"]);
	const stale = rig(verbs({ apply: res(1, "", "error: squad_yaml_changed: squad.yaml was proposed over abc and is now def; run team propose again\n") }), SEAT);
	assert.equal(await stale.press(), "  key add refused: seat rev-1: squad_yaml_changed: squad.yaml was proposed over abc and is now def; run team propose again");
	assert.deepEqual(stale.calls.map((c) => c[1]), ["propose", "apply"]);
	for (const code of ["squad_invalid", "not_lead"]) {
		const refused = rig(verbs({ relaunch: res(15, "", `error: scheduler relaunch refused (${code}): the detail\n`) }), SEAT);
		assert.equal(await refused.press(), `  key add refused: seat rev-1 is in squad.yaml; scheduler relaunch refused (${code}): the detail`);
	}
	const noProject = rig(verbs(), SEAT, true, { ...fixture, project_path: undefined });
	assert.match(await noProject.press(), /refused: squad_invalid: status names no project/);
	assert.equal(noProject.calls.length, 0);
});

// A cancel at any of the seat's dialogs runs no verb.
test("add, seat: a cancel at any dialog runs no verb", async () => {
	for (let n = 1; n < SEAT.length; n++) {
		const answers: (string | undefined)[] = [...SEAT.slice(0, n), undefined];
		const r = rig(verbs(), answers);
		assert.equal(await r.press(), "  key add: cancelled, nothing run", JSON.stringify(answers));
		assert.equal(r.calls.length, 0, JSON.stringify(answers));
	}
});
