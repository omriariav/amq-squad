import { test } from "node:test";
import assert from "node:assert/strict";
import * as path from "node:path";
import { configFromEnv, contextSection, guardCall, ingest, evidencePayload, roleMessage, type Config } from "../extensions/lib/hooks.ts";
import { run } from "../extensions/lib/verbs.ts";
import { script, tmpdir } from "./fakepi.ts";

function cfg(bin: string, over: Partial<Config> = {}): Config {
	return { bin, amq: "", me: "pi-1", root: "/tmp/root", session: "s", hookTimeoutMs: 15000, holdTimeoutMs: 5000, models: [], lead: false, ...over };
}

const call = { toolName: "bash", input: { command: "go test ./..." }, toolCallId: "call_1", cwd: "/tmp" };

// Acceptance 3: a guard exit 1, a timeout and a missing binary each block.
test("guard fails closed: exit 1, timeout, missing binary, no verdict", async () => {
	const dir = tmpdir();
	const exit1 = script(dir, "exit1", "echo 'guard: bad payload' >&2; exit 1");
	const slow = script(dir, "slow", "sleep 5");
	const junk = script(dir, "junk", "echo not-json");
	const unknown = script(dir, "unknown", `echo '{"verdict":"maybe"}'`);
	for (const [name, bin] of [["exit 1", exit1], ["timeout", slow], ["missing", path.join(dir, "nope")], ["unset", ""], ["no verdict", junk], ["unknown verdict", unknown]]) {
		const g = await guardCall(cfg(bin, { hookTimeoutMs: 300 }), call);
		assert.equal(g.kind, "block", name);
		assert.match((g as { reason: string }).reason, /failed closed/, name);
	}
});

test("guard verdicts map to allow, block and needs_gate", async () => {
	const dir = tmpdir();
	const seen = path.join(dir, "seen");
	const allow = script(dir, "allow", `cat > ${seen}; echo '{"verdict":"allow","reason_code":"allowed"}'`);
	const g = await guardCall(cfg(allow), call);
	assert.equal(g.kind, "allow");
	const payload = JSON.parse((await import("node:fs")).readFileSync(seen, "utf8"));
	assert.deepEqual(payload, { toolName: "bash", input: { command: "go test ./..." }, toolCallId: "call_1", cwd: "/tmp" });
	const deny = script(dir, "deny", `echo '{"verdict":"deny","reason_code":"force_push","reason":"force push is refused"}'`);
	const d = await guardCall(cfg(deny), call);
	assert.equal(d.kind, "block");
	assert.match((d as { reason: string }).reason, /force_push/);
	const gate = script(dir, "gate", `echo '{"verdict":"needs_gate","reason_code":"gate_release","action_class":"tag","target":"git tag v1"}'`);
	const n = await guardCall(cfg(gate), call);
	assert.equal(n.kind, "needs_gate");
});

test("guard is called with the seat and --stdin", async () => {
	const dir = tmpdir();
	const argsFile = path.join(dir, "args");
	const bin = script(dir, "g", `echo "$@" > ${argsFile}; echo '{"verdict":"allow"}'`);
	await guardCall(cfg(bin), call);
	assert.equal((await import("node:fs")).readFileSync(argsFile, "utf8").trim(), "guard --me pi-1 --stdin");
});

test("context section states what AMQ is, the task and gates", () => {
	const s = contextSection(cfg("x"), {
		seat: { session: "s1" },
		rules: { path: "/r/team-rules.md", sha256: "sha256:ab" },
		task: { id: "t3", title: "Add Max", status: "leased", class: "implement", declared_files: ["max.go"], lease_claimed: false, lease_expires_at: "2026-10-24T10:00:00Z" },
		open_gates: [{ gate_id: "g1", class: "tag", digest: "d", state: "open" }],
	});
	assert.match(s, /Agent Message Queue/);
	assert.match(s, /not "Advanced Message Queuing"/);
	assert.match(s, /amq_task_claim/);
	assert.match(s, /max\.go/);
	assert.match(s, /g1 \(tag\)/);
	assert.match(roleMessage(cfg("x"), "# Role\nDo work."), /Agent Message Queue/);
});

// 2b-2 milestone B: a seat starts in its own working tree, never the
// repository root a task's project_path names (the R12 live finding).
test("context section names the seat's working tree", () => {
	const s = contextSection(cfg("x"), { worktree: "/w/wt-pi-1", task: null });
	assert.match(s, /Your working tree is \/w\/wt-pi-1/);
	assert.match(s, /project_path names the repository, not your working tree/);
	assert.doesNotMatch(contextSection(cfg("x"), { task: null }), /working tree/);
});

test("evidence ingest gets one tool_result payload with an outcome", async () => {
	const dir = tmpdir();
	const out = path.join(dir, "payload");
	const bin = script(dir, "ing", `echo "$@" > ${out}.args; cat > ${out}`);
	const r = await ingest(cfg(bin), { toolName: "bash", toolCallId: "c1", input: { command: "ls" }, content: [{ type: "text", text: "a\nb" }], isError: false, cwd: "/w", sessionId: "s" });
	assert.equal(r.ok, true);
	const fs = await import("node:fs");
	assert.equal(fs.readFileSync(out + ".args", "utf8").trim(), "evidence ingest --me pi-1");
	const p = JSON.parse(fs.readFileSync(out, "utf8"));
	assert.equal(p.type, "tool_result");
	assert.equal(p.outcome, "ok");
	assert.equal(p.content, "a\nb");
	assert.equal(JSON.parse(evidencePayload({ toolName: "bash", toolCallId: "c", input: {}, content: "x", isError: true, cwd: "", sessionId: "" })).outcome, "error");
	const failing = await ingest(cfg(script(dir, "f", "exit 3")), { toolName: "bash", toolCallId: "c", input: {}, content: "", isError: false, cwd: "", sessionId: "" });
	assert.equal(failing.ok, false);
});

test("config needs an AMQ identity", () => {
	assert.ok("inactive" in configFromEnv({}));
	assert.ok("inactive" in configFromEnv({ AM_ME: "Bad Name", AM_ROOT: "/r" }));
	const c = configFromEnv({ AM_ME: "pi-1", AM_ROOT: "/r", AMQ_SQUAD_MODELS: "ollama/a, ollama/b", AMQ_SQUAD_HOOK_TIMEOUT_MS: "250" });
	assert.ok(!("inactive" in c));
	if (!("inactive" in c)) {
		assert.deepEqual(c.models, ["ollama/a", "ollama/b"]);
		assert.equal(c.hookTimeoutMs, 250);
	}
});

test("run reports spawn errors and timeouts", async () => {
	const r = await run("/definitely/not/here", [], { timeoutMs: 100 });
	assert.ok(r.spawnError);
	const t = await run("/bin/sleep", ["2"], { timeoutMs: 100 });
	assert.equal(t.timedOut, true);
});
