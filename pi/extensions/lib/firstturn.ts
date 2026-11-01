// The team lead's first turn carries the host facts the owner would
// otherwise state (brief 31-3 PR 2, section 5), as a system line built from
// `amq-squad status --json` and `amq-squad context --json --host`, never a
// model guess: the repository, the branch and its tip, the installed AMQ
// and amq-squad versions, the roster with each seat's runtime and model,
// the open tasks and the open gates. A fact the verbs do not give is shown
// as missing, not invented. It is a message in the conversation, given on
// the first turn and again only when a fact in it changed (the plan
// reviewer: the owner counts tokens; a line repeating what the model holds
// is the cost he named): HostFacts keys on what the screen's poll stats
// (the task store's .generation, scheduler.json) plus the decisions log
// (a roster decision), the gate store and its consumed answers, and the
// repository's HEAD and its log (the branch and its tip); only when that
// key changed does it run the verbs, and only a changed line is sent.
import * as path from "node:path";
import { contextOf, type Config, type ContextData, type Runner } from "./hooks.ts";
import { defaultStat, type Stat } from "./screen.ts";

// HOST_MESSAGE is the host line's message type.
export const HOST_MESSAGE = "amq-squad-host";

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
const REDACTED = "<redacted>";

// The extension's own pass over every fact before it is interpolated
// (Codex on #837, item 6), after context --host's redact.Block: a value
// under a credential name (NAME=value, NAME: value) and the real provider
// shapes (internal/redact RealCredentialShapes) read <redacted>. A narrow
// second layer, not the Go rules: it redacts more rather than less.
const CREDENTIAL_ASSIGN = /([A-Za-z0-9_.-]*(?:key|secret|token|passw(?:or)?d|credential|nsec)[A-Za-z0-9_.-]*)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s"',;]+)/gi;
const REAL_SHAPES = [/AKIA[0-9A-Z]{16}/g, /sk-[A-Za-z0-9]{20,}/g, /ghp_[A-Za-z0-9]{30,}/g, /AIza[0-9A-Za-z_-]{30,}/g, /nsec1[a-z0-9]{20,}/g, /BEGIN [A-Z ]*PRIVATE KEY/g];

export function redactFact(v: string): string {
	let out = v.replace(CREDENTIAL_ASSIGN, (_m, name: string, sep: string) => `${name}${sep}${REDACTED}`);
	for (const re of REAL_SHAPES) out = out.replace(re, REDACTED);
	return out;
}

function fact(v: string | undefined): string {
	return v && v.trim() ? redactFact(v.trim()) : MISSING;
}

// hostLine is the system line from a status document and context's host
// facts; either may be undefined (the verb failed), and its facts then read
// missing.
export function hostLine(st: StatusDoc | undefined, host: HostFacts | undefined): string {
	const roster = st?.seats ? (st.seats.length ? st.seats.map((s) => `${fact(s.handle)} ${fact(s.runtime)}/${fact(s.model)}`).join(", ") : "none") : MISSING;
	const tasks = st?.tasks ? st.tasks.filter((t) => !CLOSED_TASKS.has(t.task_state ?? "")) : undefined;
	const gates = st?.gates ? st.gates.filter((g) => g.state === "open") : undefined;
	const list = <T>(xs: T[] | undefined, f: (x: T) => string) => (xs ? (xs.length ? xs.map(f).join(", ") : "none") : MISSING);
	return [
		"Host facts (from amq-squad status --json and context --json --host, read this turn; not a guess):",
		`repository ${fact(host?.repository || st?.project_path)};`,
		`branch ${fact(host?.branch)} at ${fact(host?.tip)};`,
		`AMQ ${fact(host?.amq_version)}; amq-squad ${fact(st?.producer_version)};`,
		`roster: ${roster};`,
		`open tasks: ${list(tasks, (t) => `${fact(t.id)} ${fact(t.task_state)}${t.title ? ` "${redactFact(t.title)}"` : ""}`)};`,
		`open gates: ${list(gates, (g) => `${fact(g.id)} ${fact(g.action_class)}`)}.`,
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

// hostPollPaths are what the host facts' key stats under the lead's root
// and in the repository.
export function hostPollPaths(root: string, project: string): string[] {
	const ext = path.join(root, "extensions", "amq-squad");
	const paths = [path.join(ext, "tasks", ".generation"), path.join(ext, "scheduler.json"), path.join(ext, "scheduler", "decisions.jsonl"), path.join(ext, "gates"), path.join(ext, "consumed")];
	if (project) paths.push(path.join(project, ".git", "HEAD"), path.join(project, ".git", "logs", "HEAD"));
	return paths;
}

// HostFacts gives the host line on the first turn and again only when a
// fact in it changed.
export class HostFacts {
	cfg: Config;
	runner: Runner;
	stat: Stat;
	lastKey: string | undefined;
	lastLine: string | undefined;

	constructor(cfg: Config, runner: Runner, stat: Stat = defaultStat) {
		this.cfg = cfg;
		this.runner = runner;
		this.stat = stat;
	}

	// next is the line to send this turn, undefined when nothing changed.
	async next(): Promise<string | undefined> {
		const project = process.env.AMQ_SQUAD_PROJECT || this.cfg.project || "";
		const key = [this.cfg.root, ...hostPollPaths(this.cfg.root, project).map((p) => {
			const s = this.stat(p);
			return s ? `${s.mtimeMs}:${s.size}` : "-";
		})].join("|");
		if (key === this.lastKey) return undefined;
		this.lastKey = key;
		const line = hostLine(await readStatus(this.cfg, this.runner), hostOf(await contextOf(this.cfg, this.runner, true)));
		if (line === this.lastLine) return undefined;
		this.lastLine = line;
		return line;
	}
}
