// pi-hold-ext: spike S5 step 3 (amq-squad 3.0 phase 1).
//
// Question: can a pi extension hold a tool call until an external decision
// arrives? That is the bridge revision 4 approval seam
// (agent-message-queue docs/pi-bridge-protocol.md, "Bridge revision 4").
//
// Mechanism: pi awaits every `tool_call` handler before it executes the tool
// (dist/core/agent-session.js _beforeToolCall -> runner.emitToolCall, no
// timeout). This handler returns a promise that settles only when
//   <AM_ROOT>/agents/<AM_ME>/extensions/io.github.omriariav.amq-squad/approve-<id>  -> allow
//   <AM_ROOT>/agents/<AM_ME>/extensions/io.github.omriariav.amq-squad/deny-<id>     -> block
// appears, the hold times out (block), or the turn is aborted (block).
//
// An approve file may be empty (spike convenience) or JSON. When it is JSON with
// a manifest_hash, the hash must equal the pending call's hash or the answer is
// refused and the call blocks: an allow is bound to exactly one call.
//
// Env:
//   AMQ_SQUAD_HOLD_TOOLS       comma list of tool names to hold (default bash,edit,write; "*" = all)
//   AMQ_SQUAD_HOLD_TIMEOUT_MS  hold timeout, default 120000; on timeout the call is blocked
//   AMQ_SQUAD_HOLD_THROW       test only: "1" makes the handler throw, to observe pi's fail-safe
//
// Evidence: every pending call, decision, and timing is appended to
// <dir>/hold-log.jsonl; pending-<id>.json and resolved-<id>.json are written beside it.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { createHash } from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";

const NS = "io.github.omriariav.amq-squad";

export default function holdExtension(pi: ExtensionAPI): void {
	const root = process.env.AM_ROOT;
	const me = process.env.AM_ME;
	if (!root || !me || !/^[a-z0-9_-]+$/.test(me) || me.startsWith("-")) {
		// Same rule as the AMQ reference bridge: no AMQ identity, no seam.
		return;
	}
	const dir = path.join(root, "agents", me, "extensions", NS);
	const held = new Set((process.env.AMQ_SQUAD_HOLD_TOOLS ?? "bash,edit,write").split(",").map((s) => s.trim()).filter(Boolean));
	const timeoutMs = Number(process.env.AMQ_SQUAD_HOLD_TIMEOUT_MS ?? 120000);
	const throwForTest = process.env.AMQ_SQUAD_HOLD_THROW === "1";

	const log = (rec: Record<string, unknown>) => {
		fs.appendFileSync(path.join(dir, "hold-log.jsonl"), JSON.stringify({ at: new Date().toISOString(), pid: process.pid, ...rec }) + "\n", { mode: 0o600 });
	};

	pi.on("session_start", () => {
		fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
		log({ event: "session_start", held: [...held], timeout_ms: timeoutMs, throw_for_test: throwForTest });
	});

	pi.on("tool_call", async (event, ctx) => {
		if (!held.has("*") && !held.has(event.toolName)) return undefined;
		fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
		const id = safeId(event.toolCallId);
		const manifestHash = "sha256:" + createHash("sha256").update(JSON.stringify({ tool: event.toolName, input: event.input, id: event.toolCallId })).digest("hex");
		const started = Date.now();
		const expiresAt = new Date(started + timeoutMs).toISOString();
		writeAtomic(path.join(dir, `pending-${id}.json`), {
			id, tool_call_id: event.toolCallId, tool: event.toolName, input: event.input,
			manifest_hash: manifestHash, expires_at: expiresAt, created_at: new Date(started).toISOString(),
		});
		log({ event: "hold", id, tool: event.toolName, manifest_hash: manifestHash, expires_at: expiresAt });
		if (ctx.hasUI) ctx.ui.setStatus("amq-squad-hold", `holding ${event.toolName} ${id}: waiting for approve-${id}`);

		if (throwForTest) {
			log({ event: "throw_for_test", id });
			throw new Error(`hold extension test exception for ${id}`);
		}

		const decision = await waitForDecision(dir, id, manifestHash, timeoutMs, ctx.signal);
		const heldMs = Date.now() - started;
		writeAtomic(path.join(dir, `resolved-${id}.json`), { id, ...decision, held_ms: heldMs, resolved_at: new Date().toISOString() });
		log({ event: "resolved", id, ...decision, held_ms: heldMs });
		if (ctx.hasUI) ctx.ui.setStatus("amq-squad-hold", undefined);
		if (decision.outcome === "allow") return undefined;
		return { block: true, reason: `amq-squad hold: ${decision.outcome} (${decision.detail})` };
	});
}

type Decision = { outcome: "allow" | "deny" | "timeout" | "aborted" | "refused"; detail: string };

function waitForDecision(dir: string, id: string, manifestHash: string, timeoutMs: number, signal?: AbortSignal): Promise<Decision> {
	const approve = path.join(dir, `approve-${id}`);
	const deny = path.join(dir, `deny-${id}`);
	return new Promise((resolve) => {
		let done = false;
		const finish = (d: Decision) => {
			if (done) return;
			done = true;
			clearInterval(poll);
			clearTimeout(timer);
			watcher?.close();
			signal?.removeEventListener("abort", onAbort);
			resolve(d);
		};
		const check = () => {
			if (fs.existsSync(deny)) return finish({ outcome: "deny", detail: `deny-${id} present` });
			if (fs.existsSync(approve)) {
				const body = fs.readFileSync(approve, "utf8").trim();
				if (body) {
					let rec: { manifest_hash?: string } | null = null;
					try { rec = JSON.parse(body); } catch { return finish({ outcome: "refused", detail: "approve file is not JSON" }); }
					if (rec?.manifest_hash && rec.manifest_hash !== manifestHash) return finish({ outcome: "refused", detail: "manifest_hash mismatch" });
				}
				return finish({ outcome: "allow", detail: `approve-${id} present` });
			}
		};
		const onAbort = () => finish({ outcome: "aborted", detail: "turn aborted" });
		signal?.addEventListener("abort", onAbort);
		const poll = setInterval(check, 250);
		const timer = setTimeout(() => finish({ outcome: "timeout", detail: `no decision in ${timeoutMs} ms` }), timeoutMs);
		let watcher: fs.FSWatcher | undefined;
		try { watcher = fs.watch(dir, () => check()); } catch { /* polling covers it */ }
		check();
	});
}

function safeId(toolCallId: string): string {
	const s = toolCallId.replace(/[^A-Za-z0-9._-]/g, "_").slice(0, 96);
	return s || createHash("sha256").update(toolCallId).digest("hex").slice(0, 16);
}

function writeAtomic(file: string, data: unknown): void {
	const tmp = `${file}.tmp-${process.pid}`;
	fs.writeFileSync(tmp, JSON.stringify(data, null, 2) + "\n", { mode: 0o600 });
	fs.renameSync(tmp, file);
}
