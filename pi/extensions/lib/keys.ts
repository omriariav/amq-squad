// The lead's actions on the team screen (brief 31-2; brief 3.1.1-3 gives
// each its own command): /add, /reset, /dismiss, /answer and /widen. The
// owner types the command in the lead's pi: pi hands a registered command
// to the extension and never to the model, so the extension's input path
// reads it, and the model cannot press one. Each key gathers its input with
// pi's own dialogs and runs its CLI verb from the lead's process (this pi),
// exactly as the lead's tools do: a verb that is a scheduler decision is
// proved under R4 by construction, since its process descends from the
// proved lead. No key is a tool: the model's tool list gains nothing.
// Each outcome is a plain line (what happened, what to do); the verb's own
// text and the codes go below the rule (brief 3.1.1-3 item 4).
//
// The add key adds a task or a seat (brief 31-2 PR 2, the seat half;
// docs/v3-spike/31-evidence/31-2-pr2-seat-add.md): a seat is written to
// squad.yaml through `team propose` and `team apply --add-seat` under the
// digest, and starts by one roster relaunch, which reads squad.yaml again;
// the owner confirms once, since every seat relaunches. The relaunch
// carries the confirmation's moment as --start: the seat's launch budget
// counts from it.
//
// The answer key is Q7's fallback (the execution note): the terminal door
// for the four typed gates is not built, so the key shows the gate, posts
// its question over Buzz when it has not reached Buzz, and says the gate is
// waiting there. It never writes an answer.
//
// The widen key (brief 31-2 PR 3, folded into 3.1.1-3) answers a seat's
// typed widen request (#844): it shows the requested paths and the seat's
// reason in pi's confirm dialog and runs `task widen` only on its yes.
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Config, Runner } from "./hooks.ts";
import type { ScreenStatus } from "./screen.ts";
import { refusedPaths, toolArgv } from "./tools.ts";
import { firstLine, type RunResult } from "./verbs.ts";
import { blockHappened, clean, RULE, sentence } from "./words.ts";

// KEYS are the team phase's actions, each its own command (brief 3.1.1-3:
// /add, /reset, /dismiss, /answer, /widen); dismiss is the 3.1.0 clear key,
// renamed since "clear" reads as wiping.
export const KEYS = ["add", "reset", "dismiss", "answer", "widen"] as const;
export type KeyName = (typeof KEYS)[number];

const CLASSES = ["implement", "extract", "summarize", "script", "plan", "review"];
const KEEP_POOL = "keep its pool";
// CANCELLED is a key aborted at any dialog (Codex P2 2 on #831): pi's
// dialogs return undefined on a cancel, and a cancelled key runs no verb.
const CANCELLED = "Cancelled; nothing was run.";
const TYPE_POOL = "type a pool (runtime:model:effort)";
export const ADD_KINDS = ["task", "seat"];
// The runtimes squad.yaml takes (team.Runtimes).
const RUNTIMES = ["claude", "codex", "pi", "cursor-agent", "grok"];
// WIDEN_REASON is the lead's recorded decision when the owner says yes to
// a seat's widen request at the screen.
export const WIDEN_REASON = "the owner accepted the seat's widen request at the lead's screen (/widen)";
// The blocks a widening accepts (the task widen verb's own rule).
const WIDENABLE = ["widen_requested", "outside_declared_files"];

// KeyOutcome is what the key leaves on the screen: its line in plain words
// (what happened and what to do), whether it refused, and the record that
// goes below the rule (the verb's refusal as it printed it, the codes and
// ids).
export type KeyOutcome = { key: string; refused: boolean; line: string; record?: string };

// The `amq-squad gate status --json` row the answer key reads (the gate
// record and its state).
type GateStatusRow = { gate_id: string; class: string; typed_class: string; target: string; digest: string; state: string; question_sent: boolean; text?: string };

// RULE is the line between a gate question's plain lines and its
// identifiers (gatestore.Rule).
export { RULE };

// plainOf is a gate text's plain lines, the text above its rule
// (gatestore.Plain): "" for a text with no rule (a gate raised before
// 3.1.1).
export function plainOf(text: string | undefined): string {
	const at = (text ?? "").indexOf(`\n\n${RULE}\n`);
	return at < 0 ? "" : (text ?? "").slice(0, at);
}

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

// refusalRecord is a refusal for the record below the screen's rule: the
// verb's first line and, when the verb printed its own rule (brief
// 3.1.1-3: task widen's refusal), the codes below it.
export function refusalRecord(r: RunResult): string {
	const text = r.stderr || r.stdout;
	const at = text.indexOf(`\n\n${RULE}\n`);
	const below = at < 0 ? "" : text.slice(at + RULE.length + 3).split("\n").map((l) => l.trim()).filter(Boolean).join("; ");
	return below ? `${refusal(r)} (${below})` : refusal(r);
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

	// press runs one key against the screen's last status document; arg is
	// the rest of the command line (/add task, /dismiss SEAT).
	async press(name: string, ctx: ExtensionContext, st: ScreenStatus | undefined, arg = ""): Promise<KeyOutcome> {
		const key = name.trim();
		if (!(KEYS as readonly string[]).includes(key)) {
			return { key, refused: true, line: `There is no /${key}. The commands you can use now are listed on the screen's last line.`, record: `unknown_command: ${key}` };
		}
		if (!ctx.hasUI) return { key, refused: true, line: `/${key} needs the lead's interactive screen; this pi has none.`, record: "no_ui" };
		if (!st) return { key, refused: true, line: "The screen has not read the team's status yet; try again in a moment.", record: "no_status" };
		switch (key as KeyName) {
			case "add":
				return this.add(ctx, st, arg.trim());
			case "reset":
				return this.reset(ctx, st);
			case "dismiss":
				return this.dismiss(ctx, st, arg.trim());
			case "answer":
				return this.answer(ctx, st);
			case "widen":
				return this.widen(ctx, st);
		}
	}

	// add: a task or a seat ("/add task", "/add seat", or picked). For a task
	// the owner types the title, picks the class, types the declared files
	// and the pool; the key runs `task add` (the lead's amq_task_add).
	async add(ctx: ExtensionContext, st: ScreenStatus, arg = ""): Promise<KeyOutcome> {
		const k = "add";
		if (arg && !ADD_KINDS.includes(arg)) return { key: k, refused: true, line: `There is no /add ${clean(arg, 30)}: /add takes task or seat.`, record: `add_kind_unknown: ${clean(arg, 60)}` };
		const kind = arg || (await ctx.ui.select("add: a task or a seat", ADD_KINDS));
		if (!kind) return { key: k, refused: false, line: CANCELLED };
		if (kind === "seat") return this.addSeat(ctx, st);
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
		if (failed(r)) return { key: k, refused: true, line: sentence("The task was not added", refusal(r)), record: refusal(r) };
		const t = data<{ task: { id: string; state: string } }>(r.stdout)?.task;
		return { key: k, refused: false, line: `Added ${t?.id ?? "the task"} "${clean(title)}"; it is ${(t?.state ?? "queued").replace(/_/g, " ")}.` };
	}

	// addSeat: the owner types the handle and the role, picks the runtime,
	// types the model and the effort; the key runs `team propose
	// --add-seat`, asks once (every seat relaunches), runs `team apply
	// --digest D --add-seat` with the proposal's digest, then `scheduler
	// relaunch --start STAMP`, STAMP the confirmation's moment (the key
	// press the seat's launch budget counts from). A squad.yaml that does
	// not take the seat is refused squad_invalid (below the rule); the
	// relaunch's refusal goes below the rule as the verb printed it.
	async addSeat(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {
		const k = "add";
		const project = st.project_path;
		if (!project) return { key: k, refused: true, line: "There is no squad.yaml to add a seat to: the team's status names no project.", record: "squad_invalid: status names no project" };
		const handle = (await ctx.ui.input("add seat: handle"))?.trim();
		if (!handle) return { key: k, refused: false, line: CANCELLED };
		const role = await ctx.ui.input("add seat: role (empty: the handle)");
		if (role === undefined) return { key: k, refused: false, line: CANCELLED };
		const runtime = await ctx.ui.select("add seat: runtime", RUNTIMES);
		if (!runtime) return { key: k, refused: false, line: CANCELLED };
		const model = await ctx.ui.input("add seat: model (empty: the runtime's default)");
		if (model === undefined) return { key: k, refused: false, line: CANCELLED };
		const effort = await ctx.ui.input("add seat: effort (empty: none)");
		if (effort === undefined) return { key: k, refused: false, line: CANCELLED };
		const spec = [handle, role.trim(), runtime, model.trim(), effort.trim()].join(":");
		const seatArgs = ["--project", project, "--add-seat", spec, "--json"];
		const p = await this.verb(["team", "propose", ...seatArgs]);
		if (failed(p)) return { key: k, refused: true, line: sentence(`Seat ${clean(handle, 30)} was not added, squad.yaml does not take it`, refusal(p)), record: `squad_invalid: ${refusalRecord(p)}` };
		const digest = data<{ digest: string }>(p.stdout)?.digest;
		if (!digest) return { key: k, refused: true, line: `Seat ${clean(handle, 30)} was not added: the proposal printed no digest.`, record: "squad_invalid: team propose returned no digest" };
		const ok = await ctx.ui.confirm(`add seat ${handle} (${spec})`, "this relaunches every seat; continue?");
		if (!ok) return { key: k, refused: false, line: "Cancelled; nothing was written." };
		const pressed = new Date().toISOString(); // the key press: the launch budget's start
		const a = await this.verb(["team", "apply", "--digest", digest, ...seatArgs]);
		if (failed(a)) return { key: k, refused: true, line: sentence(`Seat ${clean(handle, 30)} was not added`, refusal(a)), record: `seat ${handle}: ${refusalRecord(a)}` };
		const r = await this.verb(["scheduler", "relaunch", "--reason", `add seat ${handle} (the screen's add key)`, "--start", pressed, "--json"]);
		if (failed(r)) return { key: k, refused: true, line: sentence(`Seat ${clean(handle, 30)} is in squad.yaml, but the roster did not relaunch`, refusal(r)), record: `seat ${handle} is in squad.yaml; ${refusalRecord(r)}` };
		return { key: k, refused: false, line: `Seat ${clean(handle, 30)} added; the roster relaunches with it (every seat finishes what it holds first); its launch budget counts from ${pressed}.` };
	}

	// reset: on a blocked task; `task reset`, with --pool when the owner
	// picks another pool (the 3.0.2 re-pin rule names this path).
	async reset(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {
		const k = "reset";
		const blocked = st.tasks.filter((t) => t.task_state === "blocked");
		if (!blocked.length) return { key: k, refused: true, line: "Nothing to reset: no task is blocked.", record: "no_blocked_task" };
		const labels = blocked.map((t) => `${t.id}: ${blockHappened(t)}`);
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
		if (failed(r)) return { key: k, refused: true, line: sentence(`${t.id} was not reset`, refusal(r)), record: `${t.id}: ${refusalRecord(r)}` };
		return { key: k, refused: false, line: `${t.id} reset${pool ? ` onto ${pool}` : ""}; it goes back to the queue.` };
	}

	// dismiss (the 3.1.0 clear key): on a seat's warning line; the owner's
	// acknowledgement, recorded by the scheduler as a decision naming the
	// warning and the seat's launch (`scheduler clear`, over its socket,
	// proved under R4). "/dismiss SEAT" offers that seat's warnings only.
	async dismiss(ctx: ExtensionContext, st: ScreenStatus, seat = ""): Promise<KeyOutcome> {
		const k = "dismiss";
		const lines: { seat: string; code: string; launch: string; label: string }[] = [];
		for (const s of st.seats) {
			if (!s.launch?.launch_id || (seat && s.handle !== seat)) continue;
			for (const w of s.warnings ?? []) {
				const code = w.split(":")[0].trim();
				lines.push({ seat: s.handle, code, launch: s.launch.launch_id, label: `${s.handle}  ${w.length > 100 ? w.slice(0, 97) + "..." : w}` });
			}
		}
		if (!lines.length) {
			const line = seat ? `Nothing to dismiss: seat ${clean(seat, 30)} has no warning showing.` : "Nothing to dismiss: no seat has a warning showing.";
			return { key: k, refused: true, line, record: "no_warning_line" };
		}
		const pick = await ctx.ui.select("dismiss: acknowledge which warning (never a gate; the seat itself does not change)", lines.map((l) => l.label));
		if (!pick) return { key: k, refused: false, line: CANCELLED };
		const l = lines.find((x) => x.label === pick)!;
		const r = await this.verb(["scheduler", "clear", "--seat", l.seat, "--warning", l.code, "--launch", l.launch, "--json"]);
		if (failed(r)) return { key: k, refused: true, line: sentence(`Seat ${l.seat}'s warning was not dismissed`, refusal(r)), record: `${l.seat} ${l.code}: ${refusalRecord(r)}` };
		return { key: k, refused: false, line: `Seat ${l.seat}'s warning is dismissed for its current launch; the seat itself does not change.`, record: `${l.seat} ${l.code} acknowledged for launch ${l.launch}` };
	}

	// answer: Q7's fallback. The gate is read fresh, shown with its
	// question's plain lines, then (below the rule) its kind, target and
	// digest prefix; its question is posted over Buzz when it has not reached
	// Buzz; the key says the gate waits there. A gate that is not open
	// (answered over Buzz, denied, expired) is refused by its state.
	async answer(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {
		const k = "answer";
		const open = st.gates.filter((g) => g.state === "open");
		if (!open.length) return { key: k, refused: true, line: "Nothing to answer: no owner gate is open.", record: "no_open_gate" };
		const labels = open.map((g) => gateLabel(g));
		const pick = await ctx.ui.select("answer: which gate (the phone answers it; the terminal cannot)", labels);
		if (!pick) return { key: k, refused: false, line: CANCELLED };
		const id = open[labels.indexOf(pick)].id;
		const s = await this.verb(["gate", "status", "--gate", id, "--json"]);
		if (failed(s)) return { key: k, refused: true, line: sentence("The gate could not be read", refusal(s)), record: `${id}: ${refusalRecord(s)}` };
		const g = data<GateStatusRow[]>(s.stdout)?.[0];
		if (!g) return { key: k, refused: true, line: "The gate could not be read: its status came back empty.", record: `${id}: gate status returned no gate` };
		if (g.state !== "open") {
			return { key: k, refused: true, line: `That gate is no longer open: it is ${g.state}, since an answer at either door closes it.`, record: `${id}: gate_not_open (${g.state})` };
		}
		// brief 3.1.1-1: the question's plain lines verbatim, as the phone
		// shows them, then the identifiers for the record
		const plain = plainOf(g.text);
		const ids = `${g.gate_id} ${g.class} (${g.typed_class}) ${g.target} prefix ${typedPrefix(g.digest)}`;
		const first = plain.split("\n").find((l) => l.trim()) ?? `the ${g.typed_class || g.class} gate`;
		let posted = "";
		if (!g.question_sent) {
			const p = await this.verb(["gate", "post", "--gate", id, "--json"]);
			if (failed(p)) return { key: k, refused: true, line: sentence(`The question "${first}" did not reach the phone`, refusal(p)), record: `${ids}: the question was not posted over Buzz: ${refusalRecord(p)}` };
			posted = "the question was posted over Buzz; ";
		}
		const todo = "reply yes or no in the question's Buzz thread; the terminal cannot answer a typed gate";
		try {
			ctx.ui.notify(`${plain ? plain + "\n" : ""}${posted}${todo}\n\n${RULE}\n${ids}`, "info");
		} catch {
			// the line is on the screen either way
		}
		return { key: k, refused: false, line: `Waiting on your answer on the phone to "${first}": ${posted}${todo}.`, record: ids };
	}

	// widen: the owner's yes to a seat's typed widen request (#844), or to
	// a commit refusal's files outside the task. The key reads the task's
	// own record (`task show --json`): the paths the block recorded
	// (refused_paths) and the seat's reason (block_detail); it shows both in
	// pi's confirm dialog and, only on the dialog's yes, runs `task widen`
	// with exactly those paths, each as one --path (the lead's
	// amq_task_widen argv). Several blocked tasks: the owner picks one.
	async widen(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {
		const k = "widen";
		const blocked = st.tasks.filter((t) => t.task_state === "blocked" && WIDENABLE.includes(t.block_reason));
		if (!blocked.length) return { key: k, refused: true, line: "Nothing to widen: no task is blocked with a widen request.", record: "no_widen_request" };
		let t = blocked[0];
		if (blocked.length > 1) {
			const labels = blocked.map((b) => `${b.id}: ${clean((b as { title?: string }).title) || blockHappened(b)}`);
			const pick = await ctx.ui.select("widen: which task", labels);
			if (!pick) return { key: k, refused: false, line: CANCELLED };
			t = blocked[labels.indexOf(pick)];
		}
		const shown = await this.verb(["task", "show", t.id, "--json"]);
		if (failed(shown)) return { key: k, refused: true, line: sentence(`${t.id}'s record could not be read`, refusal(shown)), record: `${t.id}: ${refusalRecord(shown)}` };
		const rec = data<{ task: { id?: string; title?: string; state?: string; block_reason?: string; block_detail?: string; refused_paths?: unknown } }>(shown.stdout)?.task;
		if (!rec) return { key: k, refused: true, line: `${t.id}'s record could not be read: task show returned no task.`, record: `${t.id}: task show returned no task` };
		let paths: string[];
		try {
			paths = refusedPaths(rec);
		} catch (err) {
			const why = String((err as Error).message ?? err);
			return { key: k, refused: true, line: `${t.id} has no widen request to accept: its block names no paths, or it is no longer blocked for them.`, record: `${t.id}: ${why}` };
		}
		const typed = rec.block_reason === "widen_requested";
		const reason = typed ? clean(rec.block_detail, 300) : "";
		const msg = [
			`${t.id}: ${clean(rec.title) || "(no title)"}`,
			typed ? "Its seat asks to touch these files outside its task:" : "The commit step found its seat changed these files outside its task:",
			...paths.map((p) => `  ${p}`),
			typed ? `The seat's reason: ${reason || "(none given)"}` : "",
			"Yes adds exactly these paths to the task's files; No changes nothing.",
		].filter(Boolean);
		const yes = await ctx.ui.confirm(`widen ${t.id}?`, msg.join("\n"));
		if (!yes) return { key: k, refused: false, line: "Cancelled; nothing was widened." };
		const r = await this.verb(toolArgv.taskWiden(this.cfg, { id: t.id, reason: WIDEN_REASON }, paths));
		if (failed(r)) return { key: k, refused: true, line: sentence(`${t.id} was not widened`, refusal(r)), record: `${t.id}: ${refusalRecord(r)}` };
		return { key: k, refused: false, line: `${t.id} widened by ${paths.length} ${paths.length === 1 ? "path" : "paths"}: ${paths.join(", ")}.`, record: `${t.id} widened: ${WIDEN_REASON}` };
	}
}

// gateLabel is a gate's line in a dialog and on the screen: its plain
// question's first line; for a gate raised before 3.1.1, its kind in words.
export function gateLabel(g: { id: string; action_class: string; question?: string }): string {
	const first = (g.question ?? "").split("\n").find((l) => l.trim() !== "");
	return first ?? `an owner gate (${g.action_class.replace(/_/g, " ")}) is open`;
}
