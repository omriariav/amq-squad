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
	assert.deepEqual(toolArgv.taskBlock(cfg(), { id: "t1", reason: "widen_requested", paths: ["src/*.go", "docs/a, b.md"], detail: "needed" }), ["task", "block", "t1", "--me", "pi-1", "--reason", "widen_requested", "--path", "src/*.go", "--path", "docs/a, b.md", "--detail", "needed"]);
	assert.throws(() => toolArgv.taskBlock(cfg(), { id: "t1", reason: "widen_requested" }), /at least one path/);
	assert.throws(() => toolArgv.taskBlock(cfg(), { id: "t1", reason: "decision", paths: ["x.go"] }), /only valid/);
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
	assert.deepEqual(names, ["amq_send", "amq_drain", "amq_reply", "amq_task_list", "amq_task_claim", "amq_task_done", "amq_task_block"]);
	const res = await pi.tools.get("amq_send").execute("c1", { to: "pi-lead", body: "@/etc/passwd is text" }, undefined, undefined, {});
	assert.match(res.content[0].text, /ok/);
	await pi.tools.get("amq_drain").execute("c2", {}, undefined, undefined, {});
	await pi.tools.get("amq_task_claim").execute("c3", {}, undefined, undefined, {});
	await pi.tools.get("amq_task_block").execute("c4", { id: "t1", reason: "widen_requested", paths: ["x.go"] }, undefined, undefined, {});
	const lines = fs.readFileSync(log, "utf8");
	assert.match(lines, /amq send --me pi-1 --to pi-lead --json --body -\n@\/etc\/passwd is text/);
	assert.match(lines, /amq drain --me pi-1 --include-body --json/);
	assert.match(lines, /amq-squad task claim --me pi-1/);
	assert.match(lines, /amq-squad task block t1 --me pi-1 --reason widen_requested --path x.go/);
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
// tool carries the task id and the lead's reason, and only the exact paths
// the task's outside_declared_files refusal recorded (round 7: refused_paths,
// never block_detail's display text), each as one exact --path.
const refusedTask = {
	id: "t3",
	state: "blocked",
	block_reason: "outside_declared_files",
	// The display text names other paths on purpose: it is never read.
	block_detail: "outside_declared_files: src/, evil.go (note) and 2 more (declared: internal/a.go)",
	refused_paths: ["internal/b.go", "src/ (note)", "docs/a, b.md", "src/*.go"],
};
const requestedTask = { ...refusedTask, block_reason: "widen_requested" };

test("argv: the relaunch and the widening are the CLI verbs; the widening takes only the recorded refused paths", () => {
	assert.deepEqual(toolArgv.schedulerRelaunch(cfg(), { reason: "rev-1 is gone" }), ["scheduler", "relaunch", "--reason", "rev-1 is gone", "--json"]);
	assert.throws(() => toolArgv.schedulerRelaunch(cfg(), { reason: " " }), /reason is required/);
	const refused = refusedPaths(refusedTask);
	assert.deepEqual(refused, ["internal/b.go", "src/ (note)", "docs/a, b.md", "src/*.go"]);
	assert.deepEqual(refusedPaths(requestedTask), refused);
	// Every recorded path, each one exact --path: a name with " (" or ", " is
	// one argument, a glob-like name is passed for the verb to make literal.
	assert.deepEqual(toolArgv.taskWiden(cfg(), { id: "t3", reason: "they belong" }, refused), [
		"task", "widen", "t3", "--path", "internal/b.go", "--path", "src/ (note)", "--path", "docs/a, b.md", "--path", "src/*.go", "--reason", "they belong", "--json",
	]);
	assert.deepEqual(toolArgv.taskWiden(cfg(), { id: "t3", reason: "one belongs", files: ["docs/a, b.md"] }, refused), ["task", "widen", "t3", "--path", "docs/a, b.md", "--reason", "one belongs", "--json"]);
	// Codex's three shapes no longer widen: the display-text parse of
	// "src/ (note)" (src/), a split of "docs/a, b.md" (docs/a), and a path the
	// display text names but the refusal did not record (evil.go).
	for (const f of ["src/", "docs/a", "b.md", "evil.go", "/etc/passwd", "src/a.go"]) {
		assert.throws(() => toolArgv.taskWiden(cfg(), { id: "t3", reason: "x", files: [f] }, refused), /not a path the commit step refused for task t3/, f);
	}
	assert.throws(() => toolArgv.taskWiden(cfg(), { id: "t3", reason: " " }, refused), /reason is required/);
	assert.throws(() => refusedPaths({ ...refusedTask, state: "pending" }), /not blocked for files outside/);
	assert.throws(() => refusedPaths({ ...refusedTask, block_reason: "size_cap" }), /not blocked for files outside/);
	// Other block reasons keep the pre-typed-flow reset guidance.
	assert.throws(() => refusedPaths({ ...refusedTask, block_reason: "seat_request" }), /the lead returns the task with amq-squad task reset t3 --pool RUNTIME:MODEL:EFFORT/);
	assert.throws(() => refusedPaths({ ...refusedTask, refused_paths: undefined }), /recorded no refused paths/);
	assert.throws(() => refusedPaths({ ...refusedTask, refused_paths: [] }), /recorded no refused paths/);
});

test("the lead's widen tool reads the recorded refusal and never widens by a fabricated path", async () => {
	const dir = tmpdir();
	const log = path.join(dir, "log");
	const show = JSON.stringify({ schema_version: 1, kind: "task", data: { root: "/r", task: refusedTask } });
	fs.writeFileSync(path.join(dir, "show.json"), show);
	// Each argument logged in brackets, so one argument with spaces or a
	// comma shows as one.
	const bin = script(dir, "amq-squad", `for a in "$@"; do printf '[%s]' "$a" >> ${log}; done; echo >> ${log}; if [ "$2" = "show" ]; then cat ${path.join(dir, "show.json")}; else echo '{"ok":true}'; fi`);
	const pi = new FakePi();
	const names = registerTools(pi as any, cfg({ bin, lead: true }));
	assert.ok(names.includes("amq_task_widen") && names.includes("amq_scheduler_relaunch"));
	assert.ok(!registerTools(new FakePi() as any, cfg({ bin })).includes("amq_task_widen"), "a seat has no widen tool");
	await pi.tools.get("amq_task_widen").execute("c1", { id: "t3", reason: "they belong" }, undefined, undefined, {});
	await assert.rejects(pi.tools.get("amq_task_widen").execute("c2", { id: "t3", reason: "x", files: ["internal/evil.go"] }, undefined, undefined, {}), /not a path the commit step refused/);
	fs.writeFileSync(path.join(dir, "show.json"), JSON.stringify({ schema_version: 1, kind: "task", data: { root: "/r", task: requestedTask } }));
	await pi.tools.get("amq_task_widen").execute("c3", { id: "t3", reason: "seat requested them", files: ["src/*.go"] }, undefined, undefined, {});
	await pi.tools.get("amq_scheduler_relaunch").execute("c4", { reason: "rev-1 is gone" }, undefined, undefined, {});
	const lines = fs.readFileSync(log, "utf8").trim().split("\n");
	assert.deepEqual(lines, [
		"[task][show][t3][--json]",
		"[task][widen][t3][--path][internal/b.go][--path][src/ (note)][--path][docs/a, b.md][--path][src/*.go][--reason][they belong][--json]",
		"[task][show][t3][--json]",
		"[task][show][t3][--json]",
		"[task][widen][t3][--path][src/*.go][--reason][seat requested them][--json]",
		"[scheduler][relaunch][--reason][rev-1 is gone][--json]",
	]);
});

// Brief 31-4, row 7: the two-way phone adds no lead tool. The lead replies
// on the cockpit thread with amq_reply as before (a progress reply is
// `kind: status`); the relay is the core's. The lead's tool list is the
// exact fourteen, including the seat-visible task block tool.
test("the lead's tool list is unchanged by the two-way phone except typed block: the exact fourteen", () => {
	const names = registerTools(new FakePi() as any, cfg({ lead: true }));
	assert.deepEqual(names, [
		"amq_send", "amq_drain", "amq_reply", "amq_task_list", "amq_task_claim", "amq_task_done", "amq_task_block",
		"amq_task_add", "amq_repo_read", "amq_repo_list", "amq_status", "amq_scheduler_relaunch", "amq_task_widen", "amq_task_reset",
	]);
});
