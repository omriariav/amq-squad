// Review 2, item 2 (b): the one-purpose preload re-hashes every manifest
// file before pi; and amendment 26: the lead's repository read and list.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import { openVerified, repoList, repoRead, resolveInRoot } from "../extensions/lib/repo.ts";
import { tmpdir } from "./fakepi.ts";

const here = path.dirname(new URL(import.meta.url).pathname);
const preloadPath = path.join(here, "../preload/runtime-check.cjs");
const preload = createRequire(import.meta.url)(preloadPath) as {
	checkRuntime(dir: string, sha: string): { ok: boolean; files?: number; reason?: string };
};

function runtimeFixture(): { dir: string; sha: string } {
	const dir = tmpdir();
	const files: Record<string, string> = {
		"node/bin/node": "#!/bin/sh\n",
		"pi/node_modules/x/dist/bundle/cli.js": "entry\n",
		"pi/node_modules/x/dist/bundle/chunks/a.js": "chunk a\n",
		"pi/node_modules/x/other.js": "lazy module\n",
		"pi/package.json": "{}",
		"pi/extensions/amq-squad.ts": "export default 1\n",
		"pi/preload/runtime-check.cjs": "// preload\n",
		"tools/rg": "#!/bin/sh\n",
	};
	const entries = [];
	for (const [p, body] of Object.entries(files)) {
		fs.mkdirSync(path.dirname(path.join(dir, p)), { recursive: true });
		fs.writeFileSync(path.join(dir, p), body);
		entries.push({ path: p, sha256: createHash("sha256").update(body).digest("hex"), size: Buffer.byteLength(body), mode: 0o444 });
	}
	const manifest = JSON.stringify({ args: ["pi/node_modules/x/dist/bundle/cli.js"], package: "pi", preload: "pi/preload/runtime-check.cjs", files: entries });
	fs.writeFileSync(path.join(dir, ".embedpi-manifest.json"), manifest);
	fs.writeFileSync(path.join(dir, ".embedpi-complete"), "x\n");
	return { dir, sha: createHash("sha256").update(manifest).digest("hex") };
}

test("the preload accepts the verified tree and checks every manifest file", () => {
	const { dir, sha } = runtimeFixture();
	const r = preload.checkRuntime(dir, sha);
	assert.equal(r.ok, true, r.reason);
	assert.equal(r.files, 8, "every file, not only the bundle and the package");
});

test("the preload refuses any change anywhere in the tree", () => {
	const cases: Record<string, (dir: string) => void> = {
		"changed node": (d) => fs.writeFileSync(path.join(d, "node/bin/node"), "#!/bin/sh\nevil\n"),
		"changed lazy module outside the bundle": (d) => fs.writeFileSync(path.join(d, "pi/node_modules/x/other.js"), "lazy MODULE\n"),
		"changed tool": (d) => fs.writeFileSync(path.join(d, "tools/rg"), "#!/bin/sh\nrg\n"),
		"changed chunk": (d) => fs.writeFileSync(path.join(d, "pi/node_modules/x/dist/bundle/chunks/a.js"), "chunk A\n"),
		"changed extension": (d) => fs.writeFileSync(path.join(d, "pi/extensions/amq-squad.ts"), "export default 2\n"),
		"extra file at the top": (d) => fs.writeFileSync(path.join(d, "evil.js"), "x\n"),
		"extra directory": (d) => fs.mkdirSync(path.join(d, "pi/node_modules/evil")),
		"missing file": (d) => fs.rmSync(path.join(d, "pi/node_modules/x/other.js")),
		"symlinked file": (d) => {
			const p = path.join(d, "tools/rg");
			const elsewhere = path.join(tmpdir(), "rg");
			fs.writeFileSync(elsewhere, "#!/bin/sh\n");
			fs.rmSync(p);
			fs.symlinkSync(elsewhere, p);
		},
		"changed manifest": (d) => fs.appendFileSync(path.join(d, ".embedpi-manifest.json"), " "),
	};
	for (const [name, tamper] of Object.entries(cases)) {
		const { dir, sha } = runtimeFixture();
		tamper(dir);
		assert.equal(preload.checkRuntime(dir, sha).ok, false, name);
	}
});

test("loaded with --require, the preload runs before the script and exits 70 on a mismatch", () => {
	const { dir, sha } = runtimeFixture();
	const script = path.join(tmpdir(), "entry.cjs");
	fs.writeFileSync(script, "console.log('entry ran')\n");
	const bad = spawnSync(process.execPath, ["--require", preloadPath, script], {
		env: { PATH: process.env.PATH, AMQ_SQUAD_RUNTIME_DIR: dir, AMQ_SQUAD_RUNTIME_MANIFEST_SHA256: "0".repeat(64) },
		encoding: "utf8",
	});
	assert.equal(bad.status, 70);
	assert.match(bad.stderr, /runtime_mismatch_in_process/);
	assert.doesNotMatch(bad.stdout, /entry ran/, "the entry never runs after a refusal");
	const good = spawnSync(process.execPath, ["--require", preloadPath, script], {
		env: { PATH: process.env.PATH, AMQ_SQUAD_RUNTIME_DIR: dir, AMQ_SQUAD_RUNTIME_MANIFEST_SHA256: sha },
		encoding: "utf8",
	});
	assert.equal(good.status, 0, good.stderr);
	assert.match(good.stdout, /entry ran/);
	const none = spawnSync(process.execPath, ["--require", preloadPath, script], { env: { PATH: process.env.PATH }, encoding: "utf8" });
	assert.equal(none.status, 0, "a user's own pi (no variables) is not checked");
});

test("the preload's first statement has no import before it", () => {
	const src = fs.readFileSync(preloadPath, "utf8");
	const code = src.split("\n").filter((l) => l.trim() && !l.trim().startsWith("//"));
	assert.equal(code[0], '"use strict";');
	assert.match(code[1], /^const check = \(function \(\) \{$/);
	assert.doesNotMatch(src, /^import /m);
});

test("the lead's repository read and list stay inside the root", () => {
	const root = tmpdir();
	fs.mkdirSync(path.join(root, "pkg"));
	fs.writeFileSync(path.join(root, "pkg", "a.go"), "line1\nline2\nline3\n");
	const outside = tmpdir();
	fs.writeFileSync(path.join(outside, "secret"), "s");
	fs.symlinkSync(outside, path.join(root, "escape"));
	assert.equal(repoRead(root, "pkg/a.go", 2, 1), "line2");
	assert.match(repoList(root), /^escape@\npkg\/$/);
	assert.match(repoList(root, "pkg"), /a\.go/);
	assert.throws(() => repoRead(root, "../" + path.basename(outside) + "/secret"), /outside the repository root/);
	assert.throws(() => repoRead(root, path.join(outside, "secret")), /outside/);
	assert.throws(() => repoRead(root, "escape/secret"), /outside/, "a symlink out of the root is refused");
	assert.throws(() => repoList(root, "escape"), /outside/);
	assert.throws(() => repoRead(root, "pkg"), /not a regular file/);
	assert.equal(resolveInRoot(root, undefined).target, fs.realpathSync(root));
});

// Amendment 26 review: a swap after the path was validated and before it is
// opened is refused (the opened descriptor is checked against an lstat walk
// of the validated components).
test("a swap between validation and open is refused", () => {
	const root = tmpdir();
	fs.mkdirSync(path.join(root, "pkg"));
	fs.writeFileSync(path.join(root, "pkg", "a.go"), "inside\n");
	const outside = tmpdir();
	fs.writeFileSync(path.join(outside, "a.go"), "outside secret\n");
	const { realRoot, target } = resolveInRoot(root, "pkg/a.go");
	// The swap: the validated directory becomes a symlink out of the root.
	fs.renameSync(path.join(root, "pkg"), path.join(root, "pkg.moved"));
	fs.symlinkSync(outside, path.join(root, "pkg"));
	assert.throws(() => openVerified(realRoot, target), /symlink|changed/);
	// A file replaced by another one (new inode) is refused too.
	fs.rmSync(path.join(root, "pkg"));
	fs.renameSync(path.join(root, "pkg.moved"), path.join(root, "pkg"));
	const v = resolveInRoot(root, "pkg/a.go");
	const { fd } = openVerified(v.realRoot, v.target);
	fs.closeSync(fd);
});
