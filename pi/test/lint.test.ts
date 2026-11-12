import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

test("lint permits erased types and relative .ts calls but gives each refused extension one diagnostic", () => {
	const copy = fs.mkdtempSync(path.join(os.tmpdir(), "amq-squad-pi-lint-"));
	fs.mkdirSync(path.join(copy, "scripts"));
	fs.mkdirSync(path.join(copy, "test"));
	fs.cpSync(path.join(root, "extensions"), path.join(copy, "extensions"), { recursive: true });
	for (const name of ["package.json", "package-lock.json"]) fs.copyFileSync(path.join(root, name), path.join(copy, name));
	fs.copyFileSync(path.join(root, "scripts", "lint.mjs"), path.join(copy, "scripts", "lint.mjs"));
	const fixture = path.join(copy, "extensions", "lint-pass.ts");
	fs.writeFileSync(
		fixture,
		'import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";\ntype API = import("@earendil-works/pi-coding-agent").ExtensionAPI;\ntype TUI = typeof import("@earendil-works/pi-tui");\nawait import("./lib/width.ts");\nreimport(name); myrequire(name);\n',
	);
	const env = { PATH: process.env.PATH ?? "", HOME: path.join(copy, "home") };
	const types = spawnSync(process.execPath, ["scripts/lint.mjs"], {
		cwd: copy,
		encoding: "utf8",
		env,
	});
	assert.equal(types.status, 0, types.stdout + types.stderr);

	fs.rmSync(fixture);
	const fixtures: Record<string, string> = {
		"f01-side-effect.ts": 'import "@earendil-works/pi-tui";\n',
		"f02-re-export.ts": 'export { x } from "@earendil-works/pi-tui";\n',
		"f03-template.ts": 'await import(`@earendil-works/pi-tui`);\n',
		"f04-concat-package.ts": 'import("@earendil-works/" + "pi-tui");\n',
		"f05-variable.ts": "await import /* gap */ (name);\n",
		"f06-create-require.ts": 'createRequire(import.meta.url)("@earendil-works/pi-tui");\n',
		"f07-comment.ts": '// never load @earendil-works/pi-tui here\nexport const x = 1;\n',
		"f08-awaited.ts": 'await import("@earendil-works/pi-tui");\n',
		"f09-bare.ts": 'import("@earendil-works/pi-tui");\n',
		"f10-require.ts": 'require("@earendil-works/pi-coding-agent");\n',
		"f11-relative-concat.ts": 'await import("./" + name);\n',
		// The inline type erases to `import {} from "@earendil-works/..."`, a
		// side-effect import that loads the package at run time, so refusing it
		// is correct rather than over-strict.
		"f12-inline-type.ts": 'import { type X } from "@earendil-works/pi-coding-agent";\n',
		"f13-require-variable.ts": "require(name);\n",
		"f14-bare-import.ts": 'await import("left-pad");\n',
		"f15-builtin-require.ts": 'require("node:fs");\n',
		"f16-relative-js.ts": 'await import("./x.js");\n',
		"f17-not-erasable.ts": "export enum E { A }\n",
	};
	for (const [name, source] of Object.entries(fixtures)) fs.writeFileSync(path.join(copy, "extensions", name), source);
	const result = spawnSync(process.execPath, ["scripts/lint.mjs"], { cwd: copy, encoding: "utf8", env });
	assert.equal(result.status, 1, result.stdout + result.stderr);
	const diagnostics = result.stderr.split("\n").filter(Boolean);
	for (const name of Object.keys(fixtures)) {
		const own = diagnostics.filter((line) => line.startsWith(`extensions/${name}:`));
		assert.equal(own.length, 1, `${name}: ${result.stderr}`);
	}
	for (const name of ["f05-variable.ts", "f11-relative-concat.ts"]) {
		assert.match(diagnostics.find((line) => line.startsWith(`extensions/${name}:`)) ?? "", /import\(\) argument must be one relative/);
	}
	for (const name of ["f13-require-variable.ts", "f15-builtin-require.ts"]) {
		assert.match(diagnostics.find((line) => line.startsWith(`extensions/${name}:`)) ?? "", /require\(\) argument must be one relative/);
	}
	for (const name of ["f14-bare-import.ts", "f16-relative-js.ts"]) {
		assert.match(diagnostics.find((line) => line.startsWith(`extensions/${name}:`)) ?? "", /import\(\) argument must be one relative/);
	}
	assert.match(diagnostics.find((line) => line.startsWith("extensions/f17-not-erasable.ts:")) ?? "", /not erasable TypeScript/);
	assert.equal(diagnostics.filter((line) => line.startsWith("extensions/f")).length, Object.keys(fixtures).length, result.stderr);
});
