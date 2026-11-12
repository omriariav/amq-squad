import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

const source = process.env.T86_SCREEN_SOURCE;
const fixtureName = process.env.T86_SCREEN_FIXTURE;
const expected = process.env.T86_SCREEN_EXPECTED;
if (!source || !fixtureName || !expected) throw new Error("T86_SCREEN_SOURCE, T86_SCREEN_FIXTURE, and T86_SCREEN_EXPECTED are required");

const { renderScreen } = await import(pathToFileURL(path.join(source, "pi/extensions/lib/screen.ts")));
const fixture = JSON.parse(fs.readFileSync(new URL(fixtureName, import.meta.url), "utf8"));

test(`snapshot-backed file-lock words from ${fixtureName}`, () => {
  for (const row of fixture.cases) {
    const status = {
      root: "/snapshot-0d2",
      session: "v31",
      lead_handle: "cto",
      seats: [],
      tasks: [row.holder, row.target],
      gates: [],
      attention: [],
      decisions: [],
      requests: [],
      warnings: [],
      train: { runs: [], alerts: [] }
    };
    const line = renderScreen(status).find((candidate) => candidate.startsWith(`  ${row.target.id} `));
    assert.equal(line, `  ${row.target.id} ${expected}`);
    assert.ok(!line.includes(`${row.target.id} pending`));
  }
});
