// The read-only screen (brief 31-1, section 7): the squad as the lead sees it,
// a widget below the editor of the lead's own pi. It is fed by
// `amq-squad status --json` and nothing else, refreshed by a poll keyed on
// the task store's .generation and scheduler.json (both stat'ed on a short
// timer; status runs only when either changed). `/squad` toggles it;
// collapsed, it is one footer line with the counts. The keys (brief 31-2,
// lib/keys.ts) are `/squad add`, `/squad reset`, `/squad clear` and
// `/squad answer`: pi hands a command to the extension, never to the model,
// and each key's verb runs from this process.
import * as fs from "node:fs";
import * as path from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Config, Runner } from "./hooks.ts";
import { KEYS, Keys, typedPrefix, type KeyOutcome } from "./keys.ts";
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
	warnings?: string[];
	acknowledged_warnings?: string[];
	launch: { launch_id?: string; outcome: string; reason_code: string; timing: Timing } | null;
};
type TaskRow = { id: string; task_state: string; lease_holder: string; block_reason: string; wait_reason: string; wait_holders?: string[]; pr_number: number; updated_at: string };
type GateRow = { id: string; action_class: string; target_digest: string; state: string };
type Attention = { kind: string; subject: string; gate_id: string; since: string };
type DecisionRow = { at: string; kind: string; seat: string; task_id: string; reason: string; reason_code: string };
type TrainRun = { run: string; last_kind: string; last_at: string; prs: number[]; merged: number[]; refusal: string; done: boolean };
export type ScreenStatus = {
	root: string;
	session: string;
	project_path?: string;
	scheduler?: { max_seats: number; live_seats: number } | null;
	lead_handle: string | null;
	lead: { handle: string; pid: number; pane: string; live: boolean } | null;
	counts: { needs_you: number; working: number; ready: number; inactive: number };
	seats: SeatRow[];
	tasks: TaskRow[];
	gates: GateRow[];
	attention: Attention[];
	decisions?: DecisionRow[];
	train?: { runs: TrainRun[]; alerts: { kind: string; detail: string }[] };
	warnings: string[];
};

// The decisions that stopped a seat (the seats block's refusal line) and a
// task (the tasks block's).
const SEAT_REFUSALS = new Set(["roster_launch_failed", "seat_not_up", "seat_launch_failed", "seat_prepare_failed", "seat_request_failed", "seat_refused", "exit_refused", "seat_close_failed", "ledger_tampered"]);
const TASK_REFUSALS = new Set(["commit_refused", "blocked"]);

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

// newestDecision is the newest decision of the given kinds, as its refusal
// line: the reason code (else the kind) and the detail.
// The item named is the block's: the seat for seats, the task for tasks.
function newestDecision(st: ScreenStatus, kinds: Set<string>, item: "seat" | "task", extra?: (d: DecisionRow) => boolean): string | undefined {
	const ds = st.decisions ?? [];
	for (let i = ds.length - 1; i >= 0; i--) {
		const d = ds[i];
		if (kinds.has(d.kind) || (extra && extra(d))) {
			const who = item === "seat" ? d.seat || d.task_id : d.task_id || d.seat;
			return `${d.reason_code || d.kind}: ${short([who, d.reason].filter(Boolean).join(" "))}`;
		}
	}
	return undefined;
}

function refusalLine(r: string | undefined): string {
	return `  refusal: ${r ?? "none"}`;
}

// renderScreen is the screen's lines: the lead handle and the root, then the
// four blocks (seats, tasks, train, gates), each closed by its refusal line.
export function renderScreen(st: ScreenStatus, now: Date = new Date(), key?: KeyOutcome): string[] {
	const lead = st.lead_handle ?? st.lead?.handle ?? "-";
	const leadProc = st.lead ? ` (pid ${st.lead.pid}${st.lead.pane ? `, pane ${st.lead.pane}` : ""}${st.lead.live ? "" : ", not live"})` : "";
	const out: string[] = [`amq-squad: lead ${lead}${leadProc} · root ${st.root}`];

	out.push(`seats (${st.seats.length})`);
	for (const s of st.seats) {
		const rt = [s.runtime, s.model].filter(Boolean).join("/") || "-";
		const l = s.launch;
		const budget = l ? `live ${sec(l.timing.to_seat_live_ms)}${l.timing.over_ceiling ? " OVER" : ""} first-msg ${sec(l.timing.to_first_message_ms)}` : "";
		out.push(`  ${s.handle}  ${rt}  ${s.state}/${s.reason_code}  ${l ? l.outcome : "no launch"}  ${budget}`.trimEnd());
		for (const w of s.warnings ?? []) out.push(`    ! ${short(w)}`);
	}
	const sch = st.scheduler;
	if (sch && sch.max_seats > 0 && sch.live_seats > sch.max_seats) {
		// brief 31-2 PR 2: more roster seats up than the lease cap (a seat
		// added past max_seats); a warning, never a refusal of the add
		out.push(`  ! over the lease cap: ${sch.live_seats} seats up, max_seats ${sch.max_seats}: a seat beyond the cap waits for a lease (seat_cap) while the others hold theirs`);
	}
	out.push(refusalLine(newestDecision(st, SEAT_REFUSALS, "seat", (d) => d.kind === "scheduler_stopped" && d.reason !== "")));

	out.push(`tasks (${st.tasks.length})`);
	for (const t of st.tasks) {
		const pr = t.pr_number ? `PR #${t.pr_number}` : "PR -";
		let why = t.task_state === "blocked" && t.block_reason ? `  ${t.block_reason}` : "";
		if (t.task_state === "pending" && t.wait_reason === "file_lock") {
			// the tasks holding its file lock; none left means a stale wait
			const holders = t.wait_holders ?? [];
			why = holders.length ? `  waiting on ${holders.join(", ")}` : "  waiting on a file lock no task holds (stale)";
		}
		out.push(`  ${t.id}  ${t.task_state}  ${t.lease_holder || "-"}  ${pr}${why}`);
	}
	let taskRefusal = newestDecision(st, TASK_REFUSALS, "task");
	if (!taskRefusal) {
		const blocked = st.tasks.filter((t) => t.task_state === "blocked" && t.block_reason).sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
		if (blocked.length) taskRefusal = `${blocked[0].block_reason}: ${blocked[0].id}`;
	}
	out.push(refusalLine(taskRefusal));

	const runs = st.train?.runs ?? [];
	const current = runs.find((r) => !r.done);
	const last = runs.find((r) => r.done);
	out.push("train");
	const runLine = (label: string, r: TrainRun) =>
		`  ${label}: ${r.run} ${r.last_kind} ${age(r.last_at, now)} ago, PRs ${r.prs.length ? r.prs.map((n) => "#" + n).join(",") : "-"}, merged ${r.merged.length}`;
	out.push(current ? runLine("current", current) : "  current: none");
	out.push(last ? runLine("last", last) : "  last: none");
	for (const a of st.train?.alerts ?? []) out.push(`    ! ${a.kind}: ${short(a.detail)}`);
	const trainRefusal = [current, last].find((r) => r && r.refusal);
	out.push(refusalLine(trainRefusal ? short(trainRefusal.refusal) : undefined));

	const open = st.gates.filter((g) => g.state === "open");
	out.push(`gates (${open.length} open)`);
	for (const g of open) {
		const since = st.attention.find((a) => a.kind === "gate" && a.gate_id === g.id)?.since ?? "";
		// the 16-hex typed prefix (R32); the terminal door is not built (Q7 of
		// brief 31-2), so an open typed gate waits on Buzz
		out.push(`  ${g.id}  ${g.action_class}  ${typedPrefix(g.target_digest)}  age ${age(since, now)}  waiting on Buzz`);
	}
	const gateAlert = [...st.attention].reverse().find((a) => a.gate_id && a.kind !== "gate");
	const gateWarn = st.warnings.find((w) => w.startsWith("gate store:"));
	out.push(refusalLine(gateAlert ? `${gateAlert.kind}: ${short(gateAlert.subject)}` : gateWarn ? short(gateWarn) : undefined));
	out.push(`keys: ${KEYS.map((k) => "/squad " + k).join(" | ")}`);
	if (key) out.push(`  key ${key.key}${key.refused ? " refused" : ""}: ${short(key.line, 200)}`);
	return out;
}

// countsLine is the collapsed screen: one footer line with the counts.
export function countsLine(st: ScreenStatus): string {
	const c = st.counts;
	const open = st.gates.filter((g) => g.state === "open").length;
	return `squad ${st.session}: ${st.seats.length} seats, ${st.tasks.length} tasks; ${c.needs_you} needs you, ${c.working} working, ${c.ready} ready, ${c.inactive} inactive; ${open} gates open (/squad)`;
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

	constructor(cfg: Config, runner: Runner = run, stat: Stat = defaultStat) {
		this.cfg = cfg;
		this.runner = runner;
		this.stat = stat;
		this.keys = new Keys(cfg, runner);
	}

	// press runs one key and shows its outcome; then the screen reads status
	// again whatever the poll key says, so what the key changed shows now.
	async press(name: string, ctx: ExtensionContext): Promise<KeyOutcome> {
		this.ctx = ctx;
		if (!this.last) await this.poll(); // a key pressed before the first read
		const outcome = await this.keys.press(name, ctx, this.last);
		this.lastKey = outcome;
		if (!this.expanded) {
			try {
				ctx.ui.notify(`key ${outcome.key}${outcome.refused ? " refused" : ""}: ${outcome.line}`, outcome.refused ? "warning" : "info");
			} catch {
				// UI is optional
			}
		}
		this.pollKeySeen = undefined;
		await this.poll();
		this.render();
		return outcome;
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

	render(): void {
		const ui = this.ctx?.hasUI ? this.ctx.ui : undefined;
		if (!ui) return;
		try {
			if (!this.last) {
				ui.setWidget(SCREEN_KEY, this.expanded ? [`amq-squad: ${this.error || "reading status"}`] : undefined, { placement: "belowEditor" });
				return;
			}
			if (this.expanded) {
				const lines = renderScreen(this.last, new Date(), this.lastKey);
				if (this.error) lines.push(`  (stale: ${this.error})`);
				ui.setWidget(SCREEN_KEY, lines, { placement: "belowEditor" });
				ui.setStatus(SCREEN_KEY, undefined);
			} else {
				ui.setWidget(SCREEN_KEY, undefined);
				ui.setStatus(SCREEN_KEY, countsLine(this.last));
			}
		} catch {
			// UI is optional (json and print modes)
		}
	}

	toggle(): void {
		this.expanded = !this.expanded;
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
// when pi has a UI, toggled by /squad, stopped at shutdown.
export function installScreen(pi: ExtensionAPI, cfg: Config, runner: Runner = run, stat: Stat = defaultStat): Screen {
	const screen = new Screen(cfg, runner, stat);
	pi.on("session_start", (_e, ctx) => {
		if (ctx.hasUI) screen.start(ctx);
	});
	pi.on("session_shutdown", () => {
		screen.stop();
	});
	pi.registerCommand("squad", {
		description: "The squad screen: /squad shows or hides it; /squad add|reset|clear|answer presses a key",
		getArgumentCompletions: (prefix: string) => KEYS.filter((k) => k.startsWith(prefix.trim())).map((k) => ({ value: k, label: k })),
		handler: async (args, ctx) => {
			screen.ctx = ctx;
			if (!args.trim()) {
				screen.toggle();
				return;
			}
			await screen.press(args, ctx);
		},
	});
	return screen;
}
