#!/usr/bin/env node
// Lint for @amq-squad/pi without a compiler download (brief 4-1: no network
// beyond the S3 pins). Checks:
//   1. every .ts file uses erasable syntax only (node:module stripTypeScriptTypes),
//      so pi's jiti loader and Node's type stripping read the same code;
//   2. relative imports name their .ts file, and pi packages are imported as
//      types only, except the host-provided "typebox";
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
	try {
		stripTypeScriptTypes(src, { mode: "strip" });
	} catch (err) {
		problems.push(`${rel}: not erasable TypeScript: ${err.message.split("\n")[0]}`);
	}
	for (const m of src.matchAll(/^\s*import\s+(type\s+)?[^;]*?from\s+"([^"]+)"/gm)) {
		const [, isType, spec] = m;
		if (spec.startsWith(".") && !spec.endsWith(".ts")) problems.push(`${rel}: relative import ${spec} must name its .ts file`);
		if (spec.startsWith("@earendil-works/") && !isType) problems.push(`${rel}: import ${spec} as a type only (pi supplies it at run time)`);
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
