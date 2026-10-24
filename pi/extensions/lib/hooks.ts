// The three verbs on every turn (plan 4.2, 4.4, R35, R36):
//   before_agent_start -> amq-squad context --me H --json   (inject role, rules, task, gates)
//   tool_call          -> amq-squad guard --me H --stdin     (fails closed: any failure blocks)
//   tool_result        -> amq-squad evidence ingest --me H   (one ledger line per tool result)
// The pi payload names are the ones guard and ingest read (2a-3 notes):
// toolName, input, toolCallId.
import { describe, firstLine, run, type RunResult } from "./verbs.ts";

export type Config = {
	bin: string; // absolute amq-squad path (AMQ_SQUAD_BIN)
	amq: string; // absolute amq path (AMQ_SQUAD_AMQ), "" when absent
	me: string;
	root: string;
	session: string;
	hookTimeoutMs: number;
	holdTimeoutMs: number;
	models: string[]; // provider/id the seat may use (registry entry)
	lead: boolean; // AMQ_SQUAD_LEAD=1: the team lead (dispatch tools)
};

const HANDLE = /^[a-z0-9][a-z0-9_-]{0,63}$/;

export type Runner = typeof run;

// configFromEnv reads the launcher's environment. Without a valid AMQ
// identity the package is inactive (like the reference bridge); the launcher
// always sets one, so an inactive package means pi was started by hand.
export function configFromEnv(env: NodeJS.ProcessEnv): Config | { inactive: string } {
	const me = env.AM_ME ?? "";
	const root = env.AM_ROOT ?? "";
	if (!HANDLE.test(me) || !root.startsWith("/")) {
		return { inactive: "AM_ME and AM_ROOT are not a valid AMQ identity; start pi through amq-squad (lead or seat-exec)" };
	}
	return {
		bin: env.AMQ_SQUAD_BIN ?? "",
		amq: env.AMQ_SQUAD_AMQ ?? "",
		me,
		root,
		session: env.AM_SESSION ?? "",
		hookTimeoutMs: positive(env.AMQ_SQUAD_HOOK_TIMEOUT_MS, 5000),
		holdTimeoutMs: positive(env.AMQ_SQUAD_HOLD_TIMEOUT_MS, 30 * 60 * 1000),
		models: (env.AMQ_SQUAD_MODELS ?? "").split(",").map((s) => s.trim()).filter(Boolean),
		lead: env.AMQ_SQUAD_LEAD === "1",
	};
}

function positive(v: string | undefined, def: number): number {
	const n = Number(v);
	return Number.isFinite(n) && n > 0 ? n : def;
}

// ---- guard ----------------------------------------------------------------

export type Verdict = {
	verdict: "allow" | "needs_gate" | "deny";
	reason_code?: string;
	reason?: string;
	action_class?: string;
	target_digest?: string;
	target?: string;
};

export type GuardOutcome =
	| { kind: "allow"; verdict: Verdict; ms: number }
	| { kind: "needs_gate"; verdict: Verdict; ms: number }
	| { kind: "block"; reason: string; ms: number };

export type Call = { toolName: string; input: unknown; toolCallId: string; cwd: string };

// guardCall asks guard for one verdict. It fails closed: guard missing, not
// startable, timed out, exiting non-zero, or printing no verdict all block
// the call (R36, brief 4-1 acceptance 3).
export async function guardCall(cfg: Config, call: Call, runner: Runner = run): Promise<GuardOutcome> {
	const payload = JSON.stringify({ toolName: call.toolName, input: call.input, toolCallId: call.toolCallId, cwd: call.cwd });
	const r = await runner(cfg.bin, ["guard", "--me", cfg.me, "--stdin"], { stdin: payload, timeoutMs: cfg.hookTimeoutMs });
	return verdictOf(r, cfg.hookTimeoutMs);
}

export function verdictOf(r: RunResult, timeoutMs: number): GuardOutcome {
	const failed = (why: string): GuardOutcome => ({
		kind: "block",
		ms: r.ms,
		reason: `amq-squad guard failed closed (${why}); the call is blocked. Do not retry; report this to your lead.`,
	});
	if (r.spawnError || r.timedOut || r.code !== 0) return failed(describe(r, timeoutMs));
	let v: Verdict;
	try {
		const line = r.stdout.trim().split("\n").filter(Boolean).pop() ?? "";
		const parsed = JSON.parse(line);
		v = (parsed && parsed.data && parsed.data.verdict ? parsed.data : parsed) as Verdict;
	} catch {
		return failed("no verdict on stdout");
	}
	switch (v.verdict) {
		case "allow":
			return { kind: "allow", verdict: v, ms: r.ms };
		case "needs_gate":
			return { kind: "needs_gate", verdict: v, ms: r.ms };
		case "deny":
			return { kind: "block", ms: r.ms, reason: `amq-squad guard: denied (${v.reason_code ?? "deny"}): ${v.reason ?? ""}`.trim() };
		default:
			return failed(`unknown verdict ${JSON.stringify(v.verdict)}`);
	}
}

// gateReason is the block text for an unanswered typed gate: an unattended
// seat cannot "ask", so it is told to raise the gate (2a-3 notes, plugin note).
export function gateReason(v: Verdict, detail: string): string {
	return (
		`amq-squad guard: this action needs the owner's signed gate (${v.action_class ?? "gate"} on ${v.target ?? "?"}, ${v.reason_code ?? ""}). ` +
		`${detail} Ask your lead to raise the gate; do not retry this call.`
	);
}

// ---- context --------------------------------------------------------------

export type ContextData = {
	seat?: { handle?: string; session?: string; root?: string };
	role?: { path?: string; sha256?: string };
	role_text?: string;
	rules?: { path?: string; sha256?: string };
	task?: { id: string; title: string; status: string; class: string; declared_files?: string[]; lease_expires_at?: string; lease_claimed?: boolean; block_reason?: string } | null;
	open_gates?: { gate_id: string; class: string; digest: string; state: string }[];
	warnings?: string[];
};

export async function contextOf(cfg: Config, runner: Runner = run): Promise<{ ok: true; data: ContextData } | { ok: false; why: string }> {
	const r = await runner(cfg.bin, ["context", "--me", cfg.me, "--json"], { timeoutMs: cfg.hookTimeoutMs });
	if (r.spawnError || r.timedOut || r.code !== 0) return { ok: false, why: describe(r, cfg.hookTimeoutMs) };
	try {
		const env = JSON.parse(r.stdout);
		return { ok: true, data: (env.data ?? env) as ContextData };
	} catch {
		return { ok: false, why: "context printed no JSON" };
	}
}

// AMQ_DEFINITION is stated in every injection: the S5 lead misread AMQ as
// "Advanced Message Queuing", decided it had no worker and started coding.
export const AMQ_DEFINITION =
	"AMQ is the Agent Message Queue: a local mail system on this machine that connects you to the other agents (seats) of your team. " +
	'It is not "Advanced Message Queuing". Reach other seats only with the native tools amq_send, amq_drain and amq_reply ' +
	"(and the amq_task_* tools for the task store); never run the amq CLI through bash.";

export function contextSection(cfg: Config, d: ContextData): string {
	const lines = [`You are seat ${cfg.me} of an amq-squad team${d.seat?.session ? ` (session ${d.seat.session})` : ""}.`, AMQ_DEFINITION];
	if (d.rules?.path) lines.push(`Team rules: ${d.rules.path} (sha256 ${d.rules.sha256 ?? "?"}). Read them before you act on a task.`);
	const t = d.task;
	if (t) {
		const files = t.declared_files?.length ? ` Declared files: ${t.declared_files.join(", ")}; touch nothing else.` : "";
		if (t.status === "blocked") lines.push(`Current task ${t.id} "${t.title}" is blocked (${t.block_reason ?? ""}); wait for the lead.`);
		else if (t.lease_claimed === false) lines.push(`Task ${t.id} "${t.title}" (${t.class}) is leased to you: claim it with amq_task_claim before ${t.lease_expires_at ?? "the lease ends"}.${files}`);
		else lines.push(`Current task ${t.id} "${t.title}" (${t.class}, ${t.status}).${files} Finish with amq_task_done and the evidence.`);
	} else {
		lines.push("Current task: none.");
	}
	const open = (d.open_gates ?? []).filter((g) => g.state === "open");
	if (open.length) lines.push(`Open gates: ${open.map((g) => `${g.gate_id} (${g.class})`).join(", ")}.`);
	for (const w of d.warnings ?? []) lines.push(`Warning: ${w}`);
	return lines.join("\n");
}

// roleMessage is the first message of a session: the role text plus the AMQ
// definition (plan 4.1: "the role text in the first message and also in the
// system prompt").
export function roleMessage(cfg: Config, roleText: string): string {
	return [`# Your role (amq-squad seat ${cfg.me})`, "", roleText.trim(), "", AMQ_DEFINITION].join("\n");
}

// ---- declared files -------------------------------------------------------

// outsideDeclared returns the reason to block a write or edit outside the
// seat's current task's declared files, or "" to let guard decide. It is the
// in-session hint of plan 7.2's task scope; the harness commit step stays the
// enforcement point (amendment 21). Paths are relative to the seat's cwd, the
// repository root of its worktree.
export function outsideDeclared(task: ContextData["task"], toolName: string, input: unknown, cwd: string): string {
	if (!task || task.status === "blocked" || !task.declared_files?.length) return "";
	if (toolName !== "write" && toolName !== "edit") return "";
	const p = (input as { path?: unknown; file_path?: unknown })?.path ?? (input as { file_path?: unknown })?.file_path;
	if (typeof p !== "string" || !p) return "";
	const abs = p.startsWith("/") ? p : `${cwd.replace(/\/$/, "")}/${p}`;
	const root = cwd.replace(/\/$/, "") + "/";
	const rel = abs.startsWith(root) ? normalize(abs.slice(root.length)) : abs;
	if (task.declared_files.some((g) => declaredMatch(g, rel))) return "";
	return (
		`amq-squad: ${rel} is outside task ${task.id}'s declared files (${task.declared_files.join(", ")}). ` +
		"Put the change in the files the task names; the commit step refuses any other file."
	);
}

function normalize(rel: string): string {
	const out: string[] = [];
	for (const part of rel.split("/")) {
		if (part === "" || part === ".") continue;
		if (part === "..") out.pop();
		else out.push(part);
	}
	return out.join("/");
}

// declaredMatch: an exact path, a directory (trailing / or /**) covering a
// tree, or a glob with * and ? inside one segment (filepath.Match).
export function declaredMatch(glob: string, rel: string): boolean {
	const g = glob.replace(/\/\*\*$/, "").replace(/\/$/, "");
	if (g === rel || rel.startsWith(g + "/")) return true;
	const gs = g.split("/");
	const rs = rel.split("/");
	if (gs.length !== rs.length) return false;
	return gs.every((seg, i) => new RegExp("^" + seg.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, "[^/]*").replace(/\?/g, "[^/]") + "$").test(rs[i]));
}

// ---- evidence -------------------------------------------------------------

const MAX_CONTENT = 64 * 1024;

export type ToolResult = { toolName: string; toolCallId: string; input: unknown; content: unknown; isError: boolean; cwd: string; sessionId: string };

export function evidencePayload(r: ToolResult): string {
	const text = textOf(r.content);
	return JSON.stringify({
		type: "tool_result",
		toolName: r.toolName,
		toolCallId: r.toolCallId,
		input: r.input,
		isError: r.isError,
		outcome: r.isError ? "error" : "ok",
		content: text.length > MAX_CONTENT ? text.slice(0, MAX_CONTENT) + "\n[truncated]" : text,
		cwd: r.cwd,
		session_id: r.sessionId,
	});
}

export async function ingest(cfg: Config, r: ToolResult, runner: Runner = run): Promise<{ ok: boolean; why: string }> {
	const res = await runner(cfg.bin, ["evidence", "ingest", "--me", cfg.me], { stdin: evidencePayload(r), timeoutMs: cfg.hookTimeoutMs });
	const ok = !res.spawnError && !res.timedOut && res.code === 0;
	return { ok, why: ok ? "" : describe(res, cfg.hookTimeoutMs) };
}

export function textOf(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	return content
		.filter((p) => p && p.type === "text" && typeof p.text === "string")
		.map((p) => p.text as string)
		.join("");
}

export { firstLine };
