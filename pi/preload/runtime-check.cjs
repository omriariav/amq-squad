// amq-squad pi runtime self-check (review 2, item 2 (b)).
//
// A one-purpose CommonJS preload: node loads it with --require before the pi
// entry, so its first statement runs before any pi or package code. It
// re-hashes every file of the runtime manifest (node, the pi tree, the
// package, fd and rg) against the manifest whose SHA-256 the launcher passes
// in the forced environment (the embedded digest, the trust root), refuses a
// missing, extra or non-regular entry, and exits 70 with the reason
// runtime_mismatch_in_process on any mismatch. The launcher consumes exit 70
// (closes the seat, writes the reason to the ledger). Without the two
// variables (a user's own pi) it does nothing.
//
// What stays open, the accepted same-UID limit (4-1-execution.md,
// amendment 25): an in-place swap of node or of this file between the
// launcher's content check and node's own load of them.
"use strict";
const check = (function () {
	const crypto = require("node:crypto");
	const fs = require("node:fs");
	const path = require("node:path");
	const MANIFEST = ".embedpi-manifest.json";
	const MARKER = ".embedpi-complete";

	function checkRuntime(dir, wantSha) {
		const t0 = Date.now();
		let raw;
		try {
			raw = fs.readFileSync(path.join(dir, MANIFEST));
		} catch (err) {
			return { ok: false, reason: "runtime manifest unreadable: " + String(err) };
		}
		if (crypto.createHash("sha256").update(raw).digest("hex") !== wantSha) {
			return { ok: false, reason: "runtime manifest differs from the embedded one" };
		}
		let m;
		try {
			m = JSON.parse(raw.toString("utf8"));
		} catch (err) {
			return { ok: false, reason: "runtime manifest is not JSON" };
		}
		const want = new Map();
		const dirs = new Set();
		for (const f of m.files || []) {
			want.set(f.path, f);
			for (let d = path.posix.dirname(f.path); d !== "."; d = path.posix.dirname(d)) dirs.add(d);
		}
		if (want.size === 0) return { ok: false, reason: "runtime manifest lists no files" };
		// Every entry of the tree: no extra file or directory, no symlink.
		const seen = new Set();
		const stack = [""];
		while (stack.length) {
			const rel = stack.pop();
			let ents;
			try {
				ents = fs.readdirSync(path.join(dir, rel), { withFileTypes: true });
			} catch (err) {
				return { ok: false, reason: "cannot list " + (rel || ".") + ": " + String(err) };
			}
			for (const d of ents) {
				const p = rel ? rel + "/" + d.name : d.name;
				if (!rel && (d.name === MANIFEST || d.name === MARKER)) continue;
				if (d.isDirectory()) {
					if (!dirs.has(p)) return { ok: false, reason: "unexpected runtime directory " + p };
					stack.push(p);
				} else if (d.isFile()) {
					if (!want.has(p)) return { ok: false, reason: "unexpected runtime entry " + p };
					seen.add(p);
				} else {
					return { ok: false, reason: "runtime entry " + p + " is not a regular file or directory" };
				}
			}
		}
		let bytes = 0;
		for (const [p, f] of want) {
			if (!seen.has(p)) return { ok: false, reason: "runtime file missing: " + p };
			let b;
			try {
				b = fs.readFileSync(path.join(dir, p));
			} catch (err) {
				return { ok: false, reason: "runtime file unreadable: " + p };
			}
			if (b.length !== f.size || crypto.createHash("sha256").update(b).digest("hex") !== f.sha256) {
				return { ok: false, reason: "SHA-256 mismatch: " + p };
			}
			bytes += b.length;
		}
		return { ok: true, files: want.size, bytes: bytes, ms: Date.now() - t0 };
	}

	function enforce(env, onRefuse) {
		const dir = env.AMQ_SQUAD_RUNTIME_DIR || "";
		const sha = env.AMQ_SQUAD_RUNTIME_MANIFEST_SHA256 || "";
		if (!dir && !sha) return null;
		const r = dir && sha ? checkRuntime(dir, sha) : { ok: false, reason: "runtime check variables are incomplete" };
		if (!r.ok) {
			process.stderr.write("amq-squad pi refused (runtime_mismatch_in_process): " + r.reason + "\n");
			if (onRefuse) onRefuse(r.reason);
			process.exit(70);
		}
		if (env.AMQ_SQUAD_PI_TIMING === "1") {
			const at = Number(env.AMQ_SQUAD_EXEC_AT_MS);
			const window = at > 0 ? Date.now() - at : -1;
			process.stderr.write("amq-squad pi self-check: files=" + r.files + " bytes=" + r.bytes + " ms=" + r.ms + " window_ms=" + window + "\n");
		}
		return r;
	}
	return { checkRuntime: checkRuntime, enforce: enforce };
})();

if (require.main === module || process.env.AMQ_SQUAD_RUNTIME_DIR || process.env.AMQ_SQUAD_RUNTIME_MANIFEST_SHA256) {
	// The reason reaches the launcher's status through exit 70; the launcher
	// writes the ledger line (it holds the identity and the ledger path).
	check.enforce(process.env, null);
}

module.exports = check;
