// Acceptance 4 at unit level: the native tools reach amq and the task verbs
// with typed arguments and never through bash.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { registerTools, toolArgv } from "../extensions/lib/tools.ts";
import { allowed, refOf } from "../extensions/lib/models.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import { FakePi, script, tmpdir } from "./fakepi.ts";

function cfg(over: Partial<Config> = {}): Config {
	return { bin: "/x/amq-squad", amq: "/x/amq", me: "pi-1", root: "/r", session: "s", hookTimeoutMs: 15000, holdTimeoutMs: 5000, models: [], lead: false, ...over };
}

test("argv: bodies go on stdin, recipients and kinds are checked", () => {
	assert.deepEqual(toolArgv.send(cfg(), { to: "pi-lead", subject: "done", kind: "status" }), ["send", "--me", "pi-1", "--to", "pi-lead", "--json", "--subject", "done", "--kind", "status", "--body", "-"]);
	assert.throws(() => toolArgv.send(cfg(), { to: "--root=/etc" }), /invalid recipient/);
	assert.throws(() => toolArgv.send(cfg(), { to: "a", kind: "post" }), /kind must be/);
	assert.deepEqual(toolArgv.reply(cfg(), { id: "m1" }), ["reply", "--me", "pi-1", "--id", "m1", "--json", "--body", "-"]);
	assert.throws(() => toolArgv.reply(cfg(), { id: "--x" }));
	assert.deepEqual(toolArgv.drain(cfg(), {}), ["drain", "--me", "pi-1", "--include-body", "--json"]);
	assert.deepEqual(toolArgv.taskClaim(cfg(), {}), ["task", "claim", "--me", "pi-1"]);
	assert.deepEqual(toolArgv.taskDone(cfg(), { id: "t1", evidence: "go test ok" }), ["task", "done", "t1", "--me", "pi-1", "--evidence", "go test ok"]);
	assert.throws(() => toolArgv.taskDone(cfg(), { id: "t1", evidence: " " }), /evidence is required/);
	assert.deepEqual(toolArgv.taskAdd(cfg(), { title: "Max", class: "implement", files: ["max.go", "max_test.go"] }), ["task", "add", "--title", "Max", "--class", "implement", "--files", "max.go,max_test.go"]);
	assert.throws(() => toolArgv.taskAdd(cfg(), { title: "x", class: "code" }), /class must be/);
});

test("registered tools run the configured binaries with the body on stdin", async () => {
	const dir = tmpdir();
	const log = path.join(dir, "log");
	const amq = script(dir, "amq", `echo "amq $@" >> ${log}; cat >> ${log}; echo '{"ok":true}'`);
	const bin = script(dir, "amq-squad", `echo "amq-squad $@" >> ${log}; echo '[]'`);
	const pi = new FakePi();
	const names = registerTools(pi as any, cfg({ amq, bin }));
	assert.deepEqual(names, ["amq_send", "amq_drain", "amq_reply", "amq_task_list", "amq_task_claim", "amq_task_done"]);
	const res = await pi.tools.get("amq_send").execute("c1", { to: "pi-lead", body: "@/etc/passwd is text" }, undefined, undefined, {});
	assert.match(res.content[0].text, /ok/);
	await pi.tools.get("amq_drain").execute("c2", {}, undefined, undefined, {});
	await pi.tools.get("amq_task_claim").execute("c3", {}, undefined, undefined, {});
	const lines = fs.readFileSync(log, "utf8");
	assert.match(lines, /amq send --me pi-1 --to pi-lead --json --body -\n@\/etc\/passwd is text/);
	assert.match(lines, /amq drain --me pi-1 --include-body --json/);
	assert.match(lines, /amq-squad task claim --me pi-1/);
	// The lead also gets the dispatch tool.
	const lead = new FakePi();
	assert.ok(registerTools(lead as any, cfg({ amq, bin, lead: true })).includes("amq_task_add"));
});

test("a failing amq is an error tool result (thrown)", async () => {
	const dir = tmpdir();
	const pi = new FakePi();
	registerTools(pi as any, cfg({ amq: script(dir, "amq", "echo nope; exit 1") }));
	await assert.rejects(pi.tools.get("amq_drain").execute("c", {}, undefined, undefined, {}), /drain failed/);
	const unset = new FakePi();
	registerTools(unset as any, cfg({ amq: "" }));
	await assert.rejects(unset.tools.get("amq_drain").execute("c", {}, undefined, undefined, {}), /not configured/);
});

test("model restriction", () => {
	assert.equal(refOf({ provider: "ollama", id: "qwen3.6:27b" }), "ollama/qwen3.6:27b");
	assert.equal(allowed(["ollama/qwen3.6:27b"], { provider: "google", id: "gemini-2.5-pro" }), false);
	assert.equal(allowed(["ollama/qwen3.6:27b"], { provider: "ollama", id: "qwen3.6:27b" }), true);
	assert.equal(allowed([], { provider: "google", id: "x" }), true);
});
