// Brief 31-3 PR 2: R53's switch (rows 2 and 5) and the first turn's host
// facts (row 4), against the stand-in pi with the whole extension loaded.
// Every verb is a fake amq-squad script answering canned documents from
// files the test writes; nothing reaches the real binary, AMQ, tmux or Buzz.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { FakePi, fakeCtx, script, testProof, tmpdir } from "./fakepi.ts";

// gitHome is a scratch HOME for git with an empty global config file.
function gitHome(dir: string): string {
	const home = path.join(dir, "git-home");
	fs.mkdirSync(home, { recursive: true });
	fs.writeFileSync(path.join(home, "gitconfig"), "");
	return home;
}

// gitEnv is the environment every git in this file runs under (ruling 18):
// the scratch HOME, its empty global config, no system config, and a fixed
// author, so nothing of the owner's git setup (signing, hooks, aliases) runs.
function gitEnv(home: string): Record<string, string> {
	return {
		HOME: home,
		GIT_CONFIG_GLOBAL: path.join(home, "gitconfig"),
		GIT_CONFIG_NOSYSTEM: "1",
		GIT_AUTHOR_NAME: "t",
		GIT_AUTHOR_EMAIL: "t@example.invalid",
		GIT_COMMITTER_NAME: "t",
		GIT_COMMITTER_EMAIL: "t@example.invalid",
	};
}

// gitExec is the one way this file runs git: gitEnv, no signing, no hooks.
function gitExec(home: string, cwd: string, ...args: string[]): string {
	return execFileSync("git", ["-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "core.hooksPath=/dev/null", "-C", cwd, ...args], {
		stdio: "pipe",
		env: { ...process.env, ...gitEnv(home) },
	}).toString();
}

// NONCE stands for the nonce the lead hands its pi over fd 3 at the spawn
// (built at run time; no credential shape); STALE is an earlier spawn's
const NONCE = "test-" + "nonce-" + "not-a-secret";
const STALE = "test-" + "nonce-" + "of-an-earlier-spawn";

// APPLY_JS is the fake team apply: the reply file, proved like the lead
// does under $FAKE_APPLY_NONCE (else $FAKE_NONCE) over the --challenge the
// extension passed (an implementation apart from the extension's)
const APPLY_JS = `import fs from "node:fs";
import crypto from "node:crypto";
const args = process.argv.slice(2);
const i = args.indexOf("--challenge");
const challenge = i >= 0 ? args[i + 1] : "";
const env = JSON.parse(fs.readFileSync(process.env.FAKE_APPLY, "utf8"));
const d = env.data;
const nonce = process.env.FAKE_APPLY_NONCE ?? process.env.FAKE_NONCE ?? "";
if (nonce && challenge) {
	const t = d.team ?? {};
	const fields = ["amq-squad.lead-proof.v2", challenge, d.outcome ?? "", d.digest ?? "", t.root ?? "", t.base_root ?? "", t.session ?? "", t.handle ?? "", t.digest ?? ""];
	const msg = fields.map((f) => Buffer.byteLength(f, "utf8") + ":" + f + ",").join("");
	d.proof = crypto.createHmac("sha256", nonce).update(msg).digest("hex");
}
process.stdout.write(JSON.stringify(env));
`;

// nonceFds are the descriptors withEnv opened for the extension's nonce
const nonceFds: number[] = [];

const DIGEST = "sha256:" + "0123456789abcdef".repeat(4) + "@absent";
const BUILTINS = ["read", "bash", "edit", "write"];
// The team lead's tool list: TeamLeadTools' eleven
// (internal/piruntime/launch.go), one list (brief 3.1.1-2 item 4);
// amq_task_claim, amq_task_done and amq_task_block are a seat's, never a
// lead's.
const TEAM11 = [
	"amq_drain",
	"amq_reply",
	"amq_repo_list",
	"amq_repo_read",
	"amq_scheduler_relaunch",
	"amq_send",
	"amq_status",
	"amq_task_add",
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
  team) case "$2" in propose) cat "$FAKE_PROPOSE" ;; apply) node "$FAKE_APPLY_JS" "$@" ;; esac ;;
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
	// the lead's nonce reaches the extension as a descriptor, as over the
	// spawn's pipe (here a file opened for it; the extension closes it)
	if (env.FAKE_NONCE) {
		const f = path.join(fs.mkdtempSync(path.join(process.env.TMPDIR ?? "/tmp", "nonce-")), "nonce");
		fs.writeFileSync(f, env.FAKE_NONCE + "\n");
		const fd = fs.openSync(f, "r");
		nonceFds.push(fd);
		process.env.AMQ_SQUAD_LEAD_NONCE_FD = String(fd);
	}
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
	const files = { FAKE_PROPOSE: path.join(dir, "propose.json"), FAKE_APPLY: path.join(dir, "apply.json"), FAKE_STATUS: path.join(dir, "status.json"), FAKE_CONTEXT: path.join(dir, "context.json"), FAKE_APPLY_JS: path.join(dir, "apply.mjs") };
	fs.writeFileSync(files.FAKE_APPLY_JS, APPLY_JS);
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
	const env: Record<string, string> = { AM_ME: "lead", AM_ROOT: soloRoot, AM_SESSION: "solo", AMQ_SQUAD_BIN: fakeBin(dir), AMQ_SQUAD_SOLO_LEAD: "1", AMQ_SQUAD_PROJECT: "/repo", FAKE_NONCE: NONCE, ...files };
	return { dir, soloRoot, squadRoot, team, applyReply, marker, env };
}

// Rows 2 and 5: before the owner's yes the solo tools answer and the team
// tools are registered but not declared; amq_team_apply's success, on the
// lead's reply carrying the team identity (no file is read; phase.json is
// even a directory), switches by
// mechanism: the tool list
// declared to the model becomes exactly the eleven team tools
// (pi.setActiveTools: read, bash, edit, write, amq_team_propose and
// amq_team_apply are gone), the lead's identity becomes the squad's, every
// solo tool call refuses phase_team (a shell call; a second apply) and the
// team tools answer. No gate-answer verb, no confirm tool.
test("R53: the owner's yes switches the lead's tools to exactly the team lead's eleven and refuses the solo set phase_team", async () => {
	const w = soloWorld();
	await withEnv(w.env, async (pi) => {
		const ctx = fakeCtx();
		await pi.emit("session_start", {}, ctx);
		const before = pi.getActiveTools();
		for (const t of [...BUILTINS, "amq_team_propose", "amq_team_apply"]) assert.ok(before.includes(t), `${t} is not declared before the yes`);
		for (const t of ["amq_task_add", "amq_status", "amq_scheduler_relaunch", "amq_task_widen"]) assert.ok(!before.includes(t), `${t} is declared before the yes`);
		// every call goes through pi's batch: prepared by the hooks, then run
		const [proposed] = await pi.batch([{ toolName: "amq_team_propose", input: { goal: "x", session: "s1", lead: "cto", seats: [{ handle: "cto", runtime: "pi" }] } }], ctx);
		assert.equal(proposed.outcome, "ran", proposed.reason);
		assert.match(proposed.text ?? "", /prefix 0123456789abcdef/);
		// the model made phase.json a directory before the yes: the record
		// cannot be written, and the switch, riding the reply, happens anyway
		fs.mkdirSync(path.join(w.soloRoot, "extensions", "amq-squad", "phase.json"));
		const [applied] = await pi.batch([{ toolName: "amq_team_apply", input: { digest: DIGEST } }], ctx);
		assert.equal(applied.outcome, "ran", applied.reason);
		assert.match(applied.text ?? "", /team phase: your tools are now the team lead's/);
		assert.deepEqual(pi.getActiveTools().sort(), TEAM11);
		assert.ok(!pi.getActiveTools().some((t) => /gate|answer|confirm/.test(t)), "a gate-answer or confirm tool in team phase");
		assert.equal(process.env.AM_ROOT, w.squadRoot);
		assert.equal(process.env.AM_ME, "cto");
		assert.equal(process.env.AMQ_SQUAD_LEAD, "1");
		// a model that names the solo tools anyway (pi's turn list could
		// still carry them) meets the hook: each refuses phase_team
		// a seat's claim, done and block too (brief 3.1.1-2 item 4): a lead
		// holding them could take a lease itself or request its own widen
		pi.active = new Set([...pi.active, ...BUILTINS, "amq_team_apply", "amq_team_propose", "amq_task_claim", "amq_task_done", "amq_task_block"]);
		const solo = await pi.batch(
			[
				{ toolName: "bash", input: { command: "ls" } },
				{ toolName: "edit", input: { path: "a" } },
				{ toolName: "amq_team_apply", input: { digest: DIGEST } },
				{ toolName: "amq_team_propose", input: {} },
				{ toolName: "amq_task_claim", input: {} },
				{ toolName: "amq_task_done", input: { id: "t1", evidence: "x" } },
				{ toolName: "amq_task_block", input: { id: "t1", reason: "widen_requested", paths: ["x.go"] } },
			],
			ctx,
		);
		for (const r of solo) {
			assert.equal(r.outcome, "blocked", r.toolName);
			assert.match(r.reason ?? "", /^phase_team: /, r.toolName);
		}
		pi.setActiveTools(TEAM11);
		const [st] = await pi.batch([{ toolName: "amq_status" }], ctx);
		assert.match(st.text ?? "", /"session":"s1"/);
	});
});

// fakeLead is a stand-in for the lead's start socket answering a phase
// query with answer, proved under nonce when one is given (the lead that
// spawned this pi holds NONCE; a server that is not the lead holds none).
async function fakeLead(dir: string, answer: Record<string, unknown>, nonce = ""): Promise<{ socket: string; got: any[]; close: () => void }> {
	const net = await import("node:net");
	const socket = path.join(dir, "lead.sock");
	const got: any[] = [];
	const server = net.createServer((c) => {
		let buf = "";
		c.on("data", (d) => {
			buf += d.toString("utf8");
			const i = buf.indexOf("\n");
			if (i < 0) return;
			const req = JSON.parse(buf.slice(0, i));
			got.push(req);
			c.end(JSON.stringify(nonce ? { ...answer, proof: testProof(nonce, req.challenge ?? "", answer as any) } : answer) + "\n");
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
		// both working paths (up after quitting; a fresh proposal) and why
		// the old plan cannot be confirmed (no proposal; the 30 minutes)
		assert.equal(
			offers[0].message.content,
			`This repository's team was started on 2026-11-01T09:00:00Z with digest ${DIGEST} and this lead was restarted. ` +
				"To resume it: quit this lead and run `amq-squad up`. " +
				"To start it again from here: ask me to propose the team again and confirm the new plan at your pane or over Buzz. " +
				"The old plan cannot be confirmed again: a restarted lead holds no proposal, and a confirmation stands for 30 minutes.",
		);
		const again = (await pi.emitAll("before_agent_start", { systemPromptOptions: { sections: {} } }, fakeCtx())).filter((r) => r?.message?.customType === "amq-squad-resume");
		assert.equal(again.length, 0, "the offer said twice");
		const [applied] = await pi.batch([{ toolName: "amq_team_apply", input: { digest: DIGEST } }], fakeCtx());
		assert.match(applied.text ?? "", /the lead's reply carried no team identity/);
		assert.ok(pi.getActiveTools().includes("bash"), "the planted marker switched at the apply");
		assert.equal(process.env.AM_ROOT, w.soloRoot);
	});
	// each fake lead closes in a finally: a failed assertion leaves no
	// listening socket to keep the test process alive
	const solo = await fakeLead(w.dir, { outcome: "solo" }, NONCE);
	try {
		await withEnv({ ...w.env, AMQ_SQUAD_LEAD_START_SOCKET: solo.socket }, async (pi) => {
			await pi.emit("session_start", {}, fakeCtx());
			assert.equal(solo.got.length, 1);
			assert.equal(solo.got[0].kind, "amq-squad.phase.v1");
			assert.match(solo.got[0].challenge, /^[0-9a-f]{32}$/, "the phase query's challenge");
			assert.ok(!JSON.stringify(solo.got[0]).includes(NONCE), "the nonce crossed the socket");
			assert.ok(pi.getActiveTools().includes("bash"), "a solo answer switched");
		});
	} finally {
		solo.close();
	}
	const team = await fakeLead(w.dir, { outcome: "team", team: w.team }, NONCE);
	try {
		await withEnv({ ...w.env, AMQ_SQUAD_LEAD_START_SOCKET: team.socket }, async (pi) => {
			await pi.emit("session_start", {}, fakeCtx());
			assert.deepEqual(pi.getActiveTools().sort(), TEAM11);
			assert.equal(process.env.AM_ROOT, w.squadRoot);
		});
	} finally {
		team.close();
	}
});

// R53 against pi's batch (Codex on #837, item 3): pi prepares a batch's
// calls, hooks included, before running any, so a shell call admitted while
// solo could run after the switch. amq_team_apply runs only alone: batched
// with any other call (before it or after it, and even when the assistant
// message's end was not seen) it refuses apply_not_alone, tells the model
// to call it alone on its next turn, and starts nothing; alone it
// switches. It is a sequential tool, so pi runs nothing beside it. A solo
// tool admitted while solo whose execute runs after the switch refuses
// phase_team at run time.
test("R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team", async () => {
	const w = soloWorld();
	const log = path.join(w.dir, "verbs.log");
	const teamRuns = () => (fs.existsSync(log) ? fs.readFileSync(log, "utf8").split("\n").filter((l) => l === "team").length : 0);
	await withEnv({ ...w.env, FAKE_LOG: log }, async (pi) => {
		const ctx = fakeCtx();
		await pi.emit("session_start", {}, ctx);
		assert.equal(pi.tools.get("amq_team_apply").executionMode, "sequential");
		for (const [why, calls, opts] of [
			["bash before the apply", [{ toolName: "bash", input: { command: "ls" } }, { toolName: "amq_team_apply", input: { digest: DIGEST } }], {}],
			["bash after the apply", [{ toolName: "amq_team_apply", input: { digest: DIGEST } }, { toolName: "bash", input: { command: "ls" } }], {}],
			["the message's end unseen", [{ toolName: "read", input: { path: "a" } }, { toolName: "amq_team_apply", input: { digest: DIGEST } }], { noMessageEnd: true }],
		] as const) {
			const out = await pi.batch(calls as any, ctx, opts as any);
			const apply = out.find((r) => r.toolName === "amq_team_apply")!;
			assert.equal(apply.outcome, "error", why);
			assert.match(apply.reason ?? "", /^apply_not_alone: /, why);
			assert.match(apply.reason ?? "", /Call amq_team_apply again on your next turn with nothing else in the batch\./, why);
			assert.equal(teamRuns(), 0, `${why}: the apply ran`);
			assert.ok(pi.getActiveTools().includes("bash"), `${why}: the tools switched`);
			assert.equal(process.env.AM_ROOT, w.soloRoot, why);
		}
		const [alone] = await pi.batch([{ toolName: "amq_team_apply", input: { digest: DIGEST } }], ctx);
		assert.equal(alone.outcome, "ran", alone.reason);
		assert.deepEqual(pi.getActiveTools().sort(), TEAM11);
	});
	// a propose admitted while solo, the switch landing between the
	// preparing and the running (here a session start the lead answers
	// team): its execute refuses phase_team
	const lead = await fakeLead(w.dir, { outcome: "team", team: w.team }, NONCE);
	try {
		await withEnv({ ...w.env, AMQ_SQUAD_LEAD_START_SOCKET: lead.socket }, async (pi) => {
			const ctx = fakeCtx();
			const [late] = await pi.batch([{ toolName: "amq_team_propose", input: { goal: "x", session: "s1", lead: "cto", seats: [{ handle: "cto", runtime: "pi" }] } }], ctx, {
				between: async () => {
					await pi.emit("session_start", {}, ctx);
				},
			});
			assert.equal(lead.got.length, 1, "no phase query in the gap");
			assert.equal(late.outcome, "error");
			assert.match(late.reason ?? "", /^phase_team: amq_team_propose/);
			assert.ok(!pi.ran.includes("amq_team_propose"));
		});
	} finally {
		lead.close();
	}
});

// Codex's re-check on #837, item 1: only the lead that spawned this pi is
// believed. The nonce comes over a descriptor the extension closes at load
// (and the environment's pointer to it is gone); a server on the socket
// path that is not the lead (it holds no nonce) answers team and pi stays
// solo; a lead of an earlier spawn (its nonce is not this pi's) answers
// team and pi stays solo; the lead that spawned it switches it. The same
// proof guards the start's reply amq_team_apply acts on. The fixed vector
// is the one Go's row checks.
test("R53: only the lead that spawned this pi is believed (lead_unproved), at a session start and at the apply", async () => {
	const { leadProof } = await import("../extensions/lib/team.ts");
	const vectorReply = { outcome: "team", team: { root: "/r/.agent-mail/s1", base_root: "/r/.agent-mail", session: "s1", handle: "cto", digest: "sha256:" + "ab".repeat(32) + "@absent" } };
	const vector = "70e5982a1f9b362bb43906fe55a452f5ec5bff328288992a6100e54b40fed165";
	assert.equal(leadProof("test-nonce-not-a-secret", "0123456789abcdef".repeat(2), vectorReply), vector);
	assert.equal(testProof("test-nonce-not-a-secret", "0123456789abcdef".repeat(2), vectorReply), vector);
	// Codex's pass 3 on #837: two identities that a newline join made one
	// (a newline inside a repository path) sign differently
	const one = { outcome: "team", team: { root: "/a\n/b", base_root: "/c", session: "s1", handle: "cto", digest: "d" } };
	const two = { outcome: "team", team: { root: "/a", base_root: "/b\n/c", session: "s1", handle: "cto", digest: "d" } };
	assert.equal([one.team.root, one.team.base_root].join("\n"), [two.team.root, two.team.base_root].join("\n"));
	assert.notEqual(leadProof("n", "c", one), leadProof("n", "c", two), "two different identities sign alike");
	const w = soloWorld();
	for (const [why, nonce] of [
		["a server that is not the lead", ""],
		["a lead of an earlier spawn", STALE],
	] as const) {
		const fake = await fakeLead(w.dir, { outcome: "team", team: w.team }, nonce);
		try {
			await withEnv({ ...w.env, AMQ_SQUAD_LEAD_START_SOCKET: fake.socket }, async (pi) => {
				const fd = nonceFds.at(-1)!;
				assert.equal(process.env.AMQ_SQUAD_LEAD_NONCE_FD, undefined, "the descriptor's name stayed in the environment");
				assert.throws(() => fs.fstatSync(fd), /EBADF/, "the nonce's descriptor stayed open");
				await pi.emit("session_start", {}, fakeCtx());
				assert.equal(fake.got.length, 1, why);
				assert.ok(pi.getActiveTools().includes("bash"), `${why} switched the lead`);
				assert.equal(process.env.AM_ROOT, w.soloRoot, why);
			});
		} finally {
			fake.close();
		}
	}
	// the start's reply, proved by an earlier spawn's lead, or not at all
	for (const [why, applyNonce] of [
		["a start reply proved under an earlier spawn's nonce", STALE],
		["a start reply with no proof", ""],
	] as const) {
		await withEnv({ ...w.env, FAKE_APPLY_NONCE: applyNonce }, async (pi) => {
			await pi.emit("session_start", {}, fakeCtx());
			const [applied] = await pi.batch([{ toolName: "amq_team_apply", input: { digest: DIGEST } }], fakeCtx());
			assert.equal(applied.outcome, "error", why);
			assert.match(applied.reason ?? "", /^lead_unproved: the reply on the start socket carries no proof from the lead that spawned this pi/, why);
			assert.ok(pi.getActiveTools().includes("bash"), `${why} switched the lead`);
			assert.equal(process.env.AM_ROOT, w.soloRoot, why);
		});
	}
	// a pi with no nonce (not spawned by the lead) believes no reply
	await withEnv({ ...w.env, FAKE_NONCE: "", FAKE_APPLY_NONCE: NONCE }, async (pi) => {
		const [applied] = await pi.batch([{ toolName: "amq_team_apply", input: { digest: DIGEST } }], fakeCtx());
		assert.match(applied.reason ?? "", /^lead_unproved: this pi holds no nonce from a lead/);
		assert.ok(pi.getActiveTools().includes("bash"));
	});
});

// The plan reviewer's order for the nonce: the descriptor is read and
// closed before the extension registers any handler, tool or command, so
// no tool call can ever run with it open. A stand-in pi checks the
// descriptor at its first registration of any kind.
test("R53: the nonce's descriptor is closed before the extension's first registration", async () => {
	const { default: amqSquad } = await import("../extensions/amq-squad.ts");
	const w = soloWorld();
	const saved = { ...process.env };
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_") || k.startsWith("FAKE_")) delete process.env[k];
	Object.assign(process.env, w.env);
	const f = path.join(w.dir, "nonce");
	fs.writeFileSync(f, NONCE + "\n");
	const fd = fs.openSync(f, "r");
	process.env.AMQ_SQUAD_LEAD_NONCE_FD = String(fd);
	const firsts: string[] = [];
	const closedAt = (what: string) => {
		if (firsts.length) return;
		let open = true;
		try {
			fs.fstatSync(fd);
		} catch {
			open = false;
		}
		firsts.push(`${what}:${open ? "open" : "closed"}`);
	};
	class Watching extends FakePi {
		on(name: string, h: any) {
			closedAt(`on ${name}`);
			return super.on(name, h);
		}
		registerTool(def: any) {
			closedAt(`tool ${def.name}`);
			super.registerTool(def);
		}
		registerCommand(name: string, def: any) {
			closedAt(`command ${name}`);
			super.registerCommand(name, def);
		}
	}
	try {
		const pi = new Watching(BUILTINS);
		amqSquad(pi as any);
		assert.equal(firsts.length, 1, "the extension registered nothing");
		assert.match(firsts[0], /:closed$/, `the first registration found the nonce's descriptor open (${firsts[0]})`);
		assert.ok(pi.tools.has("amq_team_apply"));
	} finally {
		try {
			fs.closeSync(fd);
		} catch {
			// closed by the extension
		}
		for (const k of Object.keys(process.env)) if (!(k in saved)) delete process.env[k];
		Object.assign(process.env, saved);
	}
});

// Row 5 for the lead up starts (lead --team): team phase from its first
// turn with no marker: the eleven team tools, active, and no solo tool.
test("R53: lead --team is team phase from its first turn with exactly the eleven", async () => {
	const w = soloWorld();
	const env: Record<string, string> = { ...w.env, AMQ_SQUAD_LEAD: "1", AM_ME: "cto", AM_ROOT: w.squadRoot };
	delete env.AMQ_SQUAD_SOLO_LEAD;
	await withEnv(env, async (pi) => {
		const mine = pi.getActiveTools().filter((t) => !BUILTINS.includes(t));
		assert.deepEqual(mine.sort(), TEAM11);
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
		// Codex on #837, item 6: a credential in a branch name that reached
		// the extension unredacted is redacted before it is interpolated
		// (the canary is built at run time and matches no real shape)
		const canary = "Zq9" + "xV7mP2wL4";
		fs.writeFileSync(w.env.FAKE_CONTEXT, envelope("context", { seat: { handle: "cto" }, host: { repository: "/repo", branch: `feature/api_key=${canary}`, tip: "", amq_version: "" } }));
		bump("55555");
		const redacted = await turn();
		assert.ok(!redacted.includes(canary), `the canary reached the host line:\n${redacted}`);
		assert.ok(redacted.includes("branch feature/api_key=<redacted> at missing;"), redacted);
	});
});

// Codex's re-check on #837, item 2: the extension gives context --host more
// time than Go's one budget for all its host commands (3 s) plus the verb's
// own work, so Go always kills its command groups before the extension
// kills it; a plain context keeps the hook timeout.
test("context --host's timeout is above Go's host budget", async () => {
	const { contextOf } = await import("../extensions/lib/hooks.ts");
	const seen: { args: string[]; timeoutMs?: number }[] = [];
	const runner = async (_bin: string, args: string[], opts: { timeoutMs?: number }) => {
		seen.push({ args, timeoutMs: opts.timeoutMs });
		return { code: 0, stdout: "{}", stderr: "", timedOut: false } as any;
	};
	const cfg = { bin: "/x/amq-squad", me: "cto", hookTimeoutMs: 5000 } as any;
	await contextOf(cfg, runner as any, true);
	await contextOf(cfg, runner as any, false);
	assert.ok(seen[0].args.includes("--host"));
	assert.ok((seen[0].timeoutMs ?? 0) >= 3000 + 5000, `context --host timeout ${seen[0].timeoutMs}`);
	assert.equal(seen[1].timeoutMs, 5000);
});

// Codex's re-check on #837, item 4: the host line's key follows the real
// changes. In a linked worktree (its .git is a pointer file, as in this
// checkout) a commit refreshes; a gate decision (a file under answers/)
// refreshes; a gate whose not_after passes refreshes with time alone; the
// lead's launch record, the AMQ executable and the amq-squad binary are
// keyed. Each refresh is
// the verbs running again; nothing changed runs nothing.
test("the host line refreshes on a linked worktree's HEAD, a gate decision, an expired gate, the launch record and the AMQ executable", async () => {
	const { HostFacts } = await import("../extensions/lib/firstturn.ts");
	const { run } = await import("../extensions/lib/verbs.ts");
	const w = soloWorld();
	const home = gitHome(w.dir);
	const git = (cwd: string, ...args: string[]) => gitExec(home, cwd, ...args);
	const repo = path.join(w.dir, "repo-git");
	const wt = path.join(w.dir, "wt");
	fs.mkdirSync(repo);
	git(repo, "init", "-q");
	git(repo, "commit", "-q", "--allow-empty", "-m", "one");
	git(repo, "worktree", "add", "-q", wt, "-b", "wtb");
	assert.ok(fs.statSync(path.join(wt, ".git")).isFile(), "the worktree's .git is not a pointer file");
	const ext = path.join(w.squadRoot, "extensions", "amq-squad");
	for (const d of ["gates", "answers", "denials", path.join("consumed", "gates"), path.join("launch", "cto")]) fs.mkdirSync(path.join(ext, d), { recursive: true });
	let now = new Date("2026-11-01T09:00:00Z");
	fs.writeFileSync(path.join(ext, "gates", "g1.json"), JSON.stringify({ gate_id: "g1", class: "tag", digest: "d", not_after: "2026-11-01T09:01:00Z" }));
	fs.writeFileSync(path.join(ext, "launch", "cto", "launch.json"), "{}");
	const amq = path.join(w.dir, "amq");
	fs.writeFileSync(amq, "#!/bin/sh\n", { mode: 0o755 });
	const log = path.join(w.dir, "key.log");
	// the extension's own git rev-parse runs under the same scratch setup
	const env: Record<string, string> = { ...w.env, ...gitEnv(home), AMQ_SQUAD_LEAD: "1", AM_ME: "cto", AM_ROOT: w.squadRoot, FAKE_LOG: log, AMQ_SQUAD_PROJECT: wt, AMQ_SQUAD_AMQ: amq };
	delete env.AMQ_SQUAD_SOLO_LEAD;
	const statusRuns = () => (fs.existsSync(log) ? fs.readFileSync(log, "utf8").split("\n").filter((l) => l === "status").length : 0);
	await withEnv(env, async () => {
		const cfg = { bin: env.AMQ_SQUAD_BIN, root: w.squadRoot, me: "cto", hookTimeoutMs: 5000, project: wt } as any;
		const h = new HostFacts(cfg, run, undefined, () => now);
		let runs = 0;
		const refreshes = async (why: string, yes: boolean) => {
			await h.next();
			const n = statusRuns();
			assert.equal(n > runs, yes, `${why}: the verbs ${yes ? "did not run" : "ran"} (status runs ${runs} then ${n})`);
			runs = n;
		};
		await refreshes("the first turn", true);
		await refreshes("nothing changed", false);
		git(wt, "commit", "-q", "--allow-empty", "-m", "two");
		await refreshes("a commit in the linked worktree", true);
		// the branch moved from the main checkout: only the ref in the
		// common directory changes (the worktree's HEAD and its log do not)
		const moved = git(repo, "commit-tree", "wtb^{tree}", "-p", "wtb", "-m", "three").trim();
		git(repo, "update-ref", "refs/heads/wtb", moved);
		await refreshes("the worktree's branch moved from the main checkout", true);
		fs.writeFileSync(path.join(ext, "answers", "g1.json"), JSON.stringify({ gate_id: "g1", decision: "approved" }));
		await refreshes("a gate decision under answers/", true);
		now = new Date("2026-11-01T09:02:00Z");
		await refreshes("a gate's not_after passed", true);
		await refreshes("nothing changed after the expiry", false);
		fs.writeFileSync(path.join(ext, "launch", "cto", "launch.json"), '{"handle":"cto"}');
		await refreshes("the launch record changed", true);
		fs.writeFileSync(amq, "#!/bin/sh\necho amq 0.117.0\n", { mode: 0o755 });
		await refreshes("the AMQ executable changed", true);
		fs.appendFileSync(env.AMQ_SQUAD_BIN, "\n# upgraded\n");
		await refreshes("the amq-squad binary changed", true);
	});
});

// Ruling 18 for the git the tests run: gitExec is the only place this file
// starts a process directly (the fake verbs run through the extension), so
// every git here runs with the scratch HOME, the empty global config, no
// system config, no signing and no hooks.
test("gitExec is the only process exec in the phase tests", () => {
	const src = fs.readFileSync(fileURLToPath(import.meta.url), "utf8");
	const execs = src.match(new RegExp("\\b(exec" + "FileSync|exec" + "Sync|spawn" + "Sync|spawn|exec" + "File)\\(", "g")) ?? [];
	assert.equal(execs.length, 1, `process execs in the file: ${execs.join(", ")}`);
	// the one exec sits in gitExec's body, with no signing and no hooks
	const at = src.indexOf(execs[0]);
	const fn = src.lastIndexOf("\nfunction ", at);
	assert.ok(src.startsWith("\nfunction gitExec(", fn), "the one exec is not inside gitExec");
	const body = src.slice(fn, src.indexOf("\n}\n", at));
	for (const flag of ['"commit.gpgsign=false"', '"core.hooksPath=/dev/null"', "...gitEnv(home)"]) assert.ok(body.includes(flag), `gitExec lacks ${flag}`);
	const env = gitEnv("/h");
	assert.equal(env.GIT_CONFIG_NOSYSTEM, "1");
	assert.equal(env.GIT_CONFIG_GLOBAL, "/h/gitconfig");
	assert.equal(env.HOME, "/h");
});
