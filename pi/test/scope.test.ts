// The in-session task scope (a hint; the harness commit step enforces) and
// the lead's reset tool.
import { test } from "node:test";
import assert from "node:assert/strict";
import { declaredMatch, outsideDeclared } from "../extensions/lib/hooks.ts";
import { registerTools, toolArgv } from "../extensions/lib/tools.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import { FakePi } from "./fakepi.ts";

const task = { id: "t1", title: "Max", status: "in_progress", class: "implement", declared_files: ["max.go", "max_test.go", "docs/", "pkg/*.go"] };

test("writes outside the task's declared files are blocked in session", () => {
	assert.equal(outsideDeclared(task, "write", { path: "max.go" }, "/repo"), "");
	assert.equal(outsideDeclared(task, "edit", { path: "/repo/max_test.go" }, "/repo"), "");
	assert.equal(outsideDeclared(task, "write", { path: "docs/a/b.md" }, "/repo"), "");
	assert.equal(outsideDeclared(task, "write", { path: "pkg/x.go" }, "/repo"), "");
	assert.match(outsideDeclared(task, "edit", { path: "calc.go" }, "/repo"), /outside task t1's declared files/);
	assert.match(outsideDeclared(task, "write", { path: "./docs/../calc.go" }, "/repo"), /calc\.go is outside/);
	assert.match(outsideDeclared(task, "write", { path: "pkg/sub/x.go" }, "/repo"), /outside/);
	assert.equal(outsideDeclared(task, "bash", { command: "touch calc.go" }, "/repo"), "", "bash is left to guard and the commit step");
	assert.equal(outsideDeclared(null, "edit", { path: "calc.go" }, "/repo"), "", "no task, no scope");
	assert.equal(outsideDeclared({ ...task, status: "blocked" }, "edit", { path: "calc.go" }, "/repo"), "");
	assert.equal(outsideDeclared({ ...task, declared_files: [] }, "edit", { path: "calc.go" }, "/repo"), "");
	assert.equal(declaredMatch("a/**", "a/b/c"), true);
	assert.equal(declaredMatch("*.go", "x.go"), true);
	assert.equal(declaredMatch("*.go", "d/x.go"), false);
});

function cfg(over: Partial<Config> = {}): Config {
	return { bin: "/x/amq-squad", amq: "/x/amq", me: "pi-lead", root: "/r", session: "s", hookTimeoutMs: 15000, holdTimeoutMs: 5000, models: [], lead: false, ...over };
}

test("the lead's reset tool", () => {
	assert.deepEqual(toolArgv.taskReset(cfg(), { id: "t1" }), ["task", "reset", "t1", "--me", "pi-lead"]);
	assert.throws(() => toolArgv.taskReset(cfg(), { id: "--all" }));
	assert.ok(registerTools(new FakePi() as any, cfg({ lead: true })).includes("amq_task_reset"));
	assert.ok(!registerTools(new FakePi() as any, cfg()).includes("amq_task_reset"));
});

test("every seat has the typed block tool", () => {
	assert.ok(registerTools(new FakePi() as any, cfg()).includes("amq_task_block"));
	assert.deepEqual(toolArgv.taskBlock(cfg(), { id: "t1", reason: "widen_requested", paths: ["calc.go"] }), ["task", "block", "t1", "--me", "pi-lead", "--reason", "widen_requested", "--path", "calc.go"]);
});
