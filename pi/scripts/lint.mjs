#!/usr/bin/env node
// Lint for @amq-squad/pi without a compiler download (brief 4-1: no network
// beyond the S3 pins). Checks:
//   1. every .ts file uses erasable syntax only (node:module stripTypeScriptTypes),
//      so pi's jiti loader and Node's type stripping read the same code;
//   2. relative imports name their .ts file; erased extension source never
//      names the pi package scope, and import()/require() calls are one
//      relative .ts string literal;
//   3. package.json declares existing extensions, pins pi as a dev dependency
//      at the version the lockfile resolved, and lists pi only as a peer;
//   4. no console.log in extensions/.
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const problems = [];
const files = [];
for (const dir of ["extensions", "test"]) walk(join(root, dir));

function walk(d) {
	for (const e of readdirSync(d, { withFileTypes: true })) {
		const p = join(d, e.name);
		if (e.isDirectory()) walk(p);
		else if (e.name.endsWith(".ts")) files.push(p);
	}
}

for (const f of files) {
	const rel = relative(root, f);
	const src = readFileSync(f, "utf8");
	let runtimeSrc = src;
	try {
		runtimeSrc = stripTypeScriptTypes(src, { mode: "strip" });
	} catch (err) {
		problems.push(`${rel}: not erasable TypeScript: ${err.message.split("\n")[0]}`);
	}
	for (const m of src.matchAll(/^\s*import\s+(?:type\s+)?[^;]*?from\s+"([^"]+)"/gm)) {
		const [, spec] = m;
		if (spec.startsWith(".") && !spec.endsWith(".ts")) problems.push(`${rel}: relative import ${spec} must name its .ts file`);
	}
	if (rel.startsWith("extensions/")) {
		// Type-only declarations disappear under the same erasure check above.
		// Keep comments: naming the scope anywhere in runtime source is refused
		// by construction before the call-shape check can add a second diagnostic.
		if (runtimeSrc.includes("@earendil-works/")) {
			problems.push(`${rel}: erased extension source contains @earendil-works/ (name the package as pi-tui in comments; pi supplies it at run time)`);
			continue;
		}
		const trivia = String.raw`(?:\s|\/\*[\s\S]*?\*\/|\/\/[^\n]*(?:\n|$))*`;
		const callHead = new RegExp(String.raw`(?<![\p{ID_Continue}$])(?:import|require)${trivia}\(`, "gu");
		const relativeOnly = new RegExp(String.raw`^${trivia}(?:"(?:\.\/|\.\.\/)(?:[^"\\\r\n]|\\.)*\.ts"|'(?:\.\/|\.\.\/)(?:[^'\\\r\n]|\\.)*\.ts')${trivia}\)`);
		for (const m of runtimeSrc.matchAll(callHead)) {
			const call = m[0].startsWith("require") ? "require()" : "import()";
			if (!relativeOnly.test(runtimeSrc.slice((m.index ?? 0) + m[0].length))) {
				problems.push(`${rel}: ${call} argument must be one relative ./ or ../ .ts string literal`);
				break;
			}
		}
	}
	if (rel.startsWith("extensions") && /console\.log\(/.test(src)) problems.push(`${rel}: console.log in extension code`);
}

const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
const lock = JSON.parse(readFileSync(join(root, "package-lock.json"), "utf8"));
for (const ext of pkg.pi?.extensions ?? []) if (!existsSync(join(root, ext))) problems.push(`package.json: extension ${ext} does not exist`);
const pin = pkg.devDependencies?.["@earendil-works/pi-coding-agent"];
const locked = lock.packages?.["node_modules/@earendil-works/pi-coding-agent"]?.version;
if (!pin || pin !== locked) problems.push(`package.json pins pi ${pin}, the lockfile resolved ${locked}`);
if (pkg.dependencies?.["@earendil-works/pi-coding-agent"]) problems.push("package.json: pi must not be a runtime dependency (pi supplies it)");
if (lock.packages?.[""]?.devDependencies?.["@earendil-works/pi-coding-agent"] !== pin) problems.push("package-lock.json root does not match package.json");

if (problems.length) {
	for (const p of problems) console.error(p);
	process.exit(1);
}
console.log(`lint ok: ${files.length} files, pi ${pin}`);
