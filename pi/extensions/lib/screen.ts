// The team screen (brief 31-1, section 7; brief 3.1.1-3): the squad as the
// lead sees it, a widget below the editor of the lead's own pi, on by
// default. It is fed by `amq-squad status --json` and nothing else (and,
// for a block whose words read the refusal's recorded numbers, the task's
// own `task show --json`), refreshed by a poll keyed on the task store's
// .generation and scheduler.json (both stat'ed on a short timer; status
// runs only when either changed). Each seat line says what the seat is
// doing, each task line why it waits or what stopped it and what to do;
// the last line lists the commands available now. `/team hide` collapses
// it to one footer line with the counts and `/team` shows it again; the
// actions are their own commands (lib/commands.ts, lib/keys.ts): pi hands
// a command to the extension, never to the model, and each action's verb
// runs from this process.
import * as fs from "node:fs";
import * as path from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Config, Runner } from "./hooks.ts";
import { commandsLine } from "./commands.ts";
import { gateLabel, Keys, typedPrefix, type KeyOutcome } from "./keys.ts";
import { blockNeedsRecord, blockWords, clean, decisionKind, decisionWords, resolved, RULE, requestWords, seatDoing, trainRunWords, trainStop, waitWords, warningWords, type BlockDetail } from "./words.ts";
import { run } from "./verbs.ts";

export const SCREEN_KEY = "amq-squad-screen";
export const POLL_MS = 1000;

// The parts of the status contract (docs/status-contract.md) the screen reads.
type Segment = { name: string; ms: number | null };
type Timing = {
	to_seat_live_ms: number | null;
	to_first_message_ms: number | null;
	ceiling_ms: number;
	over_ceiling: boolean;
	slowest_segment: string;
	segments: Segment[];
};
type SeatRow = {
	handle: string;
	runtime: string;
	model: string;
	effort?: string;
	state: string;
	reason_code: string;
	task_id?: string;
	warnings?: string[];
	acknowledged_warnings?: string[];
	launch: { launch_id?: string; outcome: string; reason_code: string; timing: Timing } | null;
};
// wait_pool: for a task waiting no_roster_seat, the pool its route names
// and no roster seat serves (brief 3.1.1-2)
export type TaskRow = { id: string; title?: string; task_state: string; lease_holder: string; lease_expires_at?: string | null; budget?: number; budget_ends_at?: string | null; block_reason: string; wait_reason: string; wait_holders?: string[]; waits_for?: { task_id: string; path: string }[]; wait_pool?: string; depends_on?: string[]; pr_number: number; updated_at: string };
// question is the gate question's plain lines, the text above its rule, as
// the owner reads it on the phone (brief 3.1.1-1); "" for a gate raised
// before 3.1.1.
type GateRow = { id: string; action_class: string; target_digest: string; state: string; question?: string };
type Attention = { kind: string; subject: string; gate_id: string; since: string };
type DecisionRow = { at: string; kind: string; seat: string; task_id: string; reason: string; reason_code: string; stage?: string };
// brief 31-4: a phone request of the lead reply relay
type RequestRow = { request_id: string; thread: string; lead: string; turns: number; posts: number; relayed_at: string | null; refused: string[]; closed: boolean; updated_at: string };
type TrainRun = { run: string; last_kind: string; last_at: string; prs: number[]; merged: number[]; refusal: string; done: boolean };
export type ScreenStatus = {
	root: string;
	session: string;
	project_path?: string;
	scheduler?: { max_seats: number; live_seats: number; usage_limit_until?: string | null } | null;
	lead_handle: string | null;
	lead: { handle: string; pid: number; pane: string; live: boolean } | null;
	counts: { needs_you: number; working: number; ready: number; inactive: number };
	// seats is the running roster; stale_records (an earlier session's or
	// launch's handles under the root) are never seat lines (brief 3.1.1-2)
	seats: SeatRow[];
	stale_records?: { handle: string; session: string; launch_id: string }[];
	tasks: TaskRow[];
	gates: GateRow[];
	attention: Attention[];
	decisions?: DecisionRow[];
	train?: { runs: TrainRun[]; alerts: { kind: string; detail: string }[] };
	requests?: RequestRow[];
	warnings: string[];
};

// Which decisions the seats block shows is words.ts decisionKind's: a stop,
// or a kind no table knows.

// The block reasons whose words read the task's own record (`task show
// --json`'s block_detail) are words.ts blockNeedsRecord's: the size cap's
// counts and caps, and a reason no table maps (a launch failure's AMQ code
// is marked in its detail).

function sec(ms: number | null | undefined): string {
	return typeof ms === "number" ? `${(ms / 1000).toFixed(1)}s` : "-";
}

function short(s: string, n = 120): string {
	const one = (s ?? "").replace(/\s+/g, " ").trim();
	return one.length > n ? one.slice(0, n - 3) + "..." : one;
}

function age(since: string, now: Date): string {
	const t = Date.parse(since);
	if (!Number.isFinite(t)) return "-";
	const s = Math.max(0, Math.floor((now.getTime() - t) / 1000));
	if (s < 120) return `${s}s`;
	if (s < 7200) return `${Math.floor(s / 60)}m`;
	return `${Math.floor(s / 3600)}h`;
}

function timeLeft(until: string | null | undefined, now: Date): string {
	const left = msLeft(until, now);
	if (left === null) return "";
	const s = Math.max(0, Math.ceil(left / 1000));
	if (s < 120) return `${s}s`;
	if (s < 7200) return `${Math.ceil(s / 60)}m`;
	return `${Math.ceil(s / 3600)}h`;
}

function msLeft(until: string | null | undefined, now: Date): number | null {
	const t = Date.parse(until ?? "");
	return Number.isFinite(t) ? Math.max(0, t - now.getTime()) : null;
}

function taskClock(t: TaskRow, now: Date): string {
	if (t.task_state !== "in_progress") return "";
	const leaseMs = msLeft(t.lease_expires_at, now);
	const budgetMs = msLeft(t.budget_ends_at, now);
	// A normal ten-minute lease and a distant budget add noise. Once the
	// lease has two minutes left, or the budget is one lease away, show both
	// recorded clocks so the lead can decide whether to act.
	if ((leaseMs === null || leaseMs > 2 * 60_000) && (budgetMs === null || budgetMs > 10 * 60_000)) return "";
	const lease = timeLeft(t.lease_expires_at, now);
	const budget = timeLeft(t.budget_ends_at, now);
	return [lease && `its lease has ${lease} left`, budget && `its budget has ${budget} left`].filter(Boolean).join("; ");
}

// split is a Go text's plain part (above its rule) and the rest.
function split(text: string): [string, string] {
	const at = (text ?? "").indexOf(`\n\n${RULE}\n`);
	return at < 0 ? [text ?? "", ""] : [text.slice(0, at), text.slice(at + RULE.length + 3)];
}

// launchTiming is the seat's launch budget at the end of its line, short
// (brief 3.1.1-3: the timing moves behind the doing words).
function launchTiming(l: SeatRow["launch"]): string {
	if (!l) return "";
	const t = l.timing;
	const live = typeof t.to_seat_live_ms === "number" ? `up in ${sec(t.to_seat_live_ms)}${t.over_ceiling ? ` (over the ${t.ceiling_ms / 1000}s ceiling)` : ""}` : "";
	const first = typeof t.to_first_message_ms === "number" ? `first message ${sec(t.to_first_message_ms)}` : "";
	const parts = [live, first].filter(Boolean);
	return parts.length ? `  · ${parts.join(", ")}` : "";
}

// newestSeatStop is the newest decision that stopped a seat: what happened
// and what to do (decisionWords) and its record (the kind, the seat and
// the reason, verbatim).
function newestSeatStop(st: ScreenStatus): { line: string; record: string } | undefined {
	const ds = st.decisions ?? [];
	for (let i = ds.length - 1; i >= 0; i--) {
		const d = ds[i];
		// a stop, or a kind no table knows (never hidden); a clean scheduler
		// stop (no reason) is routine
		const k = decisionKind(d.kind);
		if (k === "routine" || (d.kind === "scheduler_stopped" && !d.reason)) continue;
		// a stop a later decision resolved (words.ts RESOLVED_BY) is cleared
		if (resolved(ds, i)) continue;
		const who = d.seat || d.task_id;
		const code = k === "unknown" ? d.kind : d.reason_code || d.kind;
		return { line: decisionWords(d), record: `${code}: ${short([who, d.reason].filter(Boolean).join(" "))}` };
	}
	return undefined;
}

// taskLine is one task in words: in progress on its seat, waiting and on
// what, blocked with what happened and what to do, held, done or cancelled.
// launchCodes are the codes the decision log recorded for failed seat
// launches (seat_launch_failed, and a launch failure's block): a wait or
// block reason among them reads as a launch failure, AMQ's codes included.
function launchCodes(st: ScreenStatus): Set<string> {
	const out = new Set<string>();
	for (const d of st.decisions ?? []) {
		if (d.kind !== "seat_launch_failed" && !(d.kind === "blocked" && d.stage)) continue;
		for (const c of [d.reason_code, d.stage, d.kind === "blocked" ? d.reason : ""]) if (c) out.add(c);
	}
	return out;
}

function taskLine(t: TaskRow, st: ScreenStatus, d: BlockDetail | undefined, now: Date): { line: string; record?: string } {
	const pr = t.pr_number ? `  PR #${t.pr_number}` : "";
	const title = clean(t.title);
	switch (t.task_state) {
		case "in_progress":
			return { line: `${t.id} in progress on ${t.lease_holder || "a seat"}${title ? `: ${title}` : ""}${pr}${taskClock(t, now) ? `  · ${taskClock(t, now)}` : ""}` };
		case "pending":
			return waitWords(t, { max_seats: st.scheduler?.max_seats, usage_limit_until: st.scheduler?.usage_limit_until ?? null, launch: launchCodes(st) });
		case "blocked":
			return blockWords(t, d ?? {}, { launch: launchCodes(st) });
		case "completed":
			return { line: `${t.id} done${title ? `: ${title}` : ""}${pr}` };
		case "cancelled":
			return { line: `${t.id} cancelled${title ? `: ${title}` : ""}` };
	}
	return { line: `${t.id}: its state cannot be read`, record: `${t.id}: task_state ${clean(t.task_state, 40) || "empty"}` };
}

function taskRank(t: TaskRow): number {
	switch (t.task_state) {
		case "blocked": return 0;
		case "in_progress": return 1;
		case "pending": return 2;
		case "completed":
		case "cancelled": return 4;
		default: return 3;
	}
}

// taskRows orders actionable work before history and applies the task-area
// height. Closed work is folded only after every open row has a place; it
// can therefore never displace a blocked, running or pending task.
function taskRows(tasks: TaskRow[], maxRows?: number, rowCost: (task: TaskRow) => number = () => 1): { tasks: TaskRow[]; tail?: string } {
	const ordered = tasks.map((task, index) => ({ task, index }))
		.sort((a, b) => taskRank(a.task) - taskRank(b.task) || a.index - b.index)
		.map(({ task }) => task);
	if (maxRows === undefined || !Number.isFinite(maxRows)) return { tasks: ordered };
	const cap = Math.max(0, Math.floor(maxRows));
	if (cap === 0) return { tasks: [] };
	const open = ordered.filter((t) => t.task_state !== "completed" && t.task_state !== "cancelled");
	const closed = ordered.filter((t) => t.task_state === "completed" || t.task_state === "cancelled");
	const cost = (t: TaskRow) => Math.max(1, Math.floor(rowCost(t)));
	const openCost = open.reduce((n, t) => n + cost(t), 0);
	if (openCost > cap) {
		const shown: TaskRow[] = [];
		let room = cap - 1; // an open overflow always names how much is hidden
		for (const t of open) {
			const n = cost(t);
			if (n > room) break;
			shown.push(t);
			room -= n;
		}
		return { tasks: shown, tail: `+${tasks.length - shown.length} more (/team for all)` };
	}
	const room = cap - openCost;
	const closedCost = closed.reduce((n, t) => n + cost(t), 0);
	if (closedCost <= room) return { tasks: [...open, ...closed] };
	if (room > 0) return { tasks: open, tail: `${closed.length} closed` };
	return { tasks: open };
}

// renderScreen is the screen's lines: the lead handle and the root, then the
// blocks (seats, tasks, train, waiting on you, phone requests), each with
// one plain line when empty; then the last action's outcome line; then the
// rule and, below it, the codes, ids and digests for the record; and last,
// the commands available now (brief 3.1.1-3 item 2).
export function renderScreen(st: ScreenStatus, now: Date = new Date(), key?: KeyOutcome, details: Record<string, BlockDetail> = {}, maxTaskRows?: number): string[] {
	const lead = st.lead_handle ?? st.lead?.handle ?? "-";
	const leadProc = st.lead ? ` (pid ${st.lead.pid}${st.lead.pane ? `, pane ${st.lead.pane}` : ""}${st.lead.live ? "" : ", not live"})` : "";
	const out: string[] = [`amq-squad: lead ${lead}${leadProc} · root ${st.root}`];
	const record: string[] = [];
	const rec = (r: string | undefined) => {
		if (r) record.push(r);
	};

	out.push(`seats (${st.seats.length})`);
	if (!st.seats.length) out.push("  No seat is running yet.");
	for (const s of st.seats) {
		const rt = [s.runtime, s.model, s.effort].filter(Boolean).join(" ") || "-";
		const held = s.task_id ? st.tasks.find((t) => t.id === s.task_id) : undefined;
		const doing = seatDoing(s, held);
		rec(doing.record);
		const clock = held ? taskClock(held, now) : "";
		out.push(`  ${s.handle}  ${rt}  ${doing.text}${clock ? `  · ${clock}` : ""}${launchTiming(s.launch)}`);
		// brief 3.1.1-3 (the plan reviewer): each warning in words above the
		// rule, its code and detail verbatim below it
		for (const w of s.warnings ?? []) {
			const ww = warningWords(s.handle, w, s);
			out.push(`    ! ${short(ww.line, 200)}`);
			rec(short(ww.record, 200));
		}
	}
	const sch = st.scheduler;
	if (sch && sch.max_seats > 0 && sch.live_seats > sch.max_seats) {
		// brief 31-2 PR 2: more roster seats up than the lease cap (a seat
		// added past max_seats); a warning, never a refusal of the add
		out.push(`  ! ${sch.live_seats} seats are up over the cap of ${sch.max_seats}: a seat beyond the cap waits for a lease while the others hold theirs`);
	}
	const seatStop = newestSeatStop(st);
	if (seatStop) {
		out.push(`  ${seatStop.line}`);
		rec(seatStop.record);
	}

	out.push(`tasks (${st.tasks.length})`);
	if (!st.tasks.length) out.push("  No tasks yet; add one with /add.");
	const visibleTasks = taskRows(st.tasks, maxTaskRows, (t) => taskLine(t, st, details[t.id], now).record ? 2 : 1);
	for (const t of visibleTasks.tasks) {
		const l = taskLine(t, st, details[t.id], now);
		out.push(`  ${l.line}`);
		rec(l.record);
	}
	if (visibleTasks.tail) out.push(`  ${visibleTasks.tail}`);

	const runs = st.train?.runs ?? [];
	const current = runs.find((r) => !r.done);
	const last = runs.find((r) => r.done);
	out.push("train");
	// the run's last step in words; the run id (its tail is hex) and the
	// journal's kind go below the rule
	const runLine = (label: string, r: TrainRun) =>
		`  ${label}: ${trainRunWords(r.last_kind)}, ${age(r.last_at, now)} ago; PRs ${r.prs.length ? r.prs.map((n) => "#" + n).join(", ") : "none"}; ${r.merged.length} merged`;
	if (!current && !last) out.push("  No merge train has run yet.");
	else {
		out.push(current ? runLine("running", current) : "  running: none");
		out.push(last ? runLine("last run", last) : "  last run: none");
		for (const [label, r] of [["running", current], ["last run", last]] as const) if (r) rec(`train ${label}: ${r.run} ${r.last_kind}`);
	}
	for (const a of st.train?.alerts ?? []) {
		// the alert's plain part (above its rule); its kind and the rest go
		// below the screen's rule (the gate store alert's path and error)
		const [plain, below] = split(a.detail);
		out.push(`    ! ${short(plain)}`);
		rec(`${a.kind}${below ? ": " + short(below, 200) : ""}`);
	}
	const trainRefusal = [current, last].find((r) => r && r.refusal);
	if (trainRefusal) {
		out.push(`  ${short(trainStop(trainRefusal.refusal), 200)}`);
		rec(`train ${trainRefusal.run}: ${short(trainRefusal.refusal, 200)}`);
	}

	const open = st.gates.filter((g) => g.state === "open");
	out.push(`waiting on you (${open.length})`);
	if (!open.length) out.push("  Nothing is waiting on you.");
	for (const g of open) {
		const since = st.attention.find((a) => a.kind === "gate" && a.gate_id === g.id)?.since ?? "";
		// brief 3.1.1-1, 3.1.1-3: the question's first plain line as the
		// phone shows it; the gate id, the action and the digest prefix go
		// below the rule. The terminal door is not built (Q7 of brief 31-2),
		// so an open typed gate waits on Buzz.
		out.push(`  ${gateLabel(g)}  (answer on the phone; /answer shows it)`);
		rec(`${g.id}  ${g.action_class}  ${typedPrefix(g.target_digest)}  age ${age(since, now)}  waiting on Buzz`);
	}
	const gateAlert = [...st.attention].reverse().find((a) => a.gate_id && a.kind !== "gate");
	const gateWarn = st.warnings.find((w) => w.startsWith("gate store:"));
	if (gateAlert) {
		out.push("  ! a gate needs a look; its alert is below the line");
		rec(`${gateAlert.kind}: ${short(gateAlert.subject)}`);
	} else if (gateWarn) {
		out.push("  ! the gate store could not be read; its error is below the line");
		rec(short(gateWarn, 200));
	}
	// brief 31-4: the phone requests, newest first, each marked relayed (the
	// lead's answer reached the owner's DM) or refused by the relay
	const reqs = st.requests ?? [];
	if (reqs.length) {
		out.push(`phone requests (${reqs.length})`);
		for (const r of reqs.slice(0, 5)) {
			// the request in words above the rule; its id and the relay's
			// refusal names below it, as the 3.1.0 row read
			const mark = r.relayed_at ? `relayed ${age(r.relayed_at, now)} ago` : "not relayed";
			out.push(`  ${requestWords(r, r.relayed_at ? age(r.relayed_at, now) : "")}`);
			rec(`${r.request_id}  ${r.lead || "-"}  ${mark}${r.refused.length ? `  refused: ${[...new Set(r.refused)].join(", ")}` : ""}`);
		}
	}
	if (key) {
		out.push(`  /${key.key}: ${short(key.line, 240)}`);
		if (key.record) rec(`/${key.key}: ${short(key.record, 240)}`);
	}
	if (record.length) out.push(RULE, ...record.map((r) => `  ${r}`));
	out.push(commandsLine("team"));
	return out;
}

// countsLine is the collapsed screen: one footer line with the counts.
export function countsLine(st: ScreenStatus): string {
	const c = st.counts;
	const open = st.gates.filter((g) => g.state === "open").length;
	return `squad ${st.session}: ${st.seats.length} seats, ${st.tasks.length} tasks; ${c.needs_you} needs you, ${c.working} working, ${c.ready} ready, ${c.inactive} inactive; ${open} gates open (/team shows the screen)`;
}

// pollKey is the poll's key: the stat of the task store's .generation and of
// scheduler.json (whose updated_at the scheduler rewrites every tick).
export type Stat = (p: string) => { mtimeMs: number; size: number } | undefined;

export function defaultStat(p: string): { mtimeMs: number; size: number } | undefined {
	try {
		const s = fs.statSync(p);
		return { mtimeMs: s.mtimeMs, size: s.size };
	} catch {
		return undefined;
	}
}

export function pollPaths(root: string): string[] {
	return [path.join(root, "extensions", "amq-squad", "tasks", ".generation"), path.join(root, "extensions", "amq-squad", "scheduler.json")];
}

export function pollKey(root: string, stat: Stat): string {
	return pollPaths(root)
		.map((p) => {
			const s = stat(p);
			return s ? `${s.mtimeMs}:${s.size}` : "-";
		})
		.join("|");
}

// Screen is the poller and the renderer of one lead's screen.
export class Screen {
	cfg: Config;
	runner: Runner;
	stat: Stat;
	ctx: ExtensionContext | undefined;
	expanded = true;
	last: ScreenStatus | undefined;
	pollKeySeen: string | undefined;
	statusRuns = 0;
	busy = false;
	timer: ReturnType<typeof setInterval> | undefined;
	error = "";
	keys: Keys;
	lastKey: KeyOutcome | undefined;
	// details are the blocked tasks' own records the words read (the size
	// cap's numbers), by id, kept while the task's updated_at is unchanged
	details: Record<string, BlockDetail & { at: string }> = {};

	constructor(cfg: Config, runner: Runner = run, stat: Stat = defaultStat) {
		this.cfg = cfg;
		this.runner = runner;
		this.stat = stat;
		this.keys = new Keys(cfg, runner);
	}

	// press runs one key and shows its outcome; then the screen reads status
	// again whatever the poll key says, so what the key changed shows now.
	async press(name: string, ctx: ExtensionContext, arg = ""): Promise<KeyOutcome> {
		this.ctx = ctx;
		if (!this.last) await this.poll(); // a key pressed before the first read
		const outcome = await this.keys.press(name, ctx, this.last, arg);
		this.show(outcome, ctx);
		this.pollKeySeen = undefined;
		await this.poll();
		this.render();
		return outcome;
	}

	// show leaves an outcome on the screen; a collapsed screen says it in a
	// notice.
	show(outcome: KeyOutcome, ctx: ExtensionContext): void {
		this.lastKey = outcome;
		if (!this.expanded) {
			try {
				ctx.ui.notify(`/${outcome.key}: ${outcome.line}`, outcome.refused ? "warning" : "info");
			} catch {
				// UI is optional
			}
		}
		this.render();
	}

	// poll stats both keys and runs status --json only when either changed.
	async poll(): Promise<void> {
		if (this.busy) return;
		const key = pollKey(this.cfg.root, this.stat);
		if (key === this.pollKeySeen) return;
		this.busy = true;
		try {
			this.statusRuns++;
			const r = await this.runner(this.cfg.bin, ["status", "--root", this.cfg.root, "--json"], { timeoutMs: Math.max(this.cfg.hookTimeoutMs, 30000) });
			if (r.code === 0) {
				this.last = JSON.parse(r.stdout) as ScreenStatus;
				this.pollKeySeen = key;
				this.error = "";
				await this.readDetails(this.last);
			} else {
				this.error = `status --json failed: exit ${r.code}${r.stderr.trim() ? ": " + r.stderr.trim().split("\n")[0] : ""}`;
			}
		} catch (err) {
			this.error = `status --json unreadable: ${String(err)}`;
		} finally {
			this.busy = false;
		}
		this.render();
	}

	// readDetails reads `task show --json` for each blocked task whose words
	// need its record (blockNeedsRecord), once per change of the task.
	async readDetails(st: ScreenStatus): Promise<void> {
		for (const t of st.tasks) {
			if (t.task_state !== "blocked" || !blockNeedsRecord(t.block_reason) || this.details[t.id]?.at === t.updated_at) continue;
			const r = await this.runner(this.cfg.bin, ["task", "show", t.id, "--json"], { timeoutMs: Math.max(this.cfg.hookTimeoutMs, 30000) });
			let detail = "";
			try {
				if (r.code === 0) detail = String((JSON.parse(r.stdout) as { data?: { task?: { block_detail?: string } } }).data?.task?.block_detail ?? "");
			} catch {
				// no numbers: the words say only that the cap was passed
			}
			this.details[t.id] = { at: t.updated_at, block_detail: detail };
		}
	}

	render(): void {
		const ui = this.ctx?.hasUI ? this.ctx.ui : undefined;
		if (!ui) return;
		try {
			if (!this.last) {
				ui.setWidget(SCREEN_KEY, this.expanded ? [`amq-squad: ${this.error || "reading the team's status"}`, commandsLine("team")] : undefined, { placement: "belowEditor" });
				return;
			}
			if (this.expanded) {
				const status = this.last;
				ui.setWidget(SCREEN_KEY, (tui) => ({
					render: () => {
						const now = new Date();
						const probe = renderScreen(status, now, this.lastKey, this.details, 0);
						// The zero-row probe is every fixed line above and below the task
						// area. Task height also pays for each task's record below the rule.
						const fixed = probe.length + (this.error ? 1 : 0);
						const taskHeight = Math.max(0, tui.terminal.rows - fixed);
						const lines = renderScreen(status, now, this.lastKey, this.details, taskHeight);
						if (this.error) lines.splice(lines.length - 1, 0, `  (the screen may be stale: ${this.error})`);
						return lines;
					},
				}), { placement: "belowEditor" });
				ui.setStatus(SCREEN_KEY, undefined);
			} else {
				ui.setWidget(SCREEN_KEY, undefined);
				ui.setStatus(SCREEN_KEY, countsLine(this.last));
			}
		} catch {
			// UI is optional (json and print modes)
		}
	}

	// setExpanded shows the screen (/team) or collapses it to its footer
	// line (/team hide).
	setExpanded(on: boolean): void {
		this.expanded = on;
		this.render();
	}

	start(ctx: ExtensionContext, intervalMs = POLL_MS): void {
		this.ctx = ctx;
		if (this.timer) return;
		void this.poll();
		this.timer = setInterval(() => void this.poll(), intervalMs);
		this.timer.unref?.();
	}

	stop(): void {
		if (this.timer) clearInterval(this.timer);
		this.timer = undefined;
	}
}

// installScreen gives the team lead the screen: started at session start
// when pi has a UI, stopped at shutdown. Its commands are lib/commands.ts's.
export function installScreen(pi: ExtensionAPI, cfg: Config, runner: Runner = run, stat: Stat = defaultStat, startNow = true): Screen {
	const screen = new Screen(cfg, runner, stat);
	pi.on("session_start", (_e, ctx) => {
		screen.ctx = ctx;
		if (startNow && ctx.hasUI) screen.start(ctx);
	});
	pi.on("session_shutdown", () => {
		screen.stop();
	});
	return screen;
}
