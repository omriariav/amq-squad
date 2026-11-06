import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

test("lint rejects dynamic import() and require() of pi packages under extensions", () => {
	const copy = fs.mkdtempSync(path.join(os.tmpdir(), "amq-squad-pi-lint-"));
	fs.mkdirSync(path.join(copy, "scripts"));
	fs.mkdirSync(path.join(copy, "test"));
	fs.cpSync(path.join(root, "extensions"), path.join(copy, "extensions"), { recursive: true });
	for (const name of ["package.json", "package-lock.json"]) fs.copyFileSync(path.join(root, name), path.join(copy, name));
	fs.copyFileSync(path.join(root, "scripts", "lint.mjs"), path.join(copy, "scripts", "lint.mjs"));
	fs.writeFileSync(
		path.join(copy, "extensions", "lint-fixture.ts"),
		'void import("@earendil-works/pi-tui");\nrequire("@earendil-works/pi-coding-agent");\n',
	);
	const result = spawnSync(process.execPath, ["scripts/lint.mjs"], {
		cwd: copy,
		encoding: "utf8",
		env: { PATH: process.env.PATH ?? "", HOME: path.join(copy, "home") },
	});
	assert.equal(result.status, 1, result.stdout + result.stderr);
	assert.match(result.stderr, /extensions\/lint-fixture\.ts: import @earendil-works\/pi-tui as a type only \(pi supplies it at run time\)/);
	assert.match(result.stderr, /extensions\/lint-fixture\.ts: import @earendil-works\/pi-coding-agent as a type only \(pi supplies it at run time\)/);
});
