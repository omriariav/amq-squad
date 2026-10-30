// Acceptance 4 at unit level: the native tools reach amq and the task verbs
// with typed arguments and never through bash.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { refusedPaths, registerTools, toolArgv } from "../extensions/lib/tools.ts";
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

// 3.0.1 t16 round 4: the lead's two decisions as tools. Each runs the CLI
// verb (the scheduler proves the lead from the calling process); the widen
// tool carries the task id and the lead's reason, and only paths the task's
// outside_declared_files refusal names.
const refusedTask = {
	id: "t3",
	state: "blocked",
	block_reason: "outside_declared_files",
	block_detail: "outside_declared_files: internal/b.go, internal/c.go (declared: internal/a.go)",
};

test("argv: the relaunch and the widening are the CLI verbs; the widening takes only refused paths", () => {
	assert.deepEqual(toolArgv.schedulerRelaunch(cfg(), { reason: "rev-1 is gone" }), ["scheduler", "relaunch", "--reason", "rev-1 is gone", "--json"]);
	assert.throws(() => toolArgv.schedulerRelaunch(cfg(), { reason: " " }), /reason is required/);
	const refused = refusedPaths(refusedTask);
	assert.deepEqual(refused, ["internal/b.go", "internal/c.go"]);
	assert.deepEqual(toolArgv.taskWiden(cfg(), { id: "t3", reason: "they belong" }, refused), ["task", "widen", "t3", "--files", "internal/b.go,internal/c.go", "--reason", "they belong", "--json"]);
	assert.deepEqual(toolArgv.taskWiden(cfg(), { id: "t3", reason: "one belongs", files: ["internal/c.go"] }, refused), ["task", "widen", "t3", "--files", "internal/c.go", "--reason", "one belongs", "--json"]);
	assert.throws(() => toolArgv.taskWiden(cfg(), { id: "t3", reason: "x", files: ["internal/b.go", "/etc/passwd"] }, refused), /not a path the commit step refused for task t3: \/etc\/passwd/);
	assert.throws(() => toolArgv.taskWiden(cfg(), { id: "t3", reason: " " }, refused), /reason is required/);
	assert.throws(() => refusedPaths({ ...refusedTask, state: "pending" }), /not blocked for files outside/);
	assert.throws(() => refusedPaths({ ...refusedTask, block_reason: "size_cap" }), /not blocked for files outside/);
	assert.throws(() => refusedPaths({ ...refusedTask, block_detail: "outside_declared_files: a, b, c, d, e, f, g, h and 2 more (declared: x)" }), /more paths than it lists/);
});

test("the lead's widen tool reads the refusal and never widens by a fabricated path", async () => {
	const dir = tmpdir();
	const log = path.join(dir, "log");
	const show = JSON.stringify({ schema_version: 1, kind: "task", data: { root: "/r", task: refusedTask } });
	const bin = script(dir, "amq-squad", `echo "amq-squad $@" >> ${log}; if [ "$2" = "show" ]; then echo '${show}'; else echo '{"ok":true}'; fi`);
	const pi = new FakePi();
	const names = registerTools(pi as any, cfg({ bin, lead: true }));
	assert.ok(names.includes("amq_task_widen") && names.includes("amq_scheduler_relaunch"));
	assert.ok(!registerTools(new FakePi() as any, cfg({ bin })).includes("amq_task_widen"), "a seat has no widen tool");
	await pi.tools.get("amq_task_widen").execute("c1", { id: "t3", reason: "they belong" }, undefined, undefined, {});
	await assert.rejects(pi.tools.get("amq_task_widen").execute("c2", { id: "t3", reason: "x", files: ["internal/evil.go"] }, undefined, undefined, {}), /not a path the commit step refused/);
	await pi.tools.get("amq_scheduler_relaunch").execute("c3", { reason: "rev-1 is gone" }, undefined, undefined, {});
	const lines = fs.readFileSync(log, "utf8").trim().split("\n");
	assert.deepEqual(lines, [
		"amq-squad task show t3 --json",
		"amq-squad task widen t3 --files internal/b.go,internal/c.go --reason they belong --json",
		"amq-squad task show t3 --json",
		"amq-squad scheduler relaunch --reason rev-1 is gone --json",
	]);
});
