// The team lead's first turn carries the host facts the owner would
// otherwise state (brief 31-3 PR 2, section 5), as a system line built from
// `amq-squad status --json` and `amq-squad context --json --host`, never a
// model guess: the repository, the branch and its tip, the installed AMQ
// and amq-squad versions, the roster with each seat's runtime and model,
// the open tasks and the open gates. A fact the verbs do not give is shown
// as missing, not invented. It is a message in the conversation, given on
// the first turn and again only when a fact in it changed (the plan
// reviewer: the owner counts tokens; a line repeating what the model holds
// is the cost he named): HostFacts keys on the files the facts come from
// (HostFacts.key: the task store, the scheduler and its decisions, the
// lead's launch record, every entry of the gate store, the gates whose
// not_after passed, the repository's HEAD and the ref it names, the AMQ
// executable); only when that key changed does it run the verbs, and only
// a changed line is sent.
import * as fs from "node:fs";
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

// hostPollPaths are the files the host facts' key stats under the lead's
// root: the task store's generation, the scheduler's state and decisions
// (a roster decision), and the lead's launch record.
export function hostPollPaths(root: string, me = ""): string[] {
	const ext = path.join(root, "extensions", "amq-squad");
	const paths = [path.join(ext, "tasks", ".generation"), path.join(ext, "scheduler.json"), path.join(ext, "scheduler", "decisions.jsonl")];
	if (me) paths.push(path.join(ext, "launch", me, "launch.json"));
	return paths;
}

// GATE_DIRS are the gate store's directories the key lists entry by entry
// (a write inside a directory does not touch its parent's mtime): the
// questions, the approvals, the denials and the consumed marks.
const GATE_DIRS = ["gates", "answers", "denials", path.join("consumed", "gates")];

// GIT_DIRS_TIMEOUT_MS bounds the one git call that finds the repository's
// git directories (a linked worktree's .git is a pointer file).
const GIT_DIRS_TIMEOUT_MS = 3000;

// HostFacts gives the host line on the first turn and again only when a
// fact in it changed.
export class HostFacts {
	cfg: Config;
	runner: Runner;
	stat: Stat;
	now: () => Date;
	lastKey: string | undefined;
	lastLine: string | undefined;
	// the repository's git directory and common directory, resolved once
	// through git (null: not resolved yet; undefined: git could not say)
	git: { dir: string; common: string } | undefined | null = null;

	constructor(cfg: Config, runner: Runner, stat: Stat = defaultStat, now: () => Date = () => new Date()) {
		this.cfg = cfg;
		this.runner = runner;
		this.stat = stat;
		this.now = now;
	}

	// key is what the facts depend on, as files' mtimes and sizes (Codex's
	// re-check on #837, item 4): the poll paths; every entry of the gate
	// store's directories; how many gates' not_after has passed (a gate
	// expires with time alone); the repository's HEAD in its own git
	// directory, the ref HEAD names (in the common directory, where a
	// linked worktree's refs live), the packed refs and HEAD's log; the AMQ
	// executable context --host asks for its version, and the amq-squad
	// binary.
	async key(project: string): Promise<string> {
		const st = (p: string) => {
			const s = this.stat(p);
			return s ? `${s.mtimeMs}:${s.size}` : "-";
		};
		const ext = path.join(this.cfg.root, "extensions", "amq-squad");
		const parts = [this.cfg.root, ...hostPollPaths(this.cfg.root, this.cfg.me).map(st)];
		for (const d of GATE_DIRS) parts.push(listing(path.join(ext, d), st));
		parts.push(`expired:${expiredGates(path.join(ext, "gates"), this.now())}`);
		const git = await this.gitDirs(project);
		if (git) {
			parts.push(st(path.join(git.dir, "HEAD")), st(path.join(git.dir, "logs", "HEAD")), st(path.join(git.common, "packed-refs")));
			const ref = headRef(path.join(git.dir, "HEAD"));
			if (ref) parts.push(st(path.join(git.common, ref)));
		}
		const amq = amqPath();
		if (amq) parts.push(st(amq));
		// the amq-squad binary itself (its version is in the line)
		if (path.isAbsolute(this.cfg.bin)) parts.push(st(this.cfg.bin));
		return parts.join("|");
	}

	// gitDirs resolves the git directories once, bounded.
	async gitDirs(project: string): Promise<{ dir: string; common: string } | undefined> {
		if (this.git !== null) return this.git;
		this.git = undefined;
		if (!project) return undefined;
		const r = await this.runner("git", ["-C", project, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir"], { timeoutMs: GIT_DIRS_TIMEOUT_MS });
		const lines = r.code === 0 && !r.timedOut && !r.spawnError ? r.stdout.split("\n").map((l) => l.trim()).filter(Boolean) : [];
		if (lines.length === 2 && lines.every((l) => path.isAbsolute(l))) this.git = { dir: lines[0], common: lines[1] };
		return this.git;
	}

	// next is the line to send this turn, undefined when nothing changed.
	async next(): Promise<string | undefined> {
		const project = process.env.AMQ_SQUAD_PROJECT || this.cfg.project || "";
		const key = await this.key(project);
		if (key === this.lastKey) return undefined;
		this.lastKey = key;
		const line = hostLine(await readStatus(this.cfg, this.runner), hostOf(await contextOf(this.cfg, this.runner, true)));
		if (line === this.lastLine) return undefined;
		this.lastLine = line;
		return line;
	}
}

// listing is a directory's entries with their mtimes and sizes, "-" when
// the directory is absent.
function listing(dir: string, st: (p: string) => string): string {
	let names: string[];
	try {
		names = fs.readdirSync(dir).sort();
	} catch {
		return "-";
	}
	return names.map((n) => `${n}:${st(path.join(dir, n))}`).join(",");
}

// expiredGates counts the gate records whose not_after has passed.
function expiredGates(dir: string, now: Date): number {
	let n = 0;
	let names: string[] = [];
	try {
		names = fs.readdirSync(dir);
	} catch {
		return 0;
	}
	for (const name of names) {
		if (!name.endsWith(".json") || name.endsWith(".question.json")) continue;
		try {
			const t = Date.parse(JSON.parse(fs.readFileSync(path.join(dir, name), "utf8")).not_after ?? "");
			if (Number.isFinite(t) && t <= now.getTime()) n++;
		} catch {
			// not a gate record
		}
	}
	return n;
}

// headRef is the ref a HEAD file names ("ref: refs/heads/x"), "" for a
// detached HEAD or an unreadable one.
function headRef(head: string): string {
	try {
		const m = /^ref:\s*(refs\/\S+)\s*$/.exec(fs.readFileSync(head, "utf8").trim());
		return m ? m[1] : "";
	} catch {
		return "";
	}
}

// amqPath is the AMQ executable context --host asks for its version:
// $AMQ_SQUAD_AMQ when absolute, else amq on PATH.
function amqPath(): string {
	const env = process.env.AMQ_SQUAD_AMQ ?? "";
	if (path.isAbsolute(env)) return env;
	for (const d of (process.env.PATH ?? "").split(path.delimiter)) {
		if (!d) continue;
		const p = path.join(d, "amq");
		try {
			fs.accessSync(p, fs.constants.X_OK);
			return p;
		} catch {
			// not here
		}
	}
	return "";
}
