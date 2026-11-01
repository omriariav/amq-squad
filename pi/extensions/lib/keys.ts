// The keys on the lead's screen (brief 31-2, PR 1): add, reset, clear and
// answer. The owner presses a key by typing `/squad <key>` in the lead's
// pi: pi hands a registered command to the extension and never to the
// model, so the extension's input path reads the key, and the model cannot
// press one. Each key gathers its input with pi's own dialogs and runs its
// CLI verb from the lead's process (this pi), exactly as the lead's tools
// do: a verb that is a scheduler decision is proved under R4 by
// construction, since its process descends from the proved lead. No key is
// a tool: the model's tool list gains nothing (row 2).
//
// The widen key is PR 2 of brief 31-2: it appears only on a task whose
// block carries t26's typed widen reason, and it is not built here.
// The add key adds a task; a seat add waits (see the execution note,
// docs/v3-spike/31-evidence/31-2-keys.md): on this tip the roster relaunch
// starts the roster the scheduler was started with and stamps the
// scheduler's own start, so a new squad.yaml line would not start and its
// launch budget could not run from the key press.
//
// The answer key is Q7's fallback (the execution note): the terminal door
// for the four typed gates is not built, so the key shows the gate, posts
// its question over Buzz when it has not reached Buzz, and says the gate is
// waiting there. It never writes an answer.
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Config, Runner } from "./hooks.ts";
import type { ScreenStatus } from "./screen.ts";
import { toolArgv } from "./tools.ts";
import { firstLine, type RunResult } from "./verbs.ts";

export const KEYS = ["add", "reset", "clear", "answer"] as const;
export type KeyName = (typeof KEYS)[number];

const CLASSES = ["implement", "extract", "summarize", "script", "plan", "review"];
const KEEP_POOL = "keep its pool";
// CANCELLED is a key aborted at any dialog (Codex P2 2 on #831): pi's
// dialogs return undefined on a cancel, and a cancelled key runs no verb.
const CANCELLED = "cancelled, nothing run";
const TYPE_POOL = "type a pool (runtime:model:effort)";

// KeyOutcome is what the key leaves on the screen: its line, and whether
// the verb refused (the line then carries the refusal as the verb printed
// it).
export type KeyOutcome = { key: string; refused: boolean; line: string };

// The `amq-squad gate status --json` row the answer key reads (the gate
// record and its state).
type GateStatusRow = { gate_id: string; class: string; typed_class: string; target: string; digest: string; state: string; question_sent: boolean };

// typedPrefix is the 16-hex prefix the owner types for a gate (R32): the
// first 16 hex of its target digest.
export function typedPrefix(digest: string): string {
	return (digest ?? "").replace(/^sha256:/, "").slice(0, 16);
}

// refusal is a verb's refusal as it printed it: its first stderr line (the
// binary prints "error: <verb>: <name>..."), else its stdout, else how it
// failed to run.
export function refusal(r: RunResult): string {
	if (r.spawnError) return `could not start: ${r.spawnError}`;
	if (r.timedOut) return "timed out";
	const line = firstLine(r.stderr) || firstLine(r.stdout);
	return line.replace(/^error:\s*/, "") || `exit ${r.code}`;
}

function failed(r: RunResult): boolean {
	return Boolean(r.spawnError) || r.timedOut || r.code !== 0;
}

function data<T>(stdout: string): T | undefined {
	try {
		return (JSON.parse(stdout) as { data: T }).data;
	} catch {
		return undefined;
	}
}

function splitList(s: string | undefined): string[] {
	return (s ?? "")
		.split(",")
		.map((x) => x.trim())
		.filter(Boolean);
}

// Keys runs the screen's keys for one lead.
export class Keys {
	cfg: Config;
	runner: Runner;

	constructor(cfg: Config, runner: Runner) {
		this.cfg = cfg;
		this.runner = runner;
	}

	private async verb(args: string[]): Promise<RunResult> {
		// From this process, the lead's pi, as the lead's tools run theirs.
		return this.runner(this.cfg.bin, args, { timeoutMs: Math.max(this.cfg.hookTimeoutMs, 30000) });
	}

	// press runs one key against the screen's last status document.
	async press(name: string, ctx: ExtensionContext, st: ScreenStatus | undefined): Promise<KeyOutcome> {
		const key = name.trim();
		if (!(KEYS as readonly string[]).includes(key)) {
			return { key, refused: true, line: `unknown key ${JSON.stringify(key)}; the keys are ${KEYS.join(", ")}` };
		}
		if (!ctx.hasUI) return { key, refused: true, line: "the keys need the lead's interactive screen" };
		if (!st) return { key, refused: true, line: "no status yet; the screen is still reading it" };
		switch (key as KeyName) {
			case "add":
				return this.add(ctx);
			case "reset":
				return this.reset(ctx, st);
			case "clear":
				return this.clear(ctx, st);
			case "answer":
				return this.answer(ctx, st);
		}
	}

	// add: the owner types the title, picks the class, types the declared
	// files and the pool; the key runs `task add` (the lead's amq_task_add).
	async add(ctx: ExtensionContext): Promise<KeyOutcome> {
		const k = "add";
		const title = (await ctx.ui.input("add task: title"))?.trim();
		if (!title) return { key: k, refused: false, line: CANCELLED };
		const cls = await ctx.ui.select("add task: class", CLASSES);
		if (!cls) return { key: k, refused: false, line: CANCELLED };
		const filesIn = await ctx.ui.input("add task: declared files (comma-separated)");
		if (filesIn === undefined) return { key: k, refused: false, line: CANCELLED };
		const files = splitList(filesIn);
		const poolIn = await ctx.ui.input("add task: pool runtime:model:effort (empty: the router picks)");
		if (poolIn === undefined) return { key: k, refused: false, line: CANCELLED };
		const pool = poolIn.trim();
		const argv = [...toolArgv.taskAdd(this.cfg, { title, class: cls, files, pool: pool || undefined }), "--json"];
		const r = await this.verb(argv);
		if (failed(r)) return { key: k, refused: true, line: refusal(r) };
		const t = data<{ task: { id: string; state: string } }>(r.stdout)?.task;
		return { key: k, refused: false, line: `added ${t?.id ?? "?"} ${JSON.stringify(title)} (${t?.state ?? "?"})` };
	}

	// reset: on a blocked task; `task reset`, with --pool when the owner
	// picks another pool (the 3.0.2 re-pin rule names this path).
	async reset(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {
		const k = "reset";
		const blocked = st.tasks.filter((t) => t.task_state === "blocked");
		if (!blocked.length) return { key: k, refused: true, line: "no blocked task" };
		const labels = blocked.map((t) => `${t.id}  ${t.block_reason || "blocked"}`);
		const pick = await ctx.ui.select("reset: which blocked task", labels);
		if (!pick) return { key: k, refused: false, line: CANCELLED };
		const t = blocked[labels.indexOf(pick)];
		const pools = [...new Set(st.seats.filter((s) => s.runtime && s.model).map((s) => [s.runtime, s.model, s.effort].filter(Boolean).join(":")))];
		const choice = await ctx.ui.select(`reset ${t.id}: pool`, [KEEP_POOL, ...pools, TYPE_POOL]);
		if (!choice) return { key: k, refused: false, line: CANCELLED };
		let pool = choice === KEEP_POOL ? "" : choice;
		if (choice === TYPE_POOL) {
			// a cancelled (or empty) typed pool aborts: a reset with the old
			// pool would return the task to dispatch, which was not asked
			pool = (await ctx.ui.input(`reset ${t.id}: pool runtime:model:effort`))?.trim() ?? "";
			if (!pool) return { key: k, refused: false, line: CANCELLED };
		}
		const argv = toolArgv.taskReset(this.cfg, { id: t.id });
		if (pool) argv.push("--pool", pool);
		argv.push("--json");
		const r = await this.verb(argv);
		if (failed(r)) return { key: k, refused: true, line: `${t.id}: ${refusal(r)}` };
		return { key: k, refused: false, line: `${t.id} reset${pool ? ` onto ${pool}` : ""}` };
	}

	// clear: on a seat's warning line; the owner's acknowledgement, recorded
	// by the scheduler as a decision naming the warning and the seat's
	// launch (`scheduler clear`, over its socket, proved under R4).
	async clear(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {
		const k = "clear";
		const lines: { seat: string; code: string; launch: string; label: string }[] = [];
		for (const s of st.seats) {
			if (!s.launch?.launch_id) continue;
			for (const w of s.warnings ?? []) {
				const code = w.split(":")[0].trim();
				lines.push({ seat: s.handle, code, launch: s.launch.launch_id, label: `${s.handle}  ${w.length > 100 ? w.slice(0, 97) + "..." : w}` });
			}
		}
		if (!lines.length) return { key: k, refused: true, line: "no warning line to clear" };
		const pick = await ctx.ui.select("clear: acknowledge which warning line (never a gate; the seat's state does not change)", lines.map((l) => l.label));
		if (!pick) return { key: k, refused: false, line: CANCELLED };
		const l = lines.find((x) => x.label === pick)!;
		const r = await this.verb(["scheduler", "clear", "--seat", l.seat, "--warning", l.code, "--launch", l.launch, "--json"]);
		if (failed(r)) return { key: k, refused: true, line: `${l.seat} ${l.code}: ${refusal(r)}` };
		return { key: k, refused: false, line: `${l.seat} ${l.code} acknowledged for launch ${l.launch}` };
	}

	// answer: Q7's fallback. The gate is read fresh, shown with its kind,
	// target and typed prefix; its question is posted over Buzz when it has
	// not reached Buzz; the key says the gate waits there. A gate that is not
	// open (answered over Buzz, denied, expired) is refused by its state.
	async answer(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {
		const k = "answer";
		const open = st.gates.filter((g) => g.state === "open");
		if (!open.length) return { key: k, refused: true, line: "no open gate" };
		const labels = open.map((g) => `${g.id}  ${g.action_class}  ${typedPrefix(g.target_digest)}`);
		const pick = await ctx.ui.select("answer: which gate (the terminal door is not built: Buzz answers it)", labels);
		if (!pick) return { key: k, refused: false, line: CANCELLED };
		const id = open[labels.indexOf(pick)].id;
		const s = await this.verb(["gate", "status", "--gate", id, "--json"]);
		if (failed(s)) return { key: k, refused: true, line: `${id}: ${refusal(s)}` };
		const g = data<GateStatusRow[]>(s.stdout)?.[0];
		if (!g) return { key: k, refused: true, line: `${id}: gate status returned no gate` };
		if (g.state !== "open") {
			return { key: k, refused: true, line: `${id}: gate_not_open: the gate is ${g.state}; an answer at either door closes it` };
		}
		const shown = `${g.gate_id} ${g.class} (${g.typed_class}) ${g.target} prefix ${typedPrefix(g.digest)}`;
		let posted = "";
		if (!g.question_sent) {
			const p = await this.verb(["gate", "post", "--gate", id, "--json"]);
			if (failed(p)) return { key: k, refused: true, line: `${shown}: the question was not posted over Buzz: ${refusal(p)}` };
			posted = "question posted over Buzz; ";
		}
		const line = `${shown}: ${posted}waiting on Buzz: the terminal door is not built (Q7); answer it in the owner's Buzz DM thread`;
		try {
			ctx.ui.notify(line, "info");
		} catch {
			// the line is on the screen either way
		}
		return { key: k, refused: false, line };
	}
}
