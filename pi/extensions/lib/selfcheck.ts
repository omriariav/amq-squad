// In-process runtime re-hash (review item 2 (b)). The launcher verifies the
// whole runtime through descriptors and checks the launch set's identity
// right before exec, but node then opens the pi entry, its chunks and this
// package by path. This is the package's first code: before it registers
// anything it re-hashes the pi bundle directory (the entry and every chunk)
// and the package's own files against the runtime manifest, whose SHA-256
// the launcher passes in the forced environment (the embedded digest, the
// trust root). A mismatch, a missing file or an extra file stops the process
// with exit 70 and the named reason runtime_mismatch_in_process. What stays
// open is the window between node's own reads and the end of this check,
// measured and accepted as the same-UID limit (4-1-execution.md).
import { createHash } from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";

export const EXIT_RUNTIME_MISMATCH = 70;
export const RUNTIME_MISMATCH_REASON = "runtime_mismatch_in_process";

type Entry = { path: string; sha256: string; size: number; mode: number };
type Manifest = { args?: string[]; package?: string; files?: Entry[] };

export type CheckResult = { ok: true; files: number; bytes: number; ms: number } | { ok: false; reason: string };

// checkRuntime is pure (no exit) for tests; enforceRuntime acts on it.
export function checkRuntime(dir: string, wantSha: string): CheckResult {
	const t0 = Date.now();
	let raw: Buffer;
	try {
		raw = fs.readFileSync(path.join(dir, ".embedpi-manifest.json"));
	} catch (err) {
		return { ok: false, reason: `runtime manifest unreadable: ${String(err)}` };
	}
	if (createHash("sha256").update(raw).digest("hex") !== wantSha) {
		return { ok: false, reason: "runtime manifest differs from the embedded one" };
	}
	let m: Manifest;
	try {
		m = JSON.parse(raw.toString("utf8"));
	} catch {
		return { ok: false, reason: "runtime manifest is not JSON" };
	}
	const bundle = m.args?.[0] ? path.posix.dirname(m.args[0]) + "/" : "";
	const pkg = m.package ? m.package + "/" : "";
	const inSet = (p: string) => (bundle !== "" && p.startsWith(bundle)) || (pkg !== "" && p.startsWith(pkg) && !p.startsWith(pkg + "node_modules/"));
	const want = new Map<string, Entry>();
	for (const f of m.files ?? []) if (inSet(f.path)) want.set(f.path, f);
	if (bundle === "" || want.size === 0) return { ok: false, reason: "runtime manifest names no pi bundle" };
	// Enumerate the checked directories: nothing extra, nothing a symlink.
	const seen = new Set<string>();
	const walk = (rel: string, skipNodeModules: boolean): string => {
		let ents: fs.Dirent[];
		try {
			ents = fs.readdirSync(path.join(dir, rel), { withFileTypes: true });
		} catch (err) {
			return `cannot list ${rel}: ${String(err)}`;
		}
		for (const d of ents) {
			const p = rel + d.name;
			if (d.isDirectory()) {
				if (skipNodeModules && rel === pkg && d.name === "node_modules") continue;
				const e = walk(p + "/", false);
				if (e) return e;
			} else if (d.isFile()) {
				if (!want.has(p)) return `unexpected runtime entry ${p}`;
				seen.add(p);
			} else {
				return `runtime entry ${p} is not a regular file or directory`;
			}
		}
		return "";
	};
	for (const [root, skip] of [[bundle, false], [pkg, true]] as [string, boolean][]) {
		if (!root) continue;
		const e = walk(root, skip);
		if (e) return { ok: false, reason: e };
	}
	let bytes = 0;
	for (const [p, f] of want) {
		if (!seen.has(p)) return { ok: false, reason: `runtime file missing: ${p}` };
		let b: Buffer;
		try {
			b = fs.readFileSync(path.join(dir, p));
		} catch (err) {
			return { ok: false, reason: `runtime file unreadable: ${p}: ${String(err)}` };
		}
		if (b.length !== f.size || createHash("sha256").update(b).digest("hex") !== f.sha256) {
			return { ok: false, reason: `SHA-256 mismatch: ${p}` };
		}
		bytes += b.length;
	}
	return { ok: true, files: want.size, bytes, ms: Date.now() - t0 };
}

// enforceRuntime runs the check when the launcher asked for it (both
// variables set; a user's own pi has neither and is not checked) and stops
// the process on a mismatch. record writes one evidence line first.
export function enforceRuntime(env: NodeJS.ProcessEnv, record: (reason: string) => void): CheckResult | null {
	const dir = env.AMQ_SQUAD_RUNTIME_DIR ?? "";
	const sha = env.AMQ_SQUAD_RUNTIME_MANIFEST_SHA256 ?? "";
	if (!dir && !sha) return null;
	const r = dir && sha ? checkRuntime(dir, sha) : { ok: false as const, reason: "runtime check variables are incomplete" };
	if (!r.ok) {
		process.stderr.write(`amq-squad pi refused (${RUNTIME_MISMATCH_REASON}): ${r.reason}\n`);
		try {
			record(r.reason);
		} catch {
			// the exit below is the refusal; the record is best effort
		}
		process.exit(EXIT_RUNTIME_MISMATCH);
	}
	if (env.AMQ_SQUAD_PI_TIMING === "1") {
		const at = Number(env.AMQ_SQUAD_EXEC_AT_MS);
		const window = Number.isFinite(at) && at > 0 ? Date.now() - at : -1;
		process.stderr.write(`amq-squad pi self-check: files=${r.files} bytes=${r.bytes} ms=${r.ms} window_ms=${window}\n`);
	}
	return r;
}
