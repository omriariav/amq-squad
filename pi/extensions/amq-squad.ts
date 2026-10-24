// @amq-squad/pi: the amq-squad package for the built-in pi (plan 4.1, 4.4,
// 16.3; brief 4-1). The lead and every pi seat load it with -e from the
// unpacked runtime. It
//   - calls the three hook verbs on every turn: context on before_agent_start,
//     guard before every tool call (fail closed, R36), evidence ingest on every
//     tool result (R35);
//   - delivers the role in the first message and in the system prompt;
//   - registers native AMQ tools (amq_send, amq_drain, amq_reply, amq_task_*);
//   - restricts models to the seat's registry entry (R16);
//   - holds a call that needs the owner (a typed gate, or a tool the seat must
//     have approved) until the first decision from any face, with a retry cap;
//   - runs the AMQ pi bridge at revision 4 (tool approval, session identity).
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import * as fs from "node:fs";
import * as path from "node:path";
import amqPiBridge, { raiseApproval, APPROVE_OPTION, REJECT_OPTION } from "./lib/amq-bridge.ts";
import { blockReason, callKey, DEFAULT_CAP, hold, manifestHash, RetryCap, type Decider, type Decision } from "./lib/hold.ts";
import { AMQ_DEFINITION, configFromEnv, contextOf, contextSection, gateReason, guardCall, ingest, outsideDeclared, roleMessage, type Config, type ContextData, type Verdict } from "./lib/hooks.ts";
import { restrictModels } from "./lib/models.ts";
import { registerTools } from "./lib/tools.ts";
import { run } from "./lib/verbs.ts";

const ROLE_MESSAGE = "amq-squad-role";
const STATUS = "amq-squad";

export default function amqSquad(pi: ExtensionAPI): void {
	// The runtime self-check is not here: it is the one-purpose preload
	// pi/preload/runtime-check.cjs, which node runs with --require before pi
	// (review 2, item 2 (b)).
	const cfg = configFromEnv(process.env);
	if ("inactive" in cfg) {
		let shown = false;
		pi.on("session_start", (_e, ctx) => {
			if (!shown) note(ctx, `amq-squad package inactive: ${cfg.inactive}`);
			shown = true;
		});
		return;
	}
	amqPiBridge(pi);
	registerTools(pi, cfg);
	restrictModels(pi, cfg.models, note);
	const approveTools = new Set((process.env.AMQ_SQUAD_APPROVE_TOOLS ?? "").split(",").map((s) => s.trim()).filter(Boolean));
	let cap = new RetryCap(DEFAULT_CAP);
	let roleDelivered = false;
	let turn = 0;
	let task: ContextData["task"] = null; // the seat's task from the last context call

	pi.on("session_start", (_e, ctx) => {
		cap = new RetryCap(DEFAULT_CAP);
		roleDelivered = branchHasRole(ctx);
	});
	pi.on("turn_start", () => {
		turn++;
	});

	// R35: role and rules every turn; the role also as the first message.
	pi.on("before_agent_start", async (event, ctx) => {
		const c = await contextOf(cfg);
		task = c.ok ? (c.data.task ?? null) : null;
		let roleText = c.ok ? (c.data.role_text ?? "") : "";
		if (!roleText) roleText = readRole();
		const sections = event.systemPromptOptions.sections;
		if (roleText) sections["amq-squad-role"] = roleText.trim();
		sections["amq-squad"] = c.ok ? contextSection(cfg, c.data) : `You are seat ${cfg.me}.\n${AMQ_DEFINITION}\namq-squad context is unavailable (${c.why}).`;
		if (!c.ok) status(ctx, `context unavailable: ${c.why}`);
		if (roleDelivered || !roleText) return undefined;
		roleDelivered = true;
		return { message: { customType: ROLE_MESSAGE, content: roleMessage(cfg, roleText), display: true } };
	});

	// R36: guard before every tool call; any guard failure blocks.
	pi.on("tool_call", async (event, ctx) => {
		const call = { toolName: event.toolName, input: event.input, toolCallId: event.toolCallId, cwd: ctx.cwd };
		const scope = outsideDeclared(task, event.toolName, event.input, ctx.cwd);
		if (scope) {
			await record(cfg, ctx, { type: "scope_block", toolName: call.toolName, toolCallId: call.toolCallId, reason: scope });
			return { block: true, reason: scope };
		}
		const g = await guardCall(cfg, call);
		if (g.kind === "block") {
			await record(cfg, ctx, { type: "guard_block", toolName: call.toolName, toolCallId: call.toolCallId, reason: g.reason });
			return { block: true, reason: g.reason };
		}
		const needsOwner = g.kind === "needs_gate";
		if (!needsOwner && !approveTools.has(event.toolName)) return undefined;
		const key = callKey(call.toolName, call.input);
		const capped = cap.admit(String(turn), key);
		const what = needsOwner ? "gate" : "approval";
		if (capped) {
			await record(cfg, ctx, { type: "hold", toolName: call.toolName, toolCallId: call.toolCallId, decision: capped.outcome, detail: capped.detail });
			return { block: true, reason: blockReason(capped, what) };
		}
		const d = await holdCall(cfg, ctx, call, needsOwner ? g.verdict : null);
		cap.record(key, d);
		await record(cfg, ctx, { type: "hold", toolName: call.toolName, toolCallId: call.toolCallId, decision: d.outcome, face: d.face, detail: d.detail });
		if (d.outcome === "allow") return undefined;
		return { block: true, reason: needsOwner ? gateReason(g.verdict, `${d.outcome}: ${d.detail}.`) : blockReason(d, what) };
	});

	// R35: one ledger line per tool result. A failed ingest never blocks.
	pi.on("tool_result", async (event, ctx) => {
		const r = await ingest(cfg, {
			toolName: event.toolName,
			toolCallId: event.toolCallId,
			input: event.input,
			content: event.content,
			isError: event.isError,
			cwd: ctx.cwd,
			sessionId: sessionId(ctx),
		});
		if (!r.ok) status(ctx, `evidence ingest failed: ${r.why}`);
		return undefined;
	});
}

// holdCall holds one call until the first decision. For a typed gate the
// approving face is the core: guard re-checked whenever the gate store
// changes turns allow once the owner's signed answer is recorded (R32); a
// local or bridge answer can only block it. For an approval tool a local
// confirm and a bridge answer (revision 4) race.
async function holdCall(cfg: Config, ctx: ExtensionContext, call: { toolName: string; input: unknown; toolCallId: string; cwd: string }, gate: Verdict | null): Promise<Decision> {
	const hash = manifestHash(call.toolName, call.input, call.toolCallId);
	const expiresAt = new Date(Date.now() + cfg.holdTimeoutMs);
	const prompt = gate ? `${call.toolName}: ${gate.action_class ?? "gate"} ${gate.target ?? ""}` : `${call.toolName}: ${preview(call.input)}`;
	const deciders: Decider[] = [];
	if (gate) deciders.push(gateAnswerFace(cfg, call));
	const remote = raiseApproval({ interactionId: safeId(call.toolCallId), prompt, manifestHash: hash, expiresAt, presence: gate ? "local_only" : "remote" });
	if (remote) {
		deciders.push((settle) => remote.wait((r) => settle({ outcome: r.outcome, detail: r.detail, face: "remote" })));
	}
	if (ctx.hasUI) deciders.push(localFace(ctx, prompt, gate !== null));
	status(ctx, `holding ${call.toolName} ${safeId(call.toolCallId)}: ${gate ? "waiting for the owner's signed gate answer" : "waiting for approval"}`);
	const d = await hold({ deciders, timeoutMs: cfg.holdTimeoutMs, signal: ctx.signal });
	status(ctx, undefined);
	const byRemote = d.face === "remote";
	remote?.resolved(byRemote, byRemote ? (d.outcome === "allow" ? APPROVE_OPTION : REJECT_OPTION) : undefined);
	return d;
}

// gateAnswerFace re-runs guard every second while the gate store may change;
// guard allows only when a verified, owner-signed answer binds this action
// and target.
function gateAnswerFace(cfg: Config, call: { toolName: string; input: unknown; toolCallId: string; cwd: string }): Decider {
	return (settle, signal) => {
		let busy = false;
		const tick = async () => {
			if (busy || signal.aborted) return;
			busy = true;
			try {
				const g = await guardCall(cfg, call, run);
				if (g.kind === "allow") settle({ outcome: "allow", detail: "verified gate answer recorded", face: "remote" });
				else if (g.kind === "block" && g.reason.includes("denied")) settle({ outcome: "deny", detail: g.reason, face: "remote" });
			} finally {
				busy = false;
			}
		};
		const timer = setInterval(tick, 1000);
		timer.unref();
		return () => clearInterval(timer);
	};
}

// localFace is the TUI or RPC dialog. For a typed gate it can only block: the
// owner approves a typed gate with a signature, never with a keystroke.
function localFace(ctx: ExtensionContext, prompt: string, gate: boolean): Decider {
	return (settle, signal) => {
		if (gate) {
			void ctx.ui
				.select(`amq-squad gate: ${prompt}`, ["Keep waiting for the owner's signed answer", "Block this call"], { signal })
				.then((choice) => {
					if (choice === "Block this call") settle({ outcome: "deny", detail: "blocked at the terminal", face: "local" });
				})
				.catch(() => undefined);
			return;
		}
		void ctx.ui
			.confirm("amq-squad approval", `Allow this call once?\n${prompt}`, { signal })
			.then((yes) => {
				if (signal.aborted) return;
				settle(yes ? { outcome: "allow", detail: "approved at the terminal", face: "local" } : { outcome: "deny", detail: "blocked at the terminal", face: "local" });
			})
			.catch(() => undefined);
	};
}

async function record(cfg: Config, ctx: ExtensionContext, rec: Record<string, unknown>): Promise<void> {
	const r = await run(cfg.bin, ["evidence", "ingest", "--me", cfg.me], { stdin: JSON.stringify({ ...rec, session_id: sessionId(ctx) }), timeoutMs: cfg.hookTimeoutMs });
	if (r.code !== 0) status(ctx, "evidence ingest failed for a hold record");
}

function branchHasRole(ctx: ExtensionContext): boolean {
	try {
		const entries = (ctx.sessionManager as unknown as { getBranch(): { type?: string; customType?: string }[] }).getBranch();
		return entries.some((e) => e.customType === ROLE_MESSAGE);
	} catch {
		return false;
	}
}

function readRole(): string {
	const p = process.env.AMQ_SQUAD_ROLE_PATH ?? "";
	if (!p || !path.isAbsolute(p)) return "";
	try {
		return fs.readFileSync(p, "utf8");
	} catch {
		return "";
	}
}

function sessionId(ctx: ExtensionContext): string {
	try {
		return ctx.sessionManager.getSessionId();
	} catch {
		return "";
	}
}

function safeId(id: string): string {
	return id.replace(/[^A-Za-z0-9._-]/g, "_").slice(0, 128) || "call";
}

function preview(input: unknown): string {
	const s = JSON.stringify(input) ?? "";
	return s.length > 300 ? s.slice(0, 300) + "..." : s;
}

function status(ctx: ExtensionContext, text: string | undefined): void {
	try {
		if (ctx.hasUI) ctx.ui.setStatus(STATUS, text);
	} catch {
		// UI is optional (json and print modes)
	}
}

function note(ctx: ExtensionContext, msg: string): void {
	try {
		if (ctx.hasUI) ctx.ui.notify(msg, "warning");
		else console.error(msg);
	} catch {
		console.error(msg);
	}
}
