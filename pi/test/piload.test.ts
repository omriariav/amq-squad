// The extension loads the way pi 1.3.0 loads it (the 3.1.2 hotfix). The 3.1.0
// and 3.1.1 lead exited at once: firstturn.ts declared `type HostFacts` and
// `class HostFacts`, which Node's type stripping (this suite's loader) reads
// as one class, while pi's loader, jiti's Babel TypeScript transform, refuses
// as "Identifier 'HostFacts' has already been declared". These rows load the
// package through pi's own code, so the suite fails on what pi fails on:
//   - every .ts under extensions/ through jiti's static transform with
//     tryNative off, the options pi's bundled Node runtime passes;
//   - extensions/amq-squad.ts through pi's loadExtensions (the module the
//     lead's pi imports), from the pinned devDependency;
//   - pi's bundled CLI (dist/bundle/cli.js, the runtime's entry) started
//     with -e amq-squad.ts, offline, under a scratch HOME and agent dir with
//     no key: it must get past the extension load and stop at "No API key
//     found", before any model call;
//   - the same through a staged runtime's own node (build/pi/stage from
//     scripts/build-pi-runtime.sh build, or AMQ_SQUAD_PI_RUNTIME), skipped
//     with its reason when none is staged (CI stages none).
// Without AM_ME and AM_ROOT the package is inactive: the factory runs after
// every module it imports is evaluated and registers only a session_start
// note, so no row reaches a squad.
import { after, test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { tmpdir as fakeTmpdir } from "./fakepi.ts";

const PI_VERSION = "1.3.0"; // scripts/build-pi-runtime.sh pi_version
// fileURLToPath, not URL.pathname: a checkout path with a space stays a path
const here = path.dirname(fileURLToPath(import.meta.url));

// fakepi's tmpdir registers no cleanup; this file removes its own dirs
const scratch: string[] = [];
function tmpdir(): string {
	const d = fakeTmpdir();
	scratch.push(d);
	return d;
}
after(() => {
	for (const d of scratch) fs.rmSync(d, { recursive: true, force: true });
});
const pkgRoot = path.join(here, "..");
const ext = path.join(pkgRoot, "extensions", "amq-squad.ts");
const piDir = path.join(pkgRoot, "node_modules", "@earendil-works", "pi-coding-agent");
const piCli = "node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js";
// read before the loader row clears AMQ_SQUAD_* from this process
const stagedRuntime = process.env.AMQ_SQUAD_PI_RUNTIME || path.join(pkgRoot, "..", "build", "pi", "stage");

// pinnedPi fails (never skips) when the devDependency is absent: npm ci
// installs it before npm test, in CI too.
function pinnedPi(): void {
	const pj = path.join(piDir, "package.json");
	assert.ok(fs.existsSync(pj), `pi ${PI_VERSION} is not installed at ${piDir}: run npm ci in pi/`);
	assert.equal(JSON.parse(fs.readFileSync(pj, "utf8")).version, PI_VERSION);
}

function walk(d: string, out: string[] = []): string[] {
	for (const e of fs.readdirSync(d, { withFileTypes: true })) {
		const p = path.join(d, e.name);
		if (e.isDirectory()) walk(p, out);
		else if (e.name.endsWith(".ts")) out.push(p);
	}
	return out;
}

// startPi runs pi's bundled CLI with the extension, offline, in a scratch
// HOME, agent dir and cwd, with only PATH from this environment (no
// provider key, no AM_ME or AM_ROOT).
function startPi(node: string, cli: string, preload?: string): { code: number | null; signal: NodeJS.Signals | null; out: string } {
	const home = tmpdir();
	const agent = path.join(home, "agent");
	const cwd = path.join(home, "cwd");
	fs.mkdirSync(agent);
	fs.mkdirSync(cwd);
	const args = [...(preload ? ["--require", preload] : []), cli, "--offline", "--no-session", "-p", "-e", ext, "hi"];
	const r = spawnSync(node, args, {
		cwd,
		// JITI_FS_CACHE=false and NODE_DISABLE_COMPILE_CACHE=1 as the launcher
		// sets them (internal/piruntime): the bundle calls Node's
		// enableCompileCache() at its top, and jiti caches transforms; neither
		// writes outside the scratch HOME. (The in-process rows import the
		// unbundled loader, which never calls enableCompileCache.)
		env: { PATH: process.env.PATH ?? "/usr/bin:/bin", HOME: home, PI_CODING_AGENT_DIR: agent, PI_OFFLINE: "1", JITI_FS_CACHE: "false", NODE_DISABLE_COMPILE_CACHE: "1" },
		encoding: "utf8",
		timeout: 60_000,
	});
	return { code: r.status, signal: r.signal, out: `${r.stdout ?? ""}${r.stderr ?? ""}${r.error ? String(r.error) : ""}` };
}

// NO_KEY_EXIT is pi 1.3.0's exit status at "No API key found" in print
// mode: AgentSession.prompt() checks the provider's auth before it starts
// a run (core/agent-session.js), so the prompt fails before any request
// and pi exits 1, recorded by the run on 2026-11-05. A timeout ends with a
// signal and no status.
const NO_KEY_EXIT = 1;

function assertLoaded(r: { code: number | null; signal: NodeJS.Signals | null; out: string }): void {
	assert.doesNotMatch(r.out, /Failed to load extension/, r.out);
	// past the extension load, stopped at the model step: no key, no call
	assert.match(r.out, /No API key found/, r.out);
	assert.equal(r.signal, null, `pi ended on ${r.signal}: ${r.out}`);
	assert.equal(r.code, NO_KEY_EXIT, r.out);
}

test("pi's transform (jiti static, tryNative off) takes every extensions/ .ts", async () => {
	pinnedPi();
	const jitiDir = path.join(pkgRoot, "node_modules", "jiti");
	const { createJiti } = (await import(pathToFileURL(path.join(jitiDir, "lib", "jiti-static.mjs")).href)) as {
		createJiti: (id: string, o: object) => { transform(o: { source: string; filename: string; ts: boolean }): string };
	};
	const jiti = createJiti(import.meta.url, { moduleCache: false, tryNative: false, fsCache: false });
	const files = walk(path.join(pkgRoot, "extensions"));
	assert.ok(files.length > 1);
	const refused = files
		.map((f) => ({ f: path.relative(pkgRoot, f), out: jiti.transform({ source: fs.readFileSync(f, "utf8"), filename: f, ts: true }) }))
		.filter((x) => x.out.includes("__JITI_ERROR__"))
		.map((x) => `${x.f}: ${x.out.slice(0, 300)}`);
	assert.deepEqual(refused, []);
});

test("pi's loadExtensions loads extensions/amq-squad.ts", async () => {
	pinnedPi();
	// pi's agent-dir code reads a scratch HOME and agent dir, jiti writes no
	// transform cache (as the launcher sets it), and the package is inactive;
	// every variable is put back after
	const home = tmpdir();
	const saved = new Map<string, string | undefined>();
	const set = (k: string, v: string | undefined) => {
		if (!saved.has(k)) saved.set(k, process.env[k]);
		if (v === undefined) delete process.env[k];
		else process.env[k] = v;
	};
	set("HOME", home);
	set("PI_CODING_AGENT_DIR", path.join(home, "agent"));
	set("JITI_FS_CACHE", "false");
	for (const k of Object.keys(process.env)) if (k === "AM_ME" || k === "AM_ROOT" || k.startsWith("AMQ_SQUAD_")) set(k, undefined);
	try {
		const loader = (await import(pathToFileURL(path.join(piDir, "dist", "core", "extensions", "loader.js")).href)) as {
			loadExtensions(paths: string[], cwd: string): Promise<{ extensions: unknown[]; errors: { path: string; error: string }[] }>;
		};
		const r = await loader.loadExtensions([ext], tmpdir());
		assert.deepEqual(r.errors, []);
		assert.equal(r.extensions.length, 1);
	} finally {
		for (const [k, v] of saved) {
			if (v === undefined) delete process.env[k];
			else process.env[k] = v;
		}
	}
});

test("pi's bundled CLI starts with -e amq-squad.ts (offline, stops at no key)", () => {
	pinnedPi();
	assertLoaded(startPi(process.execPath, path.join(pkgRoot, piCli)));
});

test("a staged pi runtime's own node starts with -e amq-squad.ts", (t) => {
	const dir = stagedRuntime;
	const node = path.join(dir, "node", "bin", "node");
	const cli = path.join(dir, "pi", piCli);
	if (!fs.existsSync(node) || !fs.existsSync(cli)) {
		t.skip(`no staged pi runtime at ${dir}: run scripts/build-pi-runtime.sh build, or set AMQ_SQUAD_PI_RUNTIME to an unpacked runtime`);
		return;
	}
	// the preload is a no-op without the launcher's manifest variables
	assertLoaded(startPi(node, cli, path.join(dir, "pi", "preload", "runtime-check.cjs")));
});
