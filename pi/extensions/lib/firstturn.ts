// The team lead's first turn carries the host facts the owner would
// otherwise state (brief 31-3 PR 2, section 5), as a system line built from
// `amq-squad status --json` and `amq-squad context --json --host`, never a
// model guess: the repository, the branch and its tip, the installed AMQ
// and amq-squad versions, the roster with each seat's runtime and model,
// the open tasks and the open gates. A fact the verbs do not give is shown
// as missing, not invented. The line is rebuilt every turn, so a changed
// status changes it.
import type { Config, ContextData, Runner } from "./hooks.ts";

type StatusDoc = {
	project_path?: string;
	producer_version?: string;
	seats?: { handle: string; runtime?: string; model?: string }[];
	tasks?: { id: string; title?: string; task_state?: string }[];
	gates?: { id: string; action_class?: string; state?: string }[];
};

export type HostFacts = { repository?: string; branch?: string; tip?: string; amq_version?: string };

const MISSING = "missing";
const CLOSED_TASKS = new Set(["completed", "cancelled", "done"]);

function fact(v: string | undefined): string {
	return v && v.trim() ? v.trim() : MISSING;
}

// hostLine is the system line from a status document and context's host
// facts; either may be undefined (the verb failed), and its facts then read
// missing.
export function hostLine(st: StatusDoc | undefined, host: HostFacts | undefined): string {
	const roster = st?.seats ? (st.seats.length ? st.seats.map((s) => `${s.handle} ${fact(s.runtime)}/${fact(s.model)}`).join(", ") : "none") : MISSING;
	const tasks = st?.tasks ? st.tasks.filter((t) => !CLOSED_TASKS.has(t.task_state ?? "")) : undefined;
	const gates = st?.gates ? st.gates.filter((g) => g.state === "open") : undefined;
	const list = <T>(xs: T[] | undefined, f: (x: T) => string) => (xs ? (xs.length ? xs.map(f).join(", ") : "none") : MISSING);
	return [
		"Host facts (from amq-squad status --json and context --json --host, read this turn; not a guess):",
		`repository ${fact(host?.repository || st?.project_path)};`,
		`branch ${fact(host?.branch)} at ${fact(host?.tip)};`,
		`AMQ ${fact(host?.amq_version)}; amq-squad ${fact(st?.producer_version)};`,
		`roster: ${roster};`,
		`open tasks: ${list(tasks, (t) => `${t.id} ${t.task_state ?? MISSING}${t.title ? ` "${t.title}"` : ""}`)};`,
		`open gates: ${list(gates, (g) => `${g.id} ${fact(g.action_class)}`)}.`,
	].join(" ");
}

// readStatus runs status --json for the lead's root; undefined on failure.
export async function readStatus(cfg: Config, runner: Runner): Promise<StatusDoc | undefined> {
	const r = await runner(cfg.bin, ["status", "--root", cfg.root, "--json"], { timeoutMs: Math.max(cfg.hookTimeoutMs, 30000) });
	if (r.spawnError || r.timedOut || r.code !== 0) return undefined;
	try {
		const doc = JSON.parse(r.stdout);
		return (doc.data ?? doc) as StatusDoc;
	} catch {
		return undefined;
	}
}

// hostOf is context's host facts, undefined when context failed or gave
// none.
export function hostOf(c: { ok: true; data: ContextData } | { ok: false; why: string }): HostFacts | undefined {
	return c.ok ? ((c.data as ContextData & { host?: HostFacts }).host ?? undefined) : undefined;
}
