import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

test("lint permits type-position import() but rejects runtime dynamic import() and require()", () => {
	const copy = fs.mkdtempSync(path.join(os.tmpdir(), "amq-squad-pi-lint-"));
	fs.mkdirSync(path.join(copy, "scripts"));
	fs.mkdirSync(path.join(copy, "test"));
	fs.cpSync(path.join(root, "extensions"), path.join(copy, "extensions"), { recursive: true });
	for (const name of ["package.json", "package-lock.json"]) fs.copyFileSync(path.join(root, name), path.join(copy, name));
	fs.copyFileSync(path.join(root, "scripts", "lint.mjs"), path.join(copy, "scripts", "lint.mjs"));
	const fixture = path.join(copy, "extensions", "lint-fixture.ts");
	fs.writeFileSync(
		fixture,
		'type API = import("@earendil-works/pi-coding-agent").ExtensionAPI;\ntype TUI = typeof import("@earendil-works/pi-tui");\n',
	);
	const env = { PATH: process.env.PATH ?? "", HOME: path.join(copy, "home") };
	const types = spawnSync(process.execPath, ["scripts/lint.mjs"], {
		cwd: copy,
		encoding: "utf8",
		env,
	});
	assert.equal(types.status, 0, types.stdout + types.stderr);

	fs.writeFileSync(
		fixture,
		'await import("@earendil-works/pi-tui");\nimport("@earendil-works/pi-tui");\nrequire("@earendil-works/pi-coding-agent");\n',
	);
	const result = spawnSync(process.execPath, ["scripts/lint.mjs"], { cwd: copy, encoding: "utf8", env });
	assert.equal(result.status, 1, result.stdout + result.stderr);
	assert.match(result.stderr, /extensions\/lint-fixture\.ts: load @earendil-works\/pi-tui only in a type position \(pi supplies it at run time\)/);
	assert.match(result.stderr, /extensions\/lint-fixture\.ts: load @earendil-works\/pi-coding-agent only in a type position \(pi supplies it at run time\)/);
});
