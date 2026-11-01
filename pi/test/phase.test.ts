// Brief 31-3 PR 2: R53's switch (rows 2 and 5) and the first turn's host
// facts (row 4), against the stand-in pi with the whole extension loaded.
// Every verb is a fake amq-squad script answering canned documents from
// files the test writes; nothing reaches the real binary, AMQ, tmux or Buzz.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { FakePi, fakeCtx, script, tmpdir } from "./fakepi.ts";

const DIGEST = "sha256:" + "0123456789abcdef".repeat(4) + "@absent";
const BUILTINS = ["read", "bash", "edit", "write"];
// The team lead's tool list as the pi lead registers it: TeamLeadTools'
// eleven (internal/piruntime/launch.go) plus amq_task_claim and
// amq_task_done (the plan reviewer: the row names the registered list until
// the one-list follow-up lands).
const TEAM13 = [
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
];

const envelope = (kind: string, data: unknown) => JSON.stringify({ schema_version: 1, kind, data });

// fakeBin is a fake amq-squad answering from files: FAKE_<VERB> paths;
// context gives its host facts only when asked with --host.
function fakeBin(dir: string): string {
	return script(
		dir,
		"amq-squad",
		`[ -n "$FAKE_LOG" ] && echo "$1" >> "$FAKE_LOG"
case "$1" in
  context) case " $* " in *" --host "*) cat "$FAKE_CONTEXT" ;; *) echo '{"data":{"seat":{"handle":"cto"}}}' ;; esac ;;
  status) [ -n "$FAKE_STATUS_FAIL" ] && exit 1; cat "$FAKE_STATUS" ;;
  team) case "$2" in propose) cat "$FAKE_PROPOSE" ;; apply) cat "$FAKE_APPLY" ;; esac ;;
  guard) echo '{"verdict":"allow"}' ;;
  *) echo '{}' ;;
esac`,
	);
}

// withEnv loads the extension into a fresh stand-in pi under env, runs fn,
// and restores the process environment (the switch rewrites it).
async function withEnv(env: Record<string, string>, fn: (pi: FakePi) => Promise<void>): Promise<void> {
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	const saved = { ...process.env };
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_") || k.startsWith("FAKE_")) delete process.env[k];
	Object.assign(process.env, env);
	try {
		const pi = new FakePi(BUILTINS);
		amqSquad(pi as any);
		await fn(pi);
	} finally {
		for (const k of Object.keys(process.env)) if (!(k in saved)) delete process.env[k];
		Object.assign(process.env, saved);
	}
}

function soloWorld() {
	const dir = tmpdir();
	const soloRoot = path.join(dir, "solo-root");
	const squadRoot = path.join(dir, "repo", ".agent-mail", "s1");
	fs.mkdirSync(path.join(soloRoot, "extensions", "amq-squad"), { recursive: true });
	const files = { FAKE_PROPOSE: path.join(dir, "propose.json"), FAKE_APPLY: path.join(dir, "apply.json"), FAKE_STATUS: path.join(dir, "status.json"), FAKE_CONTEXT: path.join(dir, "context.json") };
	fs.writeFileSync(files.FAKE_PROPOSE, envelope("team_propose", { project_dir: "/repo", plan: ["goal: x"], digest: DIGEST, prefix: "0123456789abcdef", doors: "doors" }));
	const team = { root: squadRoot, base_root: path.dirname(squadRoot), session: "s1", handle: "cto", digest: DIGEST };
	const applyReply = (withTeam: boolean) =>
		fs.writeFileSync(files.FAKE_APPLY, envelope("team_apply", { outcome: "started", detail: "roster_launched: launch L1", roster: ["seat dev codex/gpt-5.6-sol live"], ...(withTeam ? { team } : {}) }));
	applyReply(true);
	fs.writeFileSync(files.FAKE_STATUS, JSON.stringify({ root: squadRoot, session: "s1", seats: [], tasks: [], gates: [] }));
	fs.writeFileSync(files.FAKE_CONTEXT, envelope("context", { seat: { handle: "cto" } }));
	// a marker with any content: here the lead's own identity, this pi's
	// parent pid, the team's root, everything a real one carries
	const marker = () =>
		fs.writeFileSync(
			path.join(soloRoot, "extensions", "amq-squad", "phase.json"),
			JSON.stringify({ schema: 1, phase: "team", project: "/repo", root: squadRoot, base_root: path.dirname(squadRoot), session: "s1", handle: "cto", digest: DIGEST, at: "2026-11-01T09:00:00.123Z", pid: process.ppid, start: "darwin-tv:1.1" }),
		);
	const env: Record<string, string> = { AM_ME: "lead", AM_ROOT: soloRoot, AM_SESSION: "solo", AMQ_SQUAD_BIN: fakeBin(dir), AMQ_SQUAD_SOLO_LEAD: "1", AMQ_SQUAD_PROJECT: "/repo", ...files };
	return { dir, soloRoot, squadRoot, team, applyReply, marker, env };
}

// Rows 2 and 5: before the owner's yes the solo tools answer and the team
// tools are registered but not declared; amq_team_apply's success, on the
// lead's reply carrying the team identity (no file is read), switches by
// mechanism: the tool list
// declared to the model becomes exactly the thirteen team tools
// (pi.setActiveTools: read, bash, edit, write, amq_team_propose and
// amq_team_apply are gone), the lead's identity becomes the squad's, every
// solo tool call refuses phase_team (a shell call; a second apply) and the
// team tools answer. No gate-answer verb, no confirm tool.
test("R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team", async () => {
	const w = soloWorld();
	await withEnv(w.env, async (pi) => {
		const ctx = fakeCtx();
		await pi.emit("session_start", {}, ctx);
		const before = pi.getActiveTools();
		for (const t of [...BUILTINS, "amq_team_propose", "amq_team_apply"]) assert.ok(before.includes(t), `${t} is not declared before the yes`);
		for (const t of ["amq_task_add", "amq_status", "amq_scheduler_relaunch", "amq_task_widen"]) assert.ok(!before.includes(t), `${t} is declared before the yes`);
		const proposed = await pi.tools.get("amq_team_propose").execute("c1", { goal: "x", session: "s1", lead: "cto", seats: [{ handle: "cto", runtime: "pi" }] }, undefined);
		assert.match(proposed.content[0].text, /prefix 0123456789abcdef/);
		assert.ok(!fs.existsSync(path.join(w.soloRoot, "extensions", "amq-squad", "phase.json")), "a marker in the world");
		const applied = await pi.tools.get("amq_team_apply").execute("c2", { digest: DIGEST }, undefined);
		assert.match(applied.content[0].text, /team phase: your tools are now the team lead's/);
		assert.deepEqual(pi.getActiveTools().sort(), TEAM13);
		assert.ok(!pi.getActiveTools().some((t) => /gate|answer|confirm/.test(t)), "a gate-answer or confirm tool in team phase");
		assert.equal(process.env.AM_ROOT, w.squadRoot);
		assert.equal(process.env.AM_ME, "cto");
		assert.equal(process.env.AMQ_SQUAD_LEAD, "1");
		for (const call of [
			{ toolName: "bash", input: { command: "ls" } },
			{ toolName: "edit", input: { path: "a" } },
			{ toolName: "amq_team_apply", input: { digest: DIGEST } },
			{ toolName: "amq_team_propose", input: {} },
		]) {
			const out = await pi.emit("tool_call", { ...call, toolCallId: "t" }, ctx);
			assert.equal(out?.block, true, call.toolName);
			assert.match(out.reason, /^phase_team: /, call.toolName);
		}
		const st = await pi.tools.get("amq_status").execute("c3", {}, undefined);
		assert.match(st.content[0].text, /"session":"s1"/);
	});
});

// fakeLead is a stand-in for the lead's start socket answering a phase
// query with answer.
async function fakeLead(dir: string, answer: unknown): Promise<{ socket: string; got: any[]; close: () => void }> {
	const net = await import("node:net");
	const socket = path.join(dir, "lead.sock");
	const got: any[] = [];
	const server = net.createServer((c) => {
		let buf = "";
		c.on("data", (d) => {
			buf += d.toString("utf8");
			const i = buf.indexOf("\n");
			if (i < 0) return;
			got.push(JSON.parse(buf.slice(0, i)));
			c.end(JSON.stringify(answer) + "\n");
		});
	});
	await new Promise<void>((resolve) => server.listen(socket, resolve));
	return { socket, got, close: () => server.close() };
}

// Row 2 after the plan reviewer (the marker is never authority): a planted
// marker (any content: the lead's own identity, this pi's parent pid) and a
// start reply without the team identity switch nothing; a session started
// again in the same lead switches on the lead's answer to a phase query,
// not on the file; a solo lead restarted in a marked repository offers, once
// on its first turn, the team's date and digest and the two ways back, and
// stays solo.
test("R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back", async () => {
	const w = soloWorld();
	w.marker();
	w.applyReply(false);
	await withEnv(w.env, async (pi) => {
		await pi.emit("session_start", {}, fakeCtx());
		assert.ok(pi.getActiveTools().includes("bash"), "the planted marker switched at the session start");
		const offers = (await pi.emitAll("before_agent_start", { systemPromptOptions: { sections: {} } }, fakeCtx())).filter((r) => r?.message?.customType === "amq-squad-resume");
		assert.equal(offers.length, 1);
		assert.equal(offers[0].message.content, `This session's team was started on 2026-11-01T09:00:00Z with digest ${DIGEST}; run \`amq-squad up\` to resume it, or confirm it again at your pane (/squad confirm) or over Buzz.`);
		const again = (await pi.emitAll("before_agent_start", { systemPromptOptions: { sections: {} } }, fakeCtx())).filter((r) => r?.message?.customType === "amq-squad-resume");
		assert.equal(again.length, 0, "the offer said twice");
		const applied = await pi.tools.get("amq_team_apply").execute("c", { digest: DIGEST }, undefined);
		assert.match(applied.content[0].text, /the lead's reply carried no team identity/);
		assert.ok(pi.getActiveTools().includes("bash"), "the planted marker switched at the apply");
		assert.equal(process.env.AM_ROOT, w.soloRoot);
	});
	const solo = await fakeLead(w.dir, { outcome: "solo" });
	await withEnv({ ...w.env, AMQ_SQUAD_LEAD_START_SOCKET: solo.socket }, async (pi) => {
		await pi.emit("session_start", {}, fakeCtx());
		assert.deepEqual(solo.got, [{ kind: "amq-squad.phase.v1" }]);
		assert.ok(pi.getActiveTools().includes("bash"), "a solo answer switched");
	});
	solo.close();
	const team = await fakeLead(w.dir, { outcome: "team", team: w.team });
	await withEnv({ ...w.env, AMQ_SQUAD_LEAD_START_SOCKET: team.socket }, async (pi) => {
		await pi.emit("session_start", {}, fakeCtx());
		assert.deepEqual(pi.getActiveTools().sort(), TEAM13);
		assert.equal(process.env.AM_ROOT, w.squadRoot);
	});
	team.close();
});

// Row 5 for the lead up starts (lead --team): team phase from its first
// turn with no marker: the thirteen team tools, active, and no solo tool.
test("R53: lead --team is team phase from its first turn with exactly the thirteen", async () => {
	const w = soloWorld();
	const env: Record<string, string> = { ...w.env, AMQ_SQUAD_LEAD: "1", AM_ME: "cto", AM_ROOT: w.squadRoot };
	delete env.AMQ_SQUAD_SOLO_LEAD;
	await withEnv(env, async (pi) => {
		const mine = pi.getActiveTools().filter((t) => !BUILTINS.includes(t));
		assert.deepEqual(mine.sort(), TEAM13);
		assert.ok(!pi.tools.has("amq_team_propose") && !pi.tools.has("amq_team_apply"));
	});
});

// Row 4: the team lead's first turn carries the host facts from status
// --json and context --json --host as a message: the repository, the
// branch and tip, the AMQ and amq-squad versions, the roster with runtime
// and model, the open tasks (not the closed) and the open gates (not the
// answered). The second turn with nothing changed carries no host line; a
// turn after a task change carries it again; a key change with the same
// facts sends nothing; a fact missing from the verbs reads missing.
test("the team lead's first turn carries the host facts from status and context, not the model", async () => {
	const w = soloWorld();
	const status = {
		root: w.squadRoot,
		session: "s1",
		project_path: "/repo",
		producer_version: "v3.1.0-rc.2",
		seats: [
			{ handle: "cto", runtime: "pi", model: "ollama/qwen3.6:27b" },
			{ handle: "dev", runtime: "codex", model: "gpt-5.6-sol" },
		],
		tasks: [
			{ id: "t1", title: "write the parser", task_state: "pending" },
			{ id: "t2", title: "done one", task_state: "completed" },
		],
		gates: [
			{ id: "g1", action_class: "tag", state: "open" },
			{ id: "g2", action_class: "external_send", state: "accepted" },
		],
	};
	fs.writeFileSync(w.env.FAKE_STATUS, JSON.stringify(status));
	fs.writeFileSync(w.env.FAKE_CONTEXT, envelope("context", { seat: { handle: "cto" }, host: { repository: "/repo", branch: "v3-31-3b", tip: "95b12207f9a3", amq_version: "amq 0.116.2" } }));
	const log = path.join(w.dir, "calls.log");
	const env: Record<string, string> = { ...w.env, AMQ_SQUAD_LEAD: "1", AM_ME: "cto", AM_ROOT: w.squadRoot, FAKE_LOG: log };
	delete env.AMQ_SQUAD_SOLO_LEAD;
	const statusRuns = () => (fs.existsSync(log) ? fs.readFileSync(log, "utf8").split("\n").filter((l) => l === "status").length : 0);
	const gen = path.join(w.squadRoot, "extensions", "amq-squad", "tasks", ".generation");
	fs.mkdirSync(path.dirname(gen), { recursive: true });
	fs.writeFileSync(gen, "1");
	const bump = (n: string) => fs.writeFileSync(gen, n); // the task store changed
	await withEnv(env, async (pi) => {
		const turn = async () => {
			const results = await pi.emitAll("before_agent_start", { systemPromptOptions: { sections: {} } }, fakeCtx());
			return results.filter((r) => r?.message?.customType === "amq-squad-host").map((r) => r.message.content as string)[0] ?? "";
		};
		const line = await turn();
		for (const want of [
			"repository /repo;",
			"branch v3-31-3b at 95b12207f9a3;",
			"AMQ amq 0.116.2; amq-squad v3.1.0-rc.2;",
			"roster: cto pi/ollama/qwen3.6:27b, dev codex/gpt-5.6-sol;",
			'open tasks: t1 pending "write the parser";',
			"open gates: g1 tag.",
		]) {
			assert.ok(line.includes(want), `the line lacks ${JSON.stringify(want)}:\n${line}`);
		}
		assert.ok(!line.includes("t2") && !line.includes("g2"), "a closed task or an answered gate in the line");
		const runs = statusRuns();
		assert.equal(await turn(), "", "a second turn with nothing changed carried the host line again");
		assert.equal(statusRuns(), runs, "a turn with nothing changed ran status again");
		status.tasks.push({ id: "t3", title: "review it", task_state: "leased" });
		fs.writeFileSync(w.env.FAKE_STATUS, JSON.stringify(status));
		bump("22");
		assert.match(await turn(), /open tasks: t1 pending "write the parser", t3 leased "review it";/);
		bump("333"); // the key changed, the facts did not: no repeat
		assert.equal(await turn(), "", "an unchanged line was sent again");
		fs.writeFileSync(w.env.FAKE_STATUS, JSON.stringify({ root: w.squadRoot, session: "s1", tasks: [], gates: [] }));
		fs.writeFileSync(w.env.FAKE_CONTEXT, envelope("context", { seat: { handle: "cto" }, host: { repository: "/repo", branch: "", tip: "", amq_version: "" } }));
		bump("4444");
		const missing = await turn();
		for (const want of ["branch missing at missing;", "AMQ missing; amq-squad missing;", "roster: missing;", "open tasks: none;"]) {
			assert.ok(missing.includes(want), `the line lacks ${JSON.stringify(want)}:\n${missing}`);
		}
	});
});
