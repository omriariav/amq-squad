import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

const source = process.env.T86_SCREEN_SOURCE;
if (!source) throw new Error("T86_SCREEN_SOURCE is required");
const { renderScreen } = await import(pathToFileURL(path.join(source, "pi/extensions/lib/screen.ts")));
const fixture = JSON.parse(fs.readFileSync(new URL("fixture-v3.1.2-widget-order.json", import.meta.url), "utf8"));

test("the captured-height widget hides every reconstructed wait row below closed tasks", () => {
  for (const row of fixture.cases) {
    const tasks = fixture.status.tasks.map((task) => {
      if (task.id === "t71") return {...task, task_state:"in_progress", lease_holder:"reviewer"};
      if (task.id === row.target) return {...task, task_state:"pending", wait_reason:"file_lock", wait_holders:["t71"], lease_holder:""};
      return task;
    });
    const lines = renderScreen({...fixture.status, tasks});
    const start = lines.indexOf("tasks (76)");
    const end = lines.indexOf("train", start);
    const taskRows = lines.slice(start + 1, end);
    const visible = taskRows.slice(0, fixture.captured_task_rows);
    assert.deepEqual(visible.map((line) => line.match(/^  (t\d+) /)?.[1]), ["t7", "t24", "t9"]);
    assert.equal(taskRows.findIndex((line) => line.startsWith(`  ${row.target} `)) + 1, row.expected_row);
    assert.equal(taskRows[row.expected_row - 1], `  ${row.target} waiting: t71 hold its files`);
    assert.ok(!visible.some((line) => line.startsWith(`  ${row.target} `)));
  }
});
