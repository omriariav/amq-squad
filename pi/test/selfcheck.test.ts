// Review item 2 (b) and amendment 26 at unit level: the in-process runtime
// re-hash, and the team lead's repository-scoped read and list.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import { checkRuntime } from "../extensions/lib/selfcheck.ts";
import { repoList, repoRead, resolveInRoot } from "../extensions/lib/repo.ts";
import { tmpdir } from "./fakepi.ts";

function runtimeFixture(): { dir: string; sha: string } {
	const dir = tmpdir();
	const files: Record<string, string> = {
		"node/bin/node": "#!/bin/sh\n",
		"pi/node_modules/x/dist/bundle/cli.js": "entry\n",
		"pi/node_modules/x/dist/bundle/chunks/a.js": "chunk a\n",
		"pi/node_modules/x/other.js": "not checked in process\n",
		"pi/package.json": "{}",
		"pi/extensions/amq-squad.ts": "export default 1\n",
	};
	const entries = [];
	for (const [p, body] of Object.entries(files)) {
		fs.mkdirSync(path.dirname(path.join(dir, p)), { recursive: true });
		fs.writeFileSync(path.join(dir, p), body);
		entries.push({ path: p, sha256: createHash("sha256").update(body).digest("hex"), size: Buffer.byteLength(body), mode: 0o444 });
	}
	const manifest = JSON.stringify({ args: ["pi/node_modules/x/dist/bundle/cli.js"], package: "pi", files: entries });
	fs.writeFileSync(path.join(dir, ".embedpi-manifest.json"), manifest);
	return { dir, sha: createHash("sha256").update(manifest).digest("hex") };
}

test("the in-process re-hash accepts the verified tree", () => {
	const { dir, sha } = runtimeFixture();
	const r = checkRuntime(dir, sha);
	assert.equal(r.ok, true);
	if (r.ok) assert.equal(r.files, 4, "the bundle (entry, chunk) and the package (package.json, extension)");
});

test("the in-process re-hash refuses a changed chunk, an extra file, a missing file, a changed manifest", () => {
	const cases: Record<string, (dir: string) => void> = {
		"changed chunk": (d) => fs.writeFileSync(path.join(d, "pi/node_modules/x/dist/bundle/chunks/a.js"), "chunk A\n"),
		"changed entry": (d) => fs.writeFileSync(path.join(d, "pi/node_modules/x/dist/bundle/cli.js"), "evil!\n"),
		"changed extension": (d) => fs.writeFileSync(path.join(d, "pi/extensions/amq-squad.ts"), "export default 2\n"),
		"extra chunk": (d) => fs.writeFileSync(path.join(d, "pi/node_modules/x/dist/bundle/chunks/b.js"), "b\n"),
		"extra extension": (d) => fs.writeFileSync(path.join(d, "pi/extensions/evil.ts"), "x\n"),
		"missing chunk": (d) => fs.rmSync(path.join(d, "pi/node_modules/x/dist/bundle/chunks/a.js")),
		"symlinked chunk": (d) => {
			const p = path.join(d, "pi/node_modules/x/dist/bundle/chunks/a.js");
			const elsewhere = path.join(tmpdir(), "a.js");
			fs.writeFileSync(elsewhere, "chunk a\n");
			fs.rmSync(p);
			fs.symlinkSync(elsewhere, p);
		},
		"changed manifest": (d) => fs.appendFileSync(path.join(d, ".embedpi-manifest.json"), " "),
	};
	for (const [name, tamper] of Object.entries(cases)) {
		const { dir, sha } = runtimeFixture();
		tamper(dir);
		const r = checkRuntime(dir, sha);
		assert.equal(r.ok, false, name);
	}
	// A file outside the in-process set (the rest of node_modules) is the
	// launcher's full re-hash's job, not this one's.
	const { dir, sha } = runtimeFixture();
	fs.writeFileSync(path.join(dir, "pi/node_modules/x/other.js"), "changed\n");
	assert.equal(checkRuntime(dir, sha).ok, true);
});

test("a mismatch exits 70 with the named reason", () => {
	const { dir } = runtimeFixture();
	const here = path.dirname(new URL(import.meta.url).pathname);
	const script = `import { enforceRuntime } from ${JSON.stringify(path.join(here, "../extensions/lib/selfcheck.ts"))};\nenforceRuntime(process.env, () => {});\nconsole.log("not reached");`;
	const file = path.join(tmpdir(), "run.mts");
	fs.writeFileSync(file, script);
	const r = spawnSync(process.execPath, [file], { env: { ...process.env, AMQ_SQUAD_RUNTIME_DIR: dir, AMQ_SQUAD_RUNTIME_MANIFEST_SHA256: "0".repeat(64) }, encoding: "utf8" });
	assert.equal(r.status, 70);
	assert.match(r.stderr, /runtime_mismatch_in_process/);
	assert.doesNotMatch(r.stdout, /not reached/);
	const none = spawnSync(process.execPath, [file], { env: { ...process.env, AMQ_SQUAD_RUNTIME_DIR: "", AMQ_SQUAD_RUNTIME_MANIFEST_SHA256: "" }, encoding: "utf8" });
	assert.equal(none.status, 0, "a user's own pi (no variables) is not checked");
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
	assert.equal(resolveInRoot(root, undefined), fs.realpathSync(root));
});
