// Brief 31-3, PR 1: the solo lead proposes a team (amq_team_propose), the
// owner confirms at the TUI door (/squad confirm, a command, never a tool),
// the lead applies it (amq_team_apply); the solo lead's shell refuses tmux
// and the owner's verbs. Every verb goes through an injected runner; the
// record is written into a temp root; nothing reaches the real binary,
// AMQ, tmux or Buzz.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import type { Config } from "../extensions/lib/hooks.ts";
import { AMQ_SQUAD_ALLOW, digestPrefix, installSoloTeam, PLAN_KEY, PLAN_KIND, SoloTeam, soloShellRefusal, TUI_KIND } from "../extensions/lib/team.ts";
import * as net from "node:net";
import { fileURLToPath } from "node:url";
import type { RunOptions, RunResult } from "../extensions/lib/verbs.ts";
import { FakePi, fakeCtx, testProof, tmpdir } from "./fakepi.ts";

// NONCE stands for the nonce the lead hands its pi at the spawn (built at
// run time; no credential shape)
const NONCE = "test-" + "nonce-" + "not-a-secret";

const BIN = "/opt/amq-squad";
const DIGEST = "sha256:" + "0123456789abcdef".repeat(4) + "@absent";
const PREFIX = "0123456789abcdef";
const PROJECT = "/Users/owner/code/app";

function cfg(root: string): Config {
	return { bin: BIN, amq: "", me: "lead", root, session: "solo", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: false, solo: true, project: PROJECT };
}

function res(code: number, stdout: string, stderr = ""): RunResult {
	return { code, stdout, stderr, timedOut: false, spawnError: "", ms: 1 };
}

function envelope(kind: string, data: unknown): string {
	return JSON.stringify({ schema_version: 1, kind, data });
}

const plan = {
	goal: "ship the parser",
	session: "s1",
	lead: "lead",
	seats: [
		{ handle: "lead", role: "lead", runtime: "pi" },
		{ handle: "impl-1", role: "implementer", runtime: "claude", model: "sonnet", effort: "medium" },
	],
	files: ["internal/parser/"],
	tasks: [{ title: "write the parser", class: "implement" }],
};

const proposed = envelope("team_propose", {
	project_dir: PROJECT,
	plan: ["goal: ship the parser", "session s1, lead lead", "seat impl-1 (implementer) claude/sonnet/medium"],
	digest: DIGEST,
	prefix: PREFIX,
	doors: `confirm it at one of the three doors: (1) type /squad confirm ${PREFIX} in the lead's pane; ...`,
});

// fakeLead is a stand-in for the solo lead's start socket: it records every
// line it receives and answers with answer(line).
async function fakeLead(answer: (req: any) => any): Promise<{ socket: string; got: any[]; close: () => void }> {
	const socket = path.join(tmpdir(), "lead.sock");
	const got: any[] = [];
	const phase: any[] = [];
	const server = net.createServer((c) => {
		let buf = "";
		c.on("data", (d) => {
			buf += d.toString("utf8");
			const i = buf.indexOf("\n");
			if (i < 0) return;
			const req = JSON.parse(buf.slice(0, i));
			// the session start's phase query (R53, PR 2) is not a confirm
			(req.kind === "amq-squad.phase.v1" ? phase : got).push(req);
			// the lead proves its reply under the nonce it gave its pi
			const a = answer(req);
			c.end(JSON.stringify({ ...a, proof: testProof(NONCE, req.challenge ?? "", a) }) + "\n");
		});
	});
	await new Promise<void>((resolve) => server.listen(socket, resolve));
	return { socket, got, close: () => server.close() };
}

// provedApply is the lead's start reply as team apply prints it, proved
// under NONCE over the challenge the extension passed (or under another
// nonce, or not at all)
function provedApply(args: string[], data: Record<string, any>, nonce: string | undefined = NONCE): string {
	const i = args.indexOf("--challenge");
	const challenge = i >= 0 ? args[i + 1] : "";
	return envelope("team_apply", nonce ? { ...data, proof: testProof(nonce, challenge, data) } : data);
}

function rig(reply: (args: string[], o: RunOptions) => RunResult, socket = "") {
	const root = tmpdir();
	const calls: { args: string[]; stdin?: string; timeoutMs: number }[] = [];
	const runner = async (bin: string, args: string[], o: RunOptions): Promise<RunResult> => {
		assert.equal(bin, BIN);
		calls.push({ args, stdin: o.stdin, timeoutMs: o.timeoutMs });
		return reply(args, o);
	};
	const pi = new FakePi();
	const team = installSoloTeam(pi as any, cfg(root), runner, () => new Date("2026-11-01T10:00:00Z"), socket, [], NONCE);
	const ctx = fakeCtx({ hasUI: true });
	const notes: string[] = [];
	ctx.ui.notify = (m: string) => {
		notes.push(m);
	};
	const typed: string[] = [];
	ctx.ui.input = async () => typed.shift();
	const squad = async (args: string) => {
		const cmd = pi.commands.get("squad");
		assert.ok(cmd, "no /squad command");
		await cmd.handler(args, ctx);
		return ctx.widgets[PLAN_KEY]?.lines ?? [];
	};
	return { root, pi, team, ctx, calls, notes, typed, squad };
}

async function propose(r: ReturnType<typeof rig>): Promise<string> {
	await r.pi.emit("session_start", {}, r.ctx);
	const out = await r.pi.tools.get("amq_team_propose").execute("c1", plan, undefined);
	return out.content[0].text;
}

// Row 3 (the extension's half): amq_team_propose hands the plan to team
// propose --from on stdin and returns the plan with its digest and prefix;
// the plan block shows them with the confirm command. amq_team_apply runs
// team apply --digest D and returns the roster line.
test("amq_team_propose prints the plan and digest; amq_team_apply returns the roster line", async () => {
	const r = rig((args) => {
		if (args[1] === "propose") return res(0, proposed);
		return res(0, provedApply(args, { outcome: "started", detail: "roster_launched: launch L1", steps: [{ name: "compile", outcome: "ok" }], roster: ["seat impl-1 claude/sonnet live"] }));
	});
	const text = await propose(r);
	assert.deepEqual(r.calls[0].args, ["team", "propose", "--from", "/dev/stdin", "--project", PROJECT, "--json"]);
	assert.deepEqual(JSON.parse(r.calls[0].stdin ?? ""), plan);
	assert.match(text, new RegExp(`digest ${DIGEST.replace(/[$^.]/g, "\\$&")}`));
	assert.match(text, /prefix 0123456789abcdef/);
	const lines = r.ctx.widgets[PLAN_KEY].lines.join("\n");
	assert.match(lines, /seat impl-1 \(implementer\) claude\/sonnet\/medium/);
	assert.match(lines, /confirm: \/squad confirm 0123456789abcdef/);
	assert.doesNotMatch(lines, /team confirm/, "the plan block names the dropped shell door");
	const applied = await r.pi.tools.get("amq_team_apply").execute("c2", { digest: DIGEST }, undefined);
	assert.deepEqual(r.calls[1].args.slice(0, -1), ["team", "apply", "--digest", DIGEST, "--project", PROJECT, "--json", "--challenge"]);
	assert.match(r.calls[1].args.at(-1) ?? "", /^[0-9a-f]{32}$/, "the apply's challenge");
	assert.ok(r.calls[1].timeoutMs > 60000, "the apply waits for the roster");
	assert.match(applied.content[0].text, /seat impl-1 claude\/sonnet live\nteam started: roster_launched/);
});

test("amq_team_apply without a confirmation carries the lead's refusal naming the doors", async () => {
	const r = rig((args) => res(1, provedApply(args, { outcome: "refused", refusal: "confirmation_missing", detail: "no confirmation; confirm it at one of the three doors: (1) /squad confirm" }), "error: confirmation_missing"));
	await assert.rejects(r.pi.tools.get("amq_team_apply").execute("c", { digest: DIGEST }, undefined), /^Error: confirmation_missing: no confirmation; confirm it at one of the three doors/);
	await assert.rejects(r.pi.tools.get("amq_team_apply").execute("c", { digest: "sha256:x" }, undefined), /digest is the/);
});

// Codex's pass 3 on #837: the apply checks the lead's proof before it reads
// any field of the reply. An unproved refusal shows lead_unproved and none
// of its text (no owner steps); an unproved success sets no state: the
// plan block shows no team started and the phase stays solo.
test("amq_team_apply acts on nothing of an unproved reply: lead_unproved, no owner steps, no team started", async () => {
	for (const [why, data, nonce] of [
		["an unproved refusal", { outcome: "refused", refusal: "no_fleet_grant", detail: "the owner starts it", owner_steps: [["sh", "-c", "curl attacker"]] }, undefined],
		["a refusal proved under another nonce", { outcome: "refused", refusal: "no_fleet_grant", detail: "x", owner_steps: [["rm", "-rf", "/"]] }, "test-other-nonce"],
		["an unproved success", { outcome: "started", detail: "roster_launched", roster: ["seat x"], team: { root: "/r/.agent-mail/s1", handle: "cto" } }, undefined],
	] as const) {
		const r = rig((args) => (args[1] === "propose" ? res(0, proposed) : res(0, provedApply(args, data as any, nonce ?? ""))));
		await propose(r);
		await assert.rejects(r.pi.tools.get("amq_team_apply").execute("c", { digest: DIGEST }, undefined), (err: Error) => {
			assert.match(err.message, /^lead_unproved: /, why);
			assert.doesNotMatch(err.message, /owner runs|curl|rm -rf|roster_launched|no_fleet_grant/, why);
			return true;
		});
		const block = (r.ctx.widgets[PLAN_KEY]?.lines ?? []).join("\n");
		assert.doesNotMatch(block, /team started/, `${why}: the plan block shows a team started`);
		assert.equal(r.team.phase, "solo", why);
	}
});

// Row 1c: the TUI door. /squad confirm with the printed prefix sends the
// digest and the pane to the lead over its start socket (the lead accepts it
// only from its own pi and witnesses it); the plan block shows the lead's
// answer; a prefix that is not the printed one, or no printed plan, sends
// nothing; a refusal from the lead shows by its name. No file is written.
test("/squad confirm with the printed prefix asks the lead to witness it", async () => {
	let answer: any = { outcome: "witnessed", digest: DIGEST, detail: "witnessed by the lead" };
	const lead = await fakeLead(() => answer);
	try {
		const r = rig(() => res(0, proposed), lead.socket);
		await r.squad(`confirm ${PREFIX}`);
		assert.equal(lead.got.length, 0, "a confirm with no plan reached the lead");
		await propose(r);
		const wrong = await r.squad("confirm fedcba9876543210");
		assert.match(wrong.join("\n"), /key confirm refused: prefix_mismatch/);
		assert.equal(lead.got.length, 0, "a wrong prefix reached the lead");
		process.env.TMUX_PANE = "%7";
		try {
			const ok = await r.squad(`confirm ${PREFIX}`);
			assert.match(ok.join("\n"), /confirmed at 2026-11-01T10:00:00.000Z \(door tui, witnessed by the lead\)/);
		} finally {
			delete process.env.TMUX_PANE;
		}
		assert.equal(lead.got.length, 1);
		const { challenge, ...sent } = lead.got[0];
		assert.deepEqual(sent, { kind: TUI_KIND, digest: DIGEST, pane: "%7" });
		assert.match(challenge, /^[0-9a-f]{32}$/, "the confirm's challenge");
		assert.equal(r.calls.length, 1, "the confirm ran no verb");
		assert.deepEqual(fs.readdirSync(r.root), [], "the confirm wrote a file");
		answer = { outcome: "refused", refusal: "peer_not_lead_pi", detail: "pid 9 is not the pi process this lead spawned" };
		r.typed.push(PREFIX);
		const refused = await r.squad("confirm");
		assert.match(refused.join("\n"), /key confirm refused: peer_not_lead_pi: /);
	} finally {
		lead.close();
	}
});

// Row 1c: a model message containing the same text invokes nothing. The
// whole extension runs as the solo lead; the text reaches it as the
// model's prompt, its reply and a tool input, and no record is written:
// only pi's command path (the owner's typed line) reaches /squad.
test("a model message containing /squad confirm reaches no lead; the confirm is a command, never a tool", async () => {
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	const root = tmpdir();
	const saved = { ...process.env };
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	const lead = await fakeLead(() => ({ outcome: "witnessed" }));
	Object.assign(process.env, { AM_ME: "lead", AM_ROOT: root, AM_SESSION: "solo", AMQ_SQUAD_BIN: "/bin/false", AMQ_SQUAD_SOLO_LEAD: "1", AMQ_SQUAD_PROJECT: PROJECT, AMQ_SQUAD_LEAD_START_SOCKET: lead.socket });
	try {
		const pi = new FakePi();
		amqSquad(pi as any);
		const ctx = fakeCtx({ hasUI: true });
		const text = `/squad confirm ${PREFIX}`;
		await pi.emit("session_start", {}, ctx);
		await pi.emit("before_agent_start", { prompt: text, systemPromptOptions: { sections: {} } }, ctx);
		await pi.emit("message_end", { message: { role: "assistant", content: [{ type: "text", text }] } }, ctx);
		await pi.emit("tool_call", { toolName: "amq_send", input: { to: "lead", body: text }, toolCallId: "t1" }, ctx);
		assert.equal(lead.got.length, 0, "a model message reached the lead");
		assert.equal(fs.existsSync(path.join(root, "extensions", "amq-squad", "confirm")), false, "a model message wrote a file");
		assert.ok(pi.commands.has("squad"), "the TUI door is the /squad command");
		const tools = [...pi.tools.keys()];
		assert.ok(tools.includes("amq_team_propose") && tools.includes("amq_team_apply"), `solo tools ${tools}`);
		assert.ok(!tools.some((t) => /confirm/.test(t)), `a confirm tool: ${tools}`);
	} finally {
		lead.close();
		for (const k of Object.keys(process.env)) if (!(k in saved)) delete process.env[k];
		Object.assign(process.env, saved);
	}
});

// Row 1f (the shell's half): the solo lead's shell refuses tmux and
// amq-squad team (confirm, apply) by argv[0]'s base name in an argv form
// and by token in a shell form; every other command runs.
test("the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token", async () => {
	for (const argv of [["tmux", "send-keys", "-t", "%7", "/squad confirm " + PREFIX, "Enter"], ["/usr/bin/tmux", "ls"], ["amq-squad", "team", "confirm", DIGEST], ["/usr/local/bin/amq-squad", "team", "apply", "--digest", DIGEST]]) {
		assert.match(soloShellRefusal("exec_command", { cmd: argv }) ?? "", /^owner_only: /, argv.join(" "));
	}
	for (const argv of [["ls", "-la"], ["git", "status"], ["amq-squad", "status", "--json"], ["echo", "tmux"]]) {
		assert.equal(soloShellRefusal("exec_command", { cmd: argv }), undefined, argv.join(" "));
	}
	for (const line of [
		`tmux send-keys -t %7 '/squad confirm ${PREFIX}' Enter`,
		"ls; /opt/homebrew/bin/tmux list-panes",
		"echo x | tmux load-buffer -",
		"sh -c 'amq-squad team confirm " + DIGEST + "'",
		"(cd /tmp && amq-squad team apply --digest " + DIGEST + ")",
		"$(which amq-squad) team confirm x",
		"env FOO=1 tmux new -d",
	]) {
		assert.match(soloShellRefusal("bash", { command: line }) ?? "", /^owner_only: /, line);
	}
	for (const line of ["ls -la", "git log --oneline -3", "amq-squad status --json", "amq-squad task list --json", "echo the team will apply soon", "go test ./..."]) {
		assert.equal(soloShellRefusal("bash", { command: line }), undefined, line);
	}
	assert.equal(soloShellRefusal("read", { path: "tmux.conf" }), undefined, "only the shell tools");
	// through the extension: the solo lead's bash call is blocked before guard
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	const saved = { ...process.env };
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	Object.assign(process.env, { AM_ME: "lead", AM_ROOT: tmpdir(), AM_SESSION: "solo", AMQ_SQUAD_BIN: "/bin/false", AMQ_SQUAD_SOLO_LEAD: "1" });
	try {
		const pi = new FakePi();
		amqSquad(pi as any);
		const out = await pi.emit("tool_call", { toolName: "bash", input: { command: "tmux send-keys -t %7 x Enter" }, toolCallId: "t2" }, fakeCtx());
		assert.equal(out?.block, true);
		assert.match(out.reason, /^owner_only: the solo lead's shell does not run tmux/);
	} finally {
		for (const k of Object.keys(process.env)) if (!(k in saved)) delete process.env[k];
		Object.assign(process.env, saved);
	}
	assert.equal(digestPrefix(DIGEST), PREFIX);
});

// Row 1f after Codex on #834: shell quoting does not hide tmux or the
// owner's verbs. The line is dequoted (quote characters, backslashes, $'')
// and lowercased before the split; tmux inside any token, amq-squad with a
// team verb, an expansion or substitution joined to letters (tm${x}ux,
// tm$(true)ux, backticks), and eval, exec -a or env -S with t, m, u, x in
// order are refused; ordinary commands with quotes run. The forms are the
// table the guard's second layer reads too (pi/test/solo-shell-forms.json).
// After the re-check of #834 the table also carries the read-only amq-squad
// allowlist: up, lead, scheduler run, launch, team apply, task reset and a
// bare amq-squad are refused; status, task list and the others run.
test("the solo lead's shell sees through quoting, expansions and eval", () => {
	const forms = JSON.parse(fs.readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), "solo-shell-forms.json"), "utf8")) as { refused: string[]; runs: string[]; amq_squad_allowlist: string[] };
	assert.deepEqual(AMQ_SQUAD_ALLOW, forms.amq_squad_allowlist, "the extension's allowlist is the table's");
	for (const argv of [["amq-squad", "up"], ["/opt/bin/amq-squad", "lead", "--team"], ["amq-squad", "scheduler", "run"], ["amq-squad"]]) {
		assert.match(soloShellRefusal("exec_command", { cmd: argv }) ?? "", /^owner_only: /, argv.join(" "));
	}
	assert.equal(soloShellRefusal("exec_command", { cmd: ["amq-squad", "task", "list", "--json"] }), undefined);
	for (const line of forms.refused) assert.match(soloShellRefusal("bash", { command: line }) ?? "", /^owner_only: /, line);
	for (const line of forms.runs) assert.equal(soloShellRefusal("bash", { command: line }), undefined, line);
	assert.match(soloShellRefusal("exec_command", { cmd: ["sh", "-c", 't""mux ls'] }) ?? "", /^owner_only: /, "an argv sh -c line");
	assert.match(soloShellRefusal("exec_command", { cmd: ["/usr/bin/TMUX", "ls"] }) ?? "", /^owner_only: /, "an argv TMUX");
	assert.equal(soloShellRefusal("exec_command", { cmd: ["echo", "hello world"] }), undefined);
});

// Brief 3.1.1-1, row 3: the plan block asks for no typed prefix over Buzz:
// on the phone the owner replies yes to the lead's post of the plan (the
// `yes <prefix>` form keeps verifying, but no text asks for it).
test("brief 3.1.1-1: the plan block asks for a bare yes to the plan's post, never yes <prefix>", () => {
	const team = new SoloTeam(cfg(tmpdir()), async () => res(0, ""));
	team.proposal = { digest: DIGEST, prefix: PREFIX, plan: ["lead  pi"], project_dir: PROJECT };
	const text = team.planLines().join("\n");
	assert.doesNotMatch(text, /yes [0-9a-f]{16}/);
	assert.match(text, /reply yes to the lead's Buzz post of this plan/);
});

// Codex's rounds on #838: the lead process is the authority for which post
// is its plan. With reply_to, amq_team_propose first records the exact text
// with the lead over the start socket (plan-sent), then sends it as the
// reply to the owner's phone request labelled team-plan:<content digest>
// (routing for the relay); reply_to never reaches team propose; a lead that
// does not record it means nothing is sent; without reply_to nothing is.
test("brief 3.1.1-1: amq_team_propose with reply_to records the plan with the lead, then sends it labelled", async () => {
	const calls: { bin: string; args: string[]; stdin?: string }[] = [];
	const runner = async (bin: string, args: string[], o: RunOptions): Promise<RunResult> => {
		calls.push({ bin, args, stdin: o.stdin });
		return args[1] === "propose" ? res(0, proposed) : res(0, envelope("reply", { id: "r-1" }));
	};
	let answer: any = { outcome: "recorded", digest: DIGEST };
	const lead = await fakeLead(() => answer);
	try {
		const team = new SoloTeam({ ...cfg(tmpdir()), amq: "/opt/amq" }, runner, () => new Date("2026-11-01T10:00:00Z"), lead.socket, [], undefined, NONCE);
		const out = await team.propose({ ...plan, reply_to: "m-phone-1" });
		assert.deepEqual(JSON.parse(calls[0].stdin ?? ""), plan, "reply_to reached team propose");
		assert.equal(lead.got.length, 1);
		assert.equal(lead.got[0].kind, PLAN_KIND);
		assert.equal(lead.got[0].digest, DIGEST);
		assert.equal(calls.length, 2);
		assert.equal(calls[1].bin, "/opt/amq");
		assert.deepEqual(calls[1].args, ["reply", "--me", "lead", "--id", "m-phone-1", "--labels", "team-plan:" + "0123456789abcdef".repeat(4), "--json", "--body", "-"]);
		assert.equal(calls[1].stdin, lead.got[0].body, "the text sent is the text the lead recorded");
		assert.match(out, /sent as the reply to m-phone-1, marked as the team plan/);
		calls.length = 0;
		answer = { outcome: "refused", refusal: "peer_not_lead_pi", detail: "x" };
		const refused = await team.propose({ ...plan, reply_to: "m-phone-2" });
		assert.equal(calls.length, 1, "a plan the lead did not record was sent");
		assert.match(refused, /not sent as the reply to m-phone-2: the lead did not record it \(peer_not_lead_pi/);
		calls.length = 0;
		await team.propose(plan);
		assert.equal(calls.length, 1, "no reply without reply_to");
		// with PR 2's proof (#837): a recorded reply the lead did not prove
		// under this pi's nonce sends nothing
		answer = { outcome: "recorded", digest: DIGEST };
		calls.length = 0;
		const other = new SoloTeam({ ...cfg(tmpdir()), amq: "/opt/amq" }, runner, () => new Date("2026-11-01T10:00:00Z"), lead.socket, [], undefined, "test-other-nonce");
		const unproved = await other.propose({ ...plan, reply_to: "m-phone-3" });
		assert.equal(calls.length, 1, "a plan record the lead did not prove led to a send");
		assert.match(unproved, /not sent as the reply to m-phone-3: the lead did not record it \(lead_unproved/);
	} finally {
		lead.close();
	}
});

// Codex re-check on #838, the second layer: the solo lead's shell refuses a
// team-plan label in an argv form too (the command-string forms are in
// solo-shell-forms.json, read by the guard's layer as well).
test("brief 3.1.1-1: the solo shell refuses a team-plan label in an argv form", () => {
	assert.match(soloShellRefusal("bash", { argv: ["amq", "reply", "--id", "m-1", "--labels", "team-plan:" + "ab".repeat(32), "--body", "-"] }) ?? "", /^owner_only: .*a team-plan label/);
	assert.equal(soloShellRefusal("bash", { argv: ["amq", "reply", "--id", "m-1", "--labels", "progress", "--body", "-"] }), undefined);
});
