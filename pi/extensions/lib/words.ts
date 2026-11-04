// The lead's screen in plain words (brief 3.1.1-3): what each seat is doing,
// why a task waits or is blocked and what to do about it, and the refusal
// and outcome lines, each composed from a field of the status document (or
// a task's own record) and never guessed (the 3.1.1-1 rule). ONE table maps
// every wait and block reason the scheduler and the task store can emit to
// its words (pi/test/words.test.ts reads Go's reason constants and fails on
// any reason the table does not map); a reason the table does not know at
// run time falls back to "waiting" or "blocked", and its code goes below the
// rule for the record.
import { redactFact } from "./firstturn.ts";

// RULE is the line between a text's plain lines and its identifiers (the
// gate question's gatestore.Rule): what happened and what to do above it,
// the codes, ids and digests below it.
export const RULE = "----------";

// TITLE_MAX is the cut of a free-text title on a seat or task line.
export const TITLE_MAX = 60;

// clean is a free-text value from the model or a seat (a task title, a
// seat's reason) as a screen line may carry it: redacted (Go's status
// already passed it through redact.Block; this is the extension's own second
// layer), control characters stripped, whitespace folded, then cut.
export function clean(s: string | undefined, max = TITLE_MAX): string {
	const one = redactFact(String(s ?? ""))
		.replace(/[\u0000-\u001f\u007f-\u009f]/g, " ")
		.replace(/\s+/g, " ")
		.trim();
	return one.length > max ? one.slice(0, max - 3) + "..." : one;
}

// codeIn is the first snake_case code or hex run in a line ("" when none):
// a refusal or outcome line above its rule carries neither (row 4). A path
// or a file name (a token with a slash or a dot inside) is not a code.
const SNAKE = /^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$/;
const HEX_RUN = /(?<![0-9a-z])(?=[0-9a-f]*[0-9])(?=[0-9a-f]*[a-f])[0-9a-f]{8,}(?![0-9a-z])/i;
export function codeIn(line: string): string {
	for (const raw of line.split(/[\s,;:()"'[\]{}<>!?]+/)) {
		const tok = raw.replace(/^[.`*]+|[.`*]+$/g, "");
		if (!tok || tok.includes("/") || tok.includes(".")) continue;
		if (SNAKE.test(tok)) return tok;
	}
	const hex = HEX_RUN.exec(line);
	return hex ? hex[0] : "";
}

// The parts of a status task row the words read.
export type TaskWords = {
	id: string;
	title?: string;
	task_state: string;
	lease_holder?: string;
	wait_reason?: string;
	wait_holders?: string[];
	wait_pool?: string;
	block_reason?: string;
	depends_on?: string[];
	pr_number?: number;
};

// WaitContext is what a wait's words read beyond the task row: the
// scheduler's lease cap and usage-limit window (status's scheduler block).
export type WaitContext = { max_seats?: number; usage_limit_until?: string | null };

// The dependency-merge wait (internal/task DependencyMergeWait, #859):
// status composes it in words from the recorded id and PR number, and the
// screen shows it verbatim.
const DEPENDENCY_MERGE = /^waiting for \S+'s PR #\d+ to merge$/;

// WAIT_WORDS maps each wait reason of a pending task (internal/task
// class.go's Wait* and the scheduler's no_roster_seat) to its words, the
// text after "<id> waiting: ".
export const WAIT_WORDS: Record<string, (t: TaskWords, c: WaitContext) => string> = {
	"": () => "queued for the next free seat",
	dependencies: (t) => `for ${(t.depends_on ?? []).join(", ") || "the tasks it depends on"} to finish`,
	file_lock: (t) => ((t.wait_holders ?? []).length ? `${(t.wait_holders ?? []).join(", ")} hold its files` : "for a file lock no task holds any more (a stale wait)"),
	no_route: () => "no pool can run it; the lead assigns one",
	seat_starting: () => "a seat for it is starting",
	seat_cap: (_t, c) => (c.max_seats ? `every seat is busy and the cap of ${c.max_seats} seats is reached` : "every seat is busy and the seat cap is reached"),
	usage_limit: (_t, c) => `a usage limit stops new seats${c.usage_limit_until ? ` until ${c.usage_limit_until}` : ""}`,
	privacy_class: () => "its files are local-only and only vendor seats could run it",
	concurrency_cap: () => "every pool that could run it is full",
	cost_cap: () => "the cost cap stops every pool that could run it",
	reviewer_family: () => "a review needs a seat of another model family than the implementer's",
	outcome_log_unwritable: () => "the scheduler cannot write its outcome log, so nothing is dispatched",
	no_roster_seat: (t) => `no seat serves ${t.wait_pool || "its pool"}`,
};

// waitWords is a pending task's line: "<id> waiting: <words>", the
// dependency-merge wait verbatim, an unknown reason "<id> waiting" with its
// code for the record.
export function waitWords(t: TaskWords, c: WaitContext = {}): { line: string; record?: string } {
	const r = t.wait_reason ?? "";
	if (DEPENDENCY_MERGE.test(r)) return { line: `${t.id} ${r}` };
	const f = WAIT_WORDS[r];
	if (!f) return { line: `${t.id} waiting`, record: `${t.id} waits: ${r}` };
	return { line: `${t.id} waiting: ${f(t, c)}` };
}

// BlockDetail is what a block's words read from the task's own record
// (`task show --json`): the refusal's detail, where the commit step
// records its numbers (the size cap's counts and caps).
export type BlockDetail = { block_detail?: string };

// sizeCap composes the size cap's overage from the commit step's recorded
// counts (internal/seatcommit "N changed files over the cap of M", "N
// changed lines over the cap of M"), never a guess: with no detail on
// record it says only that the diff is over the cap.
export function sizeCap(detail: string | undefined): string {
	const parts: string[] = [];
	const files = /(\d+) changed files over the cap of (\d+)/.exec(detail ?? "");
	const lines = /(\d+) changed lines over the cap of (\d+)/.exec(detail ?? "");
	if (files) parts.push(`${files[1]} files over the cap of ${files[2]}`);
	if (lines) parts.push(`${lines[1]} lines over the cap of ${lines[2]}`);
	return parts.length ? parts.join(" and ") : "the diff is over the size cap";
}

const COMMIT = "the commit step refused it";
const RESET = "send it back with /reset";
const LAUNCH = "its seat's launch failed";

// BLOCK_WORDS maps each block reason the scheduler, the commit step, the
// router and the task store write (the table words.test.ts checks against
// Go's constants) to what happened and what to do.
export const BLOCK_WORDS: Record<string, (d: BlockDetail) => [string, string]> = {
	// the commit step (internal/seatcommit policy.go, commit.go, ledger.go)
	outside_declared_files: () => [`${COMMIT}, it changed files outside its task`, `accept them with /widen or ${RESET}`],
	protected_path: () => [`${COMMIT}, it changed a protected file`, `${RESET}; only the owner changes protected files`],
	link_target_refused: () => [`${COMMIT}, a changed link points somewhere refused`, RESET],
	size_cap_exceeded: (d) => [`${COMMIT}, ${sizeCap(d.block_detail)}`, "ask the owner to raise the cap or split it"],
	nested_repository: () => [`${COMMIT}, it changed a nested repository`, RESET],
	base_unrecorded: () => ["the commit step found no recorded base for its seat's branch", "relaunch the seat, then /reset it"],
	worktree_mismatch: () => ["the commit step found its seat's worktree or branch not as recorded", RESET],
	push_rejected: () => ["the remote refused the push (there is no force push)", "fix the branch, then /reset it"],
	grants_invalid: () => ["the owner's grant could not be read or names no verifier for its class", "ask the owner to fix the grant, then /reset it"],
	commit_failed: () => ["the commit step kept failing", RESET],
	pr_bound_elsewhere: () => ["an open PR on its branch belongs to another task", "close that PR, then /reset it"],
	recorded_diff_mismatch: () => ["the commits ledger and the commit disagree", "ask the owner to inspect the commits ledger"],
	lease_task_mismatch: () => ["its seat's lease is for another task", RESET],
	widen_snapshot_missing: () => ["the widening found no snapshot of the stopped attempt", RESET],
	widen_snapshot_attempt_mismatch: () => ["the widening's snapshot is of another attempt", RESET],
	push_target_unacknowledged: () => ["the push target is not the one the owner's grant names now", "ask the owner to acknowledge the push target in the grant"],
	ledger_tampered: () => ["the commits ledger does not verify", "ask the owner to inspect the commits ledger; nothing commits until then"],
	tool_pin_mismatch: () => ["a pinned tool is not the pinned version", "ask the owner to check the pin"],
	verifier_timeout: () => ["its verifier ran past its time limit", `${RESET}, or ask the owner to raise the limit`],
	verifier_output_exceeded: () => ["its verifier printed more than the limit", `${RESET}, or ask the owner to raise the limit`],
	verifier_start_failed: () => ["its verifier could not start", "ask the owner to fix the verifier command, then /reset it"],
	verifier_interrupted: () => ["its verifier was interrupted before it finished", RESET],
	verifier_cancelled: () => ["its verifier stopped when the task was cancelled", RESET],
	// the completion gate (internal/task class.go)
	verdict_missing: () => ["it finished with no passing verifier result", RESET],
	verdict_failed: () => ["its verifier failed", `${RESET} to try again`],
	verdict_tree_mismatch: () => ["its verifier ran on another tree than its seat's work", RESET],
	// the typed widen request and its growth gate (internal/task task.go)
	widen_requested: () => ["its seat asks to touch files outside its task", `accept with /widen or ${RESET}`],
	growth_gate_required: () => ["the widening needs the owner's growth gate", "the owner answers it on the phone (/answer shows it)"],
	// the scheduler (internal/scheduler hooks.go, roster.go) and the router
	// (internal/routing)
	seat_replacement_needs_relaunch: () => ["every seat of its pool is gone", "ask the lead to relaunch the roster"],
	seat_replacement_resource_present: () => ["a gone seat's pane is still open, so no seat can replace it", "close that pane, then ask the lead to relaunch"],
	binding_present_without_resumable_conversation: () => ["its seat could not resume its conversation", "ask the lead to relaunch the roster"],
	task_branch_unprepared: () => ["its seat could not be put on the task's branch", RESET],
	retries_exhausted: () => ["every retry failed", "/reset it onto another pool"],
	class_unknown: () => ["its class is not in the routing table", "add it again with a known class"],
	declared_tree_unchecked: () => ["its declared files could not be checked", RESET],
	pin_unresolved: () => ["the pool it is pinned to cannot run its class", "/reset it onto another pool"],
	history_tampered: () => ["the routing history does not verify", "ask the owner to inspect it"],
	// a repeated launch failure's stage (internal/seatlaunch run.go)
	prepare_refused: () => [`${LAUNCH} while it was prepared`, RESET],
	apply_failed: () => [LAUNCH, RESET],
	launch_error: () => [LAUNCH, RESET],
};

// HOLD is the lead's hold (`task block ID --reason hold --route lead`).
export const HOLD = "hold";

// blockWords is a blocked task's line: "<id> blocked: <what happened>;
// <what to do>", a hold "<id> held by the lead", an unknown reason (a
// seat's own free-text block, an AMQ launch code) "<id> blocked" with its
// code for the record.
export function blockWords(t: TaskWords, d: BlockDetail = {}): { line: string; record?: string } {
	const r = t.block_reason ?? "";
	if (r === HOLD) return { line: `${t.id} held by the lead` };
	const f = BLOCK_WORDS[r];
	if (!f) return { line: `${t.id} blocked; its reason is below the line`, record: `${t.id} blocked: ${clean(r, 120) || "no reason recorded"}` };
	const [happened, todo] = f(d);
	return { line: `${t.id} blocked: ${happened}; ${todo}` };
}

// blockHappened is a block's first half alone (a dialog label).
export function blockHappened(t: TaskWords, d: BlockDetail = {}): string {
	const r = t.block_reason ?? "";
	if (r === HOLD) return "held by the lead";
	const f = BLOCK_WORDS[r];
	return f ? f(d)[0] : "blocked";
}

// The parts of a status seat row the seat's words read.
export type SeatWords = { handle: string; reason_code: string; state: string; task_id?: string; launch?: { outcome: string } | null };

// SEAT_DOING maps a seat row's reason code (internal/status classifySeat)
// to what the seat is doing: working on its lease's task, starting, idle,
// waiting (and on what), or stopped. The task is the one its lease names.
export const SEAT_DOING: Record<string, (s: SeatWords, t: TaskWords | undefined) => string> = {
	lease_claimed: (s, t) => `working on ${s.task_id}${t?.title ? `: ${clean(t.title)}` : ""}`,
	lease_unclaimed: (s, t) => `starting on ${s.task_id}${t?.title ? `: ${clean(t.title)}` : ""}`,
	idle: () => "idle",
	gate_open: () => "waiting: on your answer to its gate",
	question_to_owner: () => "waiting: on your answer to its question",
	lease_expired: (s) => `stopped: its lease on ${s.task_id} expired`,
	no_live_signal: () => "stopped",
	no_signal: (s) => (s.launch && ["staged", "applied"].includes(s.launch.outcome) ? "starting" : "stopped"),
};

// seatDoing is a seat's doing words. A seat holding a blocked task waits on
// that task's block (its row's reason code is the block's reason); an
// unknown code is "waiting" with the code for the record.
export function seatDoing(s: SeatWords, t: TaskWords | undefined): { text: string; record?: string } {
	if (s.state === "blocked" && s.task_id) return { text: `waiting: ${s.task_id} is blocked (its line is below)` };
	const f = SEAT_DOING[s.reason_code];
	if (!f) return { text: "waiting", record: `${s.handle}: ${s.reason_code || "no reason code"}` };
	return { text: f(s, t) };
}

// DECISION_WORDS maps the scheduler's decisions that stopped a seat (the
// seats block's last-stop line) to their words.
export const DECISION_WORDS: Record<string, (who: string) => string> = {
	roster_launch_failed: () => "the roster launch failed",
	seat_not_up: (w) => `seat ${w} did not come up`,
	seat_launch_failed: (w) => `seat ${w}'s launch failed`,
	seat_prepare_failed: (w) => `seat ${w} could not be prepared`,
	seat_request_failed: (w) => `a seat could not be requested${w ? ` for ${w}` : ""}`,
	seat_refused: (w) => `seat ${w} was refused`,
	exit_refused: (w) => `seat ${w}'s exit was refused`,
	seat_close_failed: (w) => `seat ${w} could not be closed`,
	ledger_tampered: () => "the ledger does not verify",
	scheduler_stopped: () => "the scheduler stopped",
};

// plainDetail is a verb's refusal text with its codes taken out, for the
// line above the rule: the leading "verb: code: " segments and parenthesized
// codes go, and what is left is shown only when it carries no code or hex
// run ("" otherwise). The whole text goes below the rule.
export function plainDetail(text: string): string {
	let s = text.replace(/\s*\((?:[a-z][a-z0-9]*(?:_[a-z0-9]+)+)\)/g, "");
	for (;;) {
		const m = /^\s*([^:]{1,40}):\s+/.exec(s);
		const head = m ? m[1].trim() : "";
		if (!m || !(SNAKE.test(head) || VERB_HEADS.has(head))) break;
		s = s.slice(m[0].length);
	}
	s = s.trim();
	return s && !codeIn(s) ? s : "";
}

// The verb names a refusal starts with ("task reset: ...").
const VERB_HEADS = new Set(["task add", "task reset", "task widen", "task show", "scheduler clear", "scheduler relaunch", "gate status", "gate post", "team propose", "team apply", "error"]);

// sentence is "<what happened>: <detail>." or "<what happened>." when the
// detail has nothing plain to add.
export function sentence(happened: string, detail: string): string {
	const d = plainDetail(detail);
	return d ? `${happened}: ${d}` : `${happened}.`;
}
