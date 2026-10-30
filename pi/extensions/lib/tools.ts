// Native AMQ tools (plan 4.1, 16.3, amendment 11): the seat reaches the team
// through typed tools instead of bash `amq`, which removes the argument
// guessing of the S5 runs (a positional `amq reply`, a non-existent
// `amq post`) and gives guard a typed call. Mail goes through the amq CLI;
// tasks go through the kept `amq-squad task` verbs (the task store owns
// dispatch, R17: mail is not the queue). Bodies travel on stdin (`--body -`),
// never as an argument, so a body that starts with "@" is text, not a file.
import { Type } from "typebox";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { Config, Runner } from "./hooks.ts";
import { describe, run } from "./verbs.ts";
import { repoList, repoRead } from "./repo.ts";

const KINDS = ["brainstorm", "review_request", "review_response", "question", "answer", "decision", "status", "todo"];
const CLASSES = ["extract", "summarize", "script", "implement", "plan", "review"];
const HANDLE = /^[a-z0-9][a-z0-9_@:.-]{0,127}$/;
const MAX_OUT = 32 * 1024;

type Result = { content: { type: "text"; text: string }[]; details: unknown };

function ok(text: string): Result {
	const t = text.length > MAX_OUT ? text.slice(0, MAX_OUT) + "\n[truncated]" : text;
	return { content: [{ type: "text", text: t || "(no output)" }], details: undefined };
}

async function call(runner: Runner, bin: string, args: string[], stdin: string | undefined, cfg: Config, signal?: AbortSignal): Promise<Result> {
	if (!bin) throw new Error("not configured: the launcher sets AMQ_SQUAD_AMQ and AMQ_SQUAD_BIN");
	const r = await runner(bin, args, { stdin, timeoutMs: Math.max(cfg.hookTimeoutMs, 30000), signal });
	if (r.spawnError || r.timedOut || r.code !== 0) {
		throw new Error(`${args[0]} failed (${describe(r, 30000)})${r.stdout.trim() ? `: ${r.stdout.trim().slice(0, 2000)}` : ""}`);
	}
	return ok(r.stdout.trim() || r.stderr.trim());
}

function checkKind(kind: string | undefined): void {
	if (kind !== undefined && !KINDS.includes(kind)) throw new Error(`kind must be one of ${KINDS.join(", ")}`);
}

// toolArgv builds each tool's argv; exported for the tests.
export const toolArgv = {
	send(cfg: Config, p: { to: string; subject?: string; kind?: string; thread?: string }): string[] {
		const to = p.to.split(",").map((s) => s.trim()).filter(Boolean);
		if (!to.length || !to.every((h) => HANDLE.test(h))) throw new Error(`invalid recipient ${JSON.stringify(p.to)}`);
		checkKind(p.kind);
		const a = ["send", "--me", cfg.me, "--to", to.join(","), "--json"];
		if (p.subject) a.push("--subject", p.subject);
		if (p.kind) a.push("--kind", p.kind);
		if (p.thread) a.push("--thread", p.thread);
		return [...a, "--body", "-"];
	},
	drain(cfg: Config, p: { limit?: number }): string[] {
		const a = ["drain", "--me", cfg.me, "--include-body", "--json"];
		if (p.limit && p.limit > 0) a.push("--limit", String(Math.floor(p.limit)));
		return a;
	},
	reply(cfg: Config, p: { id: string; kind?: string }): string[] {
		if (!p.id || p.id.startsWith("-")) throw new Error("id is the message id shown by amq_drain");
		checkKind(p.kind);
		const a = ["reply", "--me", cfg.me, "--id", p.id, "--json"];
		if (p.kind) a.push("--kind", p.kind);
		return [...a, "--body", "-"];
	},
	taskList(_cfg: Config, p: { state?: string }): string[] {
		const a = ["task", "list", "--json"];
		if (p.state) a.push("--state", p.state);
		return a;
	},
	taskClaim(cfg: Config, p: { id?: string }): string[] {
		const a = ["task", "claim"];
		if (p.id) {
			if (p.id.startsWith("-")) throw new Error("invalid task id");
			a.push(p.id);
		}
		return [...a, "--me", cfg.me];
	},
	taskDone(cfg: Config, p: { id: string; evidence: string }): string[] {
		if (!p.id || p.id.startsWith("-")) throw new Error("invalid task id");
		if (!p.evidence?.trim()) throw new Error("evidence is required: the command you ran and its result");
		return ["task", "done", p.id, "--me", cfg.me, "--evidence", p.evidence];
	},
	taskReset(cfg: Config, p: { id: string }): string[] {
		if (!p.id || p.id.startsWith("-")) throw new Error("invalid task id");
		return ["task", "reset", p.id, "--me", cfg.me];
	},
	// schedulerRelaunch is the lead's relaunch of the roster (3.0.1 t16): the
	// `amq-squad scheduler relaunch` verb itself, which proves the lead over
	// the scheduler's decision socket from this process's ancestry.
	schedulerRelaunch(_cfg: Config, p: { reason: string }): string[] {
		if (!p.reason?.trim()) throw new Error("reason is required: why the roster is relaunched (for example, which seat is gone)");
		return ["scheduler", "relaunch", "--reason", p.reason.trim(), "--json"];
	},
	// taskWiden is the lead's widening of a task the commit step refused for
	// files outside its declared set (3.0.1 t16): the `amq-squad task widen`
	// verb with only the paths that refusal names. refused is those paths
	// (refusedPaths); files, when given, must be among them, so the tool
	// never widens a task by a path the seat's refusal did not name.
	taskWiden(_cfg: Config, p: { id: string; reason: string; files?: string[] }, refused: string[]): string[] {
		if (!p.id || p.id.startsWith("-")) throw new Error("invalid task id");
		if (!p.reason?.trim()) throw new Error("reason is required: the lead's decision to widen");
		const files = p.files?.length ? p.files : refused;
		const unknown = files.filter((f) => !refused.includes(f));
		if (unknown.length) throw new Error(`not a path the commit step refused for task ${p.id}: ${unknown.join(", ")} (refused: ${refused.join(", ")})`);
		if (!files.length || files.some((f) => !f || f.includes(",") || f.startsWith("-"))) throw new Error("no path to widen by");
		return ["task", "widen", p.id, "--files", files.join(","), "--reason", p.reason.trim(), "--json"];
	},
	taskAdd(_cfg: Config, p: { title: string; class: string; description?: string; files?: string[]; depends_on?: string[]; pool?: string; priority?: number }): string[] {
		if (!CLASSES.includes(p.class)) throw new Error(`class must be one of ${CLASSES.join(", ")}`);
		const a = ["task", "add", "--title", p.title, "--class", p.class];
		if (p.description) a.push("--desc", p.description);
		if (p.files?.length) a.push("--files", p.files.join(","));
		if (p.depends_on?.length) a.push("--depends-on", p.depends_on.join(","));
		if (p.pool) a.push("--pool", p.pool);
		if (p.priority !== undefined) a.push("--priority", String(Math.floor(p.priority)));
		return a;
	},
};

// refusedPaths is the paths a task's commit refusal names as outside its
// declared files: the task (from `amq-squad task show --json`) must be
// blocked with outside_declared_files, and the paths are read from the
// refusal's detail as the commit step writes it ("outside_declared_files:
// a.go, b.go (declared: ...)"; more than eight end with "and N more", which
// is refused rather than guessed).
export function refusedPaths(t: { id?: string; state?: string; block_reason?: string; block_detail?: string }): string[] {
	const prefix = "outside_declared_files: ";
	if (t.state !== "blocked" || t.block_reason !== "outside_declared_files") {
		throw new Error(`task ${t.id ?? "?"} is not blocked for files outside its declared set (state ${t.state ?? "?"}, reason ${t.block_reason || "none"}); there is nothing to widen`);
	}
	for (const seg of (t.block_detail ?? "").split("; ")) {
		if (!seg.startsWith(prefix)) continue;
		let rest = seg.slice(prefix.length);
		const cut = rest.indexOf(" (");
		if (cut >= 0) rest = rest.slice(0, cut);
		if (/ and \d+ more$/.test(rest)) throw new Error(`task ${t.id}: the refusal names more paths than it lists; widen with amq-squad task widen from the owner's decision`);
		const paths = rest.split(", ").map((x) => x.trim()).filter(Boolean);
		if (paths.length) return paths;
	}
	throw new Error(`task ${t.id ?? "?"}: the refusal names no path`);
}

export function registerTools(pi: ExtensionAPI, cfg: Config, runner: Runner = run): string[] {
	const names: string[] = [];
	const reg = (def: Parameters<ExtensionAPI["registerTool"]>[0]) => {
		pi.registerTool(def);
		names.push(def.name);
	};
	reg({
		name: "amq_send",
		label: "AMQ send",
		description: "Send an AMQ message to another seat of your team (Agent Message Queue, the local mail between agents).",
		promptSnippet: "amq_send: mail another seat (to, subject, kind, body)",
		parameters: Type.Object({
			to: Type.String({ description: "recipient handle, or several separated by commas" }),
			body: Type.String({ description: "message text" }),
			subject: Type.Optional(Type.String()),
			kind: Type.Optional(Type.String({ description: `one of ${KINDS.join(", ")}` })),
			thread: Type.Optional(Type.String({ description: "thread id; required for several recipients" })),
		}),
		async execute(_id, p, signal) {
			return call(runner, cfg.amq, toolArgv.send(cfg, p), p.body, cfg, signal);
		},
	});
	reg({
		name: "amq_drain",
		label: "AMQ drain",
		description: "Read and acknowledge your new AMQ messages (with bodies). Run it when a message says new mail arrived.",
		promptSnippet: "amq_drain: read your new mail",
		parameters: Type.Object({ limit: Type.Optional(Type.Integer({ minimum: 1 })) }),
		async execute(_id, p, signal) {
			return call(runner, cfg.amq, toolArgv.drain(cfg, p), undefined, cfg, signal);
		},
	});
	reg({
		name: "amq_reply",
		label: "AMQ reply",
		description: "Reply to one AMQ message by its id (the id field amq_drain shows); thread and recipient are set for you.",
		promptSnippet: "amq_reply: answer a message by id",
		parameters: Type.Object({
			id: Type.String({ description: "message id from amq_drain" }),
			body: Type.String(),
			kind: Type.Optional(Type.String({ description: `one of ${KINDS.join(", ")}` })),
		}),
		async execute(_id, p, signal) {
			return call(runner, cfg.amq, toolArgv.reply(cfg, p), p.body, cfg, signal);
		},
	});
	reg({
		name: "amq_task_list",
		label: "Task list",
		description: "List the squad's tasks in the task store (JSON).",
		parameters: Type.Object({ state: Type.Optional(Type.String()) }),
		async execute(_id, p, signal) {
			return call(runner, cfg.bin, toolArgv.taskList(cfg, p), undefined, cfg, signal);
		},
	});
	reg({
		name: "amq_task_claim",
		label: "Task claim",
		description: "Claim the task the scheduler leased to you (or a given id). Claim before you start work.",
		parameters: Type.Object({ id: Type.Optional(Type.String()) }),
		async execute(_id, p, signal) {
			return call(runner, cfg.bin, toolArgv.taskClaim(cfg, p), undefined, cfg, signal);
		},
	});
	reg({
		name: "amq_task_done",
		label: "Task done",
		description: "Report your task done with evidence (the command you ran and its result). The harness commits the declared files.",
		parameters: Type.Object({ id: Type.String(), evidence: Type.String() }),
		async execute(_id, p, signal) {
			return call(runner, cfg.bin, toolArgv.taskDone(cfg, p), undefined, cfg, signal);
		},
	});
	if (cfg.lead) {
		reg({
			name: "amq_task_add",
			label: "Task add",
			description:
				"Add one task to the squad's task store for a seat to do. Give each task one class (extract, summarize, script, implement, plan, review), " +
				"the exact files it may touch, and a description a seat can act on alone. The scheduler leases tasks to seats; you do not send work by mail.",
			promptSnippet: "amq_task_add: dispatch one classed task through the scheduler",
			parameters: Type.Object({
				title: Type.String(),
				class: Type.String({ description: CLASSES.join(" | ") }),
				description: Type.Optional(Type.String()),
				files: Type.Optional(Type.Array(Type.String(), { description: "declared files or directories the task may change" })),
				depends_on: Type.Optional(Type.Array(Type.String())),
				pool: Type.Optional(Type.String({ description: "runtime:model:effort" })),
				priority: Type.Optional(Type.Integer()),
			}),
			async execute(_id, p, signal) {
				return call(runner, cfg.bin, toolArgv.taskAdd(cfg, p), undefined, cfg, signal);
			},
		});
		const root = process.env.AMQ_SQUAD_PROJECT || process.cwd();
		reg({
			name: "amq_repo_read",
			label: "Read (repository)",
			description: "Read a file of the repository (read-only, inside the repository root). Optional 1-based line offset and line limit.",
			promptSnippet: "amq_repo_read: read a repository file",
			parameters: Type.Object({ path: Type.String(), offset: Type.Optional(Type.Integer({ minimum: 1 })), limit: Type.Optional(Type.Integer({ minimum: 1 })) }),
			async execute(_id, p) {
				return ok(repoRead(root, p.path, p.offset, p.limit));
			},
		});
		reg({
			name: "amq_repo_list",
			label: "List (repository)",
			description: "List a directory of the repository (read-only, inside the repository root); directories end with /.",
			promptSnippet: "amq_repo_list: list a repository directory",
			parameters: Type.Object({ path: Type.Optional(Type.String()) }),
			async execute(_id, p) {
				return ok(repoList(root, p.path));
			},
		});
		reg({
			name: "amq_status",
			label: "Squad status",
			description: "The squad's status as JSON (amq-squad status --json): seats, tasks with their evidence and state, the attention queue and gates. Read-only.",
			promptSnippet: "amq_status: the squad's seats, tasks and gates",
			parameters: Type.Object({}),
			async execute(_id, _p, signal) {
				return call(runner, cfg.bin, ["status", "--json"], undefined, cfg, signal);
			},
		});
		reg({
			name: "amq_scheduler_relaunch",
			label: "Relaunch the roster",
			description:
				"Relaunch the squad's roster when a task is blocked seat_replacement_needs_relaunch (a seat is gone and AMQ cannot start one alone): " +
				"the seats finish what they hold, every seat closes, and the roster starts again in one launch. Give the reason.",
			promptSnippet: "amq_scheduler_relaunch: replace a gone seat by relaunching the roster",
			parameters: Type.Object({ reason: Type.String({ description: "why: which seat is gone, which task waits" }) }),
			async execute(_id, p, signal) {
				return call(runner, cfg.bin, toolArgv.schedulerRelaunch(cfg, p), undefined, cfg, signal);
			},
		});
		reg({
			name: "amq_task_widen",
			label: "Task widen",
			description:
				"Widen a task the harness refused for changing files outside its declared files (outside_declared_files), by the paths that refusal names " +
				"and no others; files, if given, must be among them. Give your decision as the reason.",
			promptSnippet: "amq_task_widen: accept a refused task's out-of-scope paths",
			parameters: Type.Object({
				id: Type.String({ description: "the refused task's id" }),
				reason: Type.String({ description: "your decision: why these paths belong to the task" }),
				files: Type.Optional(Type.Array(Type.String(), { description: "a subset of the paths the refusal names (default: all of them)" })),
			}),
			async execute(_id, p, signal) {
				if (!p.id || p.id.startsWith("-")) throw new Error("invalid task id");
				const shown = await call(runner, cfg.bin, ["task", "show", p.id, "--json"], undefined, cfg, signal);
				let t: Parameters<typeof refusedPaths>[0];
				try {
					t = JSON.parse(shown.content[0].text).data.task;
				} catch {
					throw new Error(`task ${p.id}: amq-squad task show did not return the task`);
				}
				return call(runner, cfg.bin, toolArgv.taskWiden(cfg, p, refusedPaths(t)), undefined, cfg, signal);
			},
		});
		reg({
			name: "amq_task_reset",
			label: "Task reset",
			description:
				"Reset a blocked task, for example one the harness refused because the seat changed a file outside its declared files: " +
				"the seat's report is discarded and the scheduler leases the task again.",
			parameters: Type.Object({ id: Type.String() }),
			async execute(_id, p, signal) {
				return call(runner, cfg.bin, toolArgv.taskReset(cfg, p), undefined, cfg, signal);
			},
		});
	}
	return names;
}
