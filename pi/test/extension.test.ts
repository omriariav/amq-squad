// The package entry against a stand-in pi: before_agent_start injection, the
// guard path on tool_call (fail closed, holds), evidence on tool_result, and
// the bridge revision 4 approval seam.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import amqSquad from "../extensions/amq-squad.ts";
import { answerName, raiseApproval, BRIDGE_REVISION, PROTOCOL } from "../extensions/lib/amq-bridge.ts";
import { FakePi, fakeCtx, script, tmpdir } from "./fakepi.ts";

async function waitFor<T>(f: () => T | undefined, ms = 15000): Promise<T> {
	const t0 = Date.now();
	for (;;) {
		let v: T | undefined;
		try {
			v = f();
		} catch {
			v = undefined;
		}
		if (v) return v;
		if (Date.now() - t0 > ms) throw new Error("waitFor timed out");
		await new Promise((r) => setTimeout(r, 50));
	}
}

type Setup = { pi: FakePi; ctx: any; root: string; dir: string; log: string };

// setup writes a fake amq-squad whose guard verdict comes from $dir/verdict
// (allow when absent), whose context prints a role, and which logs every call.
function setup(env: Record<string, string> = {}, guard = ""): Setup {
	const dir = tmpdir();
	const root = path.join(dir, "root");
	fs.mkdirSync(path.join(root, "agents", "pi-1"), { recursive: true });
	const log = path.join(dir, "calls.log");
	const ctxJson = JSON.stringify({ schema_version: 1, kind: "context", data: { seat: { handle: "pi-1", session: "s" }, role_text: "# Role: worker seat pi-1\nDo the task.", rules: { path: "/r.md", sha256: "sha256:1" }, task: null, open_gates: [] } });
	const bin = script(
		dir,
		"amq-squad",
		`echo "$1 $2" >> ${log}
case "$1" in
context) printf '%s\\n' '${ctxJson}' ;;
guard) cat > /dev/null; ${guard || `if [ -f ${dir}/verdict ]; then cat ${dir}/verdict; else echo '{"verdict":"allow"}'; fi`} ;;
evidence) cat >> ${dir}/ledger.jsonl; echo >> ${dir}/ledger.jsonl ;;
esac`,
	);
	for (const k of Object.keys(process.env)) if (k.startsWith("AMQ_SQUAD_") || k.startsWith("AM_")) delete process.env[k];
	Object.assign(process.env, { AM_ME: "pi-1", AM_ROOT: root, AM_SESSION: "s", AMQ_SQUAD_BIN: bin, AMQ_SQUAD_HOOK_TIMEOUT_MS: "15000", ...env });
	const pi = new FakePi();
	amqSquad(pi as any);
	return { pi, ctx: fakeCtx(), root, dir, log };
}

async function start(s: Setup): Promise<void> {
	await s.pi.emit("session_start", { type: "session_start", reason: "startup" }, s.ctx);
}

async function stop(s: Setup): Promise<void> {
	await s.pi.emit("session_shutdown", { type: "session_shutdown", reason: "quit" }, s.ctx);
}

function bae(): any {
	return { type: "before_agent_start", messageType: "user", prompt: "go", systemPrompt: "", systemPromptOptions: { sections: {} } };
}

test("before_agent_start injects role and context every turn and the role message once", async () => {
	const s = setup();
	await start(s);
	const e1 = bae();
	const r1 = await s.pi.emit("before_agent_start", e1, s.ctx);
	assert.equal(r1.message.customType, "amq-squad-role");
	assert.match(r1.message.content, /Role: worker seat pi-1/);
	assert.match(r1.message.content, /Agent Message Queue/);
	assert.match(e1.systemPromptOptions.sections["amq-squad-role"], /worker seat pi-1/);
	assert.match(e1.systemPromptOptions.sections["amq-squad"], /not "Advanced Message Queuing"/);
	const e2 = bae();
	const r2 = await s.pi.emit("before_agent_start", e2, s.ctx);
	assert.equal(r2, undefined, "the role message goes in the first message only");
	assert.match(e2.systemPromptOptions.sections["amq-squad-role"], /worker seat pi-1/, "the system prompt carries the role every turn");
	assert.equal(fs.readFileSync(s.log, "utf8").split("\n").filter((l) => l === "context --me").length, 2);
	await stop(s);
});

test("a resumed session that already holds the role message gets no second one", async () => {
	const s = setup();
	s.ctx.sessionManager.getBranch = () => [{ type: "custom_message", customType: "amq-squad-role" }];
	await start(s);
	assert.equal(await s.pi.emit("before_agent_start", bae(), s.ctx), undefined);
	await stop(s);
});

const toolCall = (id = "call_1", command = "touch x") => ({ type: "tool_call", toolName: "bash", toolCallId: id, input: { command } });

test("tool_call: allow passes, deny blocks, guard failure blocks", async () => {
	const s = setup();
	await start(s);
	assert.equal(await s.pi.emit("tool_call", toolCall(), s.ctx), undefined);
	fs.writeFileSync(path.join(s.dir, "verdict"), '{"verdict":"deny","reason_code":"protected_path","reason":"write to a protected path"}');
	const d = await s.pi.emit("tool_call", toolCall("call_2"), s.ctx);
	assert.equal(d.block, true);
	assert.match(d.reason, /protected_path/);
	fs.writeFileSync(path.join(s.dir, "verdict"), "garbage");
	const f = await s.pi.emit("tool_call", toolCall("call_3"), s.ctx);
	assert.equal(f.block, true);
	assert.match(f.reason, /failed closed/);
	await stop(s);
	const t = setup({ AMQ_SQUAD_HOOK_TIMEOUT_MS: "1000" }, "sleep 30");
	await start(t);
	const timed = await t.pi.emit("tool_call", toolCall(), t.ctx);
	assert.match(timed.reason, /timed out after 1000 ms/);
	await stop(t);
	const m = setup({ AMQ_SQUAD_BIN: "/no/such/amq-squad" });
	await start(m);
	const missing = await m.pi.emit("tool_call", toolCall(), m.ctx);
	assert.equal(missing.block, true);
	assert.match(missing.reason, /could not start/);
	await stop(m);
});

test("needs_gate holds until guard allows (a verified gate answer)", async () => {
	const s = setup({ AMQ_SQUAD_HOLD_TIMEOUT_MS: "5000" });
	await start(s);
	fs.writeFileSync(path.join(s.dir, "verdict"), '{"verdict":"needs_gate","reason_code":"gate_release","action_class":"tag","target":"git tag v1"}');
	const guardCalls = () => fs.readFileSync(s.log, "utf8").split("\n").filter((l) => l === "guard --me").length;
	const pending = s.pi.emit("tool_call", toolCall("call_g", "git tag v1"), s.ctx);
	// The owner's signed answer lands while the call is held: guard re-checks
	// and now allows.
	await waitFor(() => guardCalls() >= 2 || undefined);
	fs.writeFileSync(path.join(s.dir, "verdict"), '{"verdict":"allow"}');
	const r = await pending;
	assert.equal(r, undefined, "allowed once the signed answer is recorded");
	const ledger = fs.readFileSync(path.join(s.dir, "ledger.jsonl"), "utf8");
	assert.match(ledger, /"type":"hold".*"decision":"allow".*"face":"remote"/);
	await stop(s);
});

test("needs_gate times out and blocks; the same call is then capped", async () => {
	const s = setup({ AMQ_SQUAD_HOLD_TIMEOUT_MS: "200" });
	await start(s);
	fs.writeFileSync(path.join(s.dir, "verdict"), '{"verdict":"needs_gate","reason_code":"gate_send","action_class":"external_send","target":"mail x"}');
	const r = await s.pi.emit("tool_call", toolCall("c1", "mail x"), s.ctx);
	assert.equal(r.block, true);
	assert.match(r.reason, /raise the gate/);
	assert.match(r.reason, /timeout/);
	const again = await s.pi.emit("tool_call", toolCall("c2", "mail x"), s.ctx);
	assert.match(again.reason, /already blocked/);
	await stop(s);
});

test("an approval tool: a local confirm races the remote answer", async () => {
	const s = setup({ AMQ_SQUAD_APPROVE_TOOLS: "bash", AMQ_SQUAD_HOLD_TIMEOUT_MS: "3000" });
	s.ctx.hasUI = true;
	s.ctx.ui.confirm = (_t: string, _m: string, o: { signal: AbortSignal }) =>
		new Promise((res) => {
			const t = setTimeout(() => res(true), 50);
			o.signal.addEventListener("abort", () => clearTimeout(t));
		});
	await start(s);
	assert.equal(await s.pi.emit("tool_call", toolCall("a1", "ls"), s.ctx), undefined, "local confirm allowed it");
	s.ctx.ui.confirm = async () => false;
	const blocked = await s.pi.emit("tool_call", toolCall("a2", "rm -rf build"), s.ctx);
	assert.equal(blocked.block, true);
	assert.match(blocked.reason, /blocked at the terminal/);
	await stop(s);
});

// Bridge revision 4: an approval raised under a ref the bridge owns is
// answered from answers/<hash>.json, and the events record it.
test("bridge revision 4: the remote answer approves the held call under an owned ref", async () => {
	const s = setup({ AMQ_SQUAD_APPROVE_TOOLS: "bash", AMQ_SQUAD_HOLD_TIMEOUT_MS: "5000" });
	s.ctx.hasUI = true;
	s.ctx.ui.confirm = (_t: string, _m: string, o: { signal: AbortSignal }) => new Promise((res) => o.signal.addEventListener("abort", () => res(false)));
	await start(s);
	const bdir = path.join(s.root, "agents", "pi-1", "extensions", "pi-bridge");
	const live = JSON.parse(fs.readFileSync(path.join(bdir, "bridge.liveness"), "utf8"));
	assert.equal(live.bridge_revision, BRIDGE_REVISION);
	assert.equal(live.bridge_revision, 4);
	assert.equal(live.session_id, "sess-1");
	// A remote request arrives and starts.
	const ref = "amqr1_" + "a".repeat(16);
	fs.writeFileSync(path.join(bdir, "requests", `${ref}.json`), JSON.stringify({ ref, text: "run ls", deliver_as: "followUp", bridge_revision: 4, not_after: new Date(Date.now() + 60000).toISOString(), epoch_hint: live.session_generation }));
	await waitFor(() => s.pi.sent[0]);
	assert.equal(s.pi.sent[0]?.text, "run ls");
	await s.pi.emit("message_start", { type: "message_start", message: { role: "user", content: "run ls" } }, s.ctx);
	// The held call raises an interaction; the adapter answers it.
	const pending = s.pi.emit("tool_call", toolCall("call_r", "ls"), s.ctx);
	const events = () => fs.readFileSync(path.join(bdir, "events", `${ref}.jsonl`), "utf8").trim().split("\n").map((l) => JSON.parse(l));
	const interaction = await waitFor(() => events().find((e) => e.event === "interaction"));
	assert.ok(interaction, "interaction line written");
	assert.equal(interaction.presence, "remote");
	fs.writeFileSync(path.join(bdir, "answers", answerName(ref, interaction.interaction_id)), JSON.stringify({ protocol: PROTOCOL, ref, interaction_id: interaction.interaction_id, manifest_hash: interaction.manifest_hash, option: "Allow once", at: new Date().toISOString() }));
	assert.equal(await pending, undefined, "the remote answer allowed the call");
	const resolved = events().find((e) => e.event === "interaction_resolved");
	assert.equal(resolved.outcome, "answered");
	assert.equal(resolved.option, "Allow once");
	// A wrong manifest hash never applies: the second call times out on its own answer.
	const second = s.pi.emit("tool_call", toolCall("call_s", "ls -la"), s.ctx);
	const i2 = await waitFor(() => events().filter((e) => e.event === "interaction")[1]);
	fs.writeFileSync(path.join(bdir, "answers", answerName(ref, i2.interaction_id)), JSON.stringify({ protocol: PROTOCOL, ref, interaction_id: i2.interaction_id, manifest_hash: "sha256:" + "0".repeat(64), option: "Allow once" }));
	// The run ends with the approval open: run_ended.
	await new Promise((r) => setTimeout(r, 400));
	await s.pi.emit("agent_before_settle", { type: "agent_before_settle", outcome: "completed" }, s.ctx);
	await s.pi.emit("agent_settled", { type: "agent_settled" }, s.ctx);
	const ended = events().filter((e) => e.event === "interaction_resolved").pop();
	assert.equal(ended.outcome, "run_ended");
	s.ctx.signal = undefined;
	await stop(s);
	const late = await Promise.race([second, new Promise((r) => setTimeout(() => r("still held"), 50))]);
	assert.equal(late, "still held", "a mismatched answer never approves");
});

test("no owned ref: raiseApproval returns null (the local face answers)", () => {
	assert.equal(raiseApproval({ interactionId: "x", prompt: "p", manifestHash: "h", expiresAt: new Date(Date.now() + 1000), presence: "remote" }), null);
});

test("tool_result writes one evidence line", async () => {
	const s = setup();
	await start(s);
	await s.pi.emit("tool_result", { type: "tool_result", toolName: "bash", toolCallId: "c9", input: { command: "ls" }, content: [{ type: "text", text: "ok" }], isError: false }, s.ctx);
	const lines = fs.readFileSync(path.join(s.dir, "ledger.jsonl"), "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l));
	assert.equal(lines.length, 1);
	assert.equal(lines[0].type, "tool_result");
	assert.equal(lines[0].toolCallId, "c9");
	await stop(s);
});

test("without an AMQ identity the package is inactive", async () => {
	for (const k of Object.keys(process.env)) if (k.startsWith("AM_")) delete process.env[k];
	const pi = new FakePi();
	amqSquad(pi as any);
	assert.equal(pi.tools.size, 0);
	assert.equal(pi.handlers.has("tool_call"), false);
});
