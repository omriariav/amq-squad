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

// own is a table's entry for a key, never an inherited property (Codex on
// ed3d1e65: a reason named "constructor" or "__proto__" found Object's own
// and broke the render); every table lookup goes through it.
export function own<T>(table: Record<string, T>, key: string | undefined): T | undefined {
	return key !== undefined && Object.hasOwn(table, key) ? table[key] : undefined;
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
// launch holds the codes the decision log recorded for failed seat launches
// (seat_launch_failed's reason code and stage): a wait or block reason
// among them is a launch failure even when it is AMQ's own code.
export type WaitContext = { max_seats?: number; usage_limit_until?: string | null; launch?: Set<string> };

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
	const f = own(WAIT_WORDS, r);
	if (f) return { line: `${t.id} waiting: ${f(t, c)}` };
	// a launch failure's code (internal/scheduler launchfail.go waitForSeat)
	const l = own(LAUNCH_WORDS, r);
	if (l) return { line: `${t.id} waiting: its seat's launch failed, ${l}; ${LAUNCH_RETRY}` };
	if (c.launch?.has(r)) return { line: `${t.id} waiting: its seat's launch failed (AMQ's reason is below the line); ${LAUNCH_RETRY}`, record: `${t.id} waits: ${clean(r, 120)}` };
	return { line: `${t.id} waiting`, record: `${t.id} waits: ${clean(r, 120)}` };
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
	evidence_ref_symbolic: () => ["its evidence ref is a symbolic link", "ask the owner to inspect the evidence refs"],
	// the store's re-pin refusal (task lease.go) and the router's open
	// refusal (routing open.go)
	repin_after_attempt: () => ["its pool cannot change after its first attempt", "reset it on its own pool"],
	routing_invalid: () => ["the routing table could not be read", "ask the owner to fix the routing table"],
	// two deferrals of the step (commit.go), mapped so a block that ever
	// carries one still reads in words
	commit_cancelled: () => ["the task was cancelled while its commit step ran", "nothing to do unless it should run again (/reset)"],
	pr_pending: () => ["the commit step is waiting for its PR to open", "wait, or /reset it if it stays"],
	widen_snapshot_missing: () => ["the widening found no snapshot of the stopped attempt", RESET],
	widen_snapshot_attempt_mismatch: () => ["the widening's snapshot is of another attempt", RESET],
	push_target_unacknowledged: () => ["the push target is not the one the owner's grant names now", "ask the owner to acknowledge the push target in the grant"],
	ledger_tampered: () => ["the commits ledger does not verify", "ask the owner to inspect the commits ledger before anything commits"],
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
	task_branch_unprepared: () => ["its seat could not be put on the task's branch", RESET],
	retries_exhausted: () => ["every retry failed", "/reset it onto another pool"],
	class_unknown: () => ["its class is not in the routing table", "add it again with a known class"],
	declared_tree_unchecked: () => ["its declared files could not be checked", RESET],
	pin_unresolved: () => ["the pool it is pinned to cannot run its class", "/reset it onto another pool"],
	history_tampered: () => ["the routing history does not verify", "ask the owner to inspect it"],
};

const LAUNCH_RETRY = "the scheduler tries once more, then blocks it for the lead";
const LAUNCH_FIX = "fix the cause, then /reset it (or relaunch the roster)";

// LAUNCH_WORDS maps each code amq-squad's own launch path can give a failed
// seat launch (launchfail.go's LaunchFailure.Code(): the reason code, else
// the stage), which becomes a pending task's wait reason and, on a repeat,
// its block reason: the stages (internal/seatlaunch), the launcher's own
// codes and seat states, seat-exec's refusals (internal/seatexec), the
// proof server's (seatlaunch proof_unix.go), the Claude and pi checks it
// runs (internal/claudeseat, internal/piruntime) and the scheduler's roster
// codes. words.test.ts scans those sources for them. AMQ's own reason codes
// (launchapi types them as a plain string, an open set) are recognized as
// launch failures through the decision log (WaitContext.launch).
export const LAUNCH_WORDS: Record<string, string> = {
	// stages
	launch_error: "the launcher could not start it",
	apply_failed: "AMQ's apply failed",
	prepare_refused: "AMQ refused to prepare it",
	prepared: "it stopped after it was prepared",
	seat_not_up: "the seat did not come up",
	decisions_pending: "AMQ waits for a decision before it launches",
	resource_present: "a gone seat's pane is still open",
	// the launcher's codes and seat states
	apply_ambiguous: "AMQ saw more than one pane for it",
	not_observed: "the launch reported nothing for it",
	no_proof: "it never proved it started",
	not_started: "it did not start",
	pending: "it had not proved it started when the launch ended",
	refused: "its seat-exec refused it",
	// the scheduler's roster codes
	seat_replacement_resource_present: "a gone seat's pane is still open, so no seat can replace it",
	binding_present_without_resumable_conversation: "it could not resume its conversation",
	// seat-exec's refusals
	argv_missing: "its command line is missing",
	codex_approval_missing: "its Codex approval mode is missing",
	codex_sandbox_missing: "its Codex sandbox is missing",
	dir_writable_by_others: "a directory on its path is writable by others",
	env_needs_invalid: "its env needs are not valid",
	identity_mismatch: "its identity is not the launch's",
	identity_proof_failed: "it could not prove its identity",
	identity_proof_timeout: "it ran out of time proving its identity",
	identity_incomplete: "its AMQ identity is incomplete",
	mkdir_failed: "a directory for it could not be made",
	path_invalid: "a path it needs is not valid",
	proof_expired: "its proof came after its token expired",
	proof_ticket_consumed: "its proof ticket was already used",
	proof_ticket_missing: "it has no proof ticket",
	proof_unacknowledged: "the launcher did not take its proof",
	proof_refused: "the launcher refused its proof",
	provider_missing: "its provider's program is not installed",
	record_invalid: "its launch record does not read",
	record_missing: "its launch record is missing",
	role_mismatch: "its role is not the launch's",
	role_missing: "its role file is missing",
	mailbox_missing: "its mailbox is missing",
	root_invalid: "its root is not valid",
	root_unreadable: "its root cannot be read",
	rules_mismatch: "its team rules are not the launch's",
	rules_missing: "its team rules are missing",
	runtime_mismatch: "its runtime is not the launch's",
	seat_invalid: "its seat entry is not valid",
	stat_failed: "a file it needs cannot be read",
	symlink_in_path: "a link sits on its path",
	write_failed: "a file for it could not be written",
	wrong_owner: "a file it needs has the wrong owner",
	sandbox_pins_missing: "its Codex sandbox pins are missing",
	// the Claude and pi checks
	claude_hooks_missing: "its Claude command skips the hooks",
	claude_permission_mismatch: "its Claude permission mode is not the recorded one",
	claude_token_insecure: "its Claude token file is not private",
	pi_not_embedded: "this amq-squad carries no pi runtime",
	pi_runtime_rejected: "this amq-squad's pi runtime was rejected",
	runtime_mismatch_in_process: "its pi runtime is not the one this process runs",
	ledger_unreadable: "its pi session ledger does not read",
	pi_resume_context_lost: "its pi session lost its context",
	pi_session_id_invalid: "its pi session id is not valid",
	pi_session_id_missing: "it has no pi session id",
	pi_session_unreadable: "its pi session does not read",
	// the proof server's
	identity_forged: "a forged proof was sent for it",
	proof_malformed: "its proof was malformed",
	proof_origin_unknown: "its proof's sender could not be read",
	proof_origin: "its proof came from another process",
	proof_token_invalid: "its proof carried another seat's token",
	proof_unknown: "the launch has no ticket for it",
	proof_connections_full: "the proof server was full",
	proof_ledger_failed: "its proof could not be recorded",
	proof_marker_failed: "its proof marker could not be written",
	proof_replayed: "its proof was replayed",
};

// HOLD is the lead's hold (`task block ID --reason hold --route lead`).
export const HOLD = "hold";

// blockWords is a blocked task's line: "<id> blocked: <what happened>;
// <what to do>", a hold "<id> held by the lead", an unknown reason (a
// seat's own free-text block, an AMQ launch code) "<id> blocked" with its
// code for the record.
export function blockWords(t: TaskWords, d: BlockDetail = {}, c: WaitContext = {}): { line: string; record?: string } {
	const r = t.block_reason ?? "";
	if (r === HOLD) return { line: `${t.id} held by the lead` };
	const f = own(BLOCK_WORDS, r);
	if (f) {
		const [happened, todo] = f(d);
		return { line: `${t.id} blocked: ${happened}; ${todo}` };
	}
	// a repeated launch failure's code (launchfail.go blockOnLaunchFailure)
	const l = own(LAUNCH_WORDS, r);
	if (l) return { line: `${t.id} blocked: its seat's launch failed twice, ${l}; ${LAUNCH_FIX}` };
	if (c.launch?.has(r)) return { line: `${t.id} blocked: its seat's launch failed twice (AMQ's reason is below the line); ${LAUNCH_FIX}`, record: `${t.id} blocked: ${clean(r, 120)}` };
	return { line: `${t.id} blocked; its reason is below the line`, record: `${t.id} blocked: ${clean(r, 120) || "no reason recorded"}` };
}

// blockHappened is a block's first half alone (a dialog label).
export function blockHappened(t: TaskWords, d: BlockDetail = {}): string {
	const r = t.block_reason ?? "";
	if (r === HOLD) return "held by the lead";
	const f = own(BLOCK_WORDS, r);
	if (f) return f(d)[0];
	const l = own(LAUNCH_WORDS, r);
	return l ? `its seat's launch failed, ${l}` : "blocked";
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
	const f = own(SEAT_DOING, s.reason_code);
	if (!f) return { text: "waiting", record: `${s.handle}: ${s.reason_code || "no reason code"}` };
	return { text: f(s, t) };
}

// DECISION_WORDS maps the scheduler's decisions that stopped a seat (the
// seats block's stop line) to what happened and, when the decision's
// reason has no words of its own, what to do.
export const DECISION_WORDS: Record<string, (who: string) => [string, string]> = {
	roster_launch_failed: () => ["the roster launch failed", "ask the lead to relaunch the roster"],
	seat_not_up: (w) => [`seat ${w} did not come up`, "ask the lead to relaunch the roster"],
	seat_launch_failed: (w) => [`seat ${w}'s launch failed`, "ask the lead to relaunch the roster"],
	seat_prepare_failed: (w) => [`seat ${w} could not be prepared`, "ask the lead to relaunch the roster"],
	seat_request_failed: (w) => [`a seat could not be requested${w ? ` for ${w}` : ""}`, "check the task's pool, or /reset it onto another"],
	seat_refused: (w) => [`seat ${w} was refused`, "ask the lead why; the record is below the line"],
	exit_refused: (w) => [`seat ${w} did not stop cleanly`, "ask the owner to inspect it before the seat is relaunched"],
	seat_close_failed: (w) => [`seat ${w} could not be closed`, "close its pane by hand"],
	ledger_tampered: () => ["the ledger does not verify", "ask the owner to inspect it"],
	scheduler_stopped: () => ["the scheduler stopped", "start the team again with amq-squad up"],
};

// decisionWords is a stop decision's line: "<what happened>: <the reason's
// words>; <what to do>" when the decision's reason code (its reason_code,
// else its reason's first word) is in the block table, else "<what
// happened>; <the decision's own what to do>".
export function decisionWords(d: { kind: string; seat?: string; task_id?: string; reason?: string; reason_code?: string }): string {
	const who = d.seat || d.task_id || "";
	const [happened, todo] = own(DECISION_WORDS, d.kind)?.(who) ?? ["a seat stopped", "the record is below the line"];
	const code = d.reason_code || (d.reason ?? "").trim().split(/[\s:]+/)[0] || "";
	const f = own(BLOCK_WORDS, code);
	if (!f) return `${happened}; ${todo}`;
	const [why, then] = f({});
	return `${happened}: ${why}; ${then}`;
}

// The parts of a status seat row a warning's words read: its launch
// timing (to seat live, the ceiling, the slowest segment).
export type WarningSeat = { launch?: { timing?: { to_seat_live_ms?: number | null; ceiling_ms?: number; slowest_segment?: string } } | null };

// SEGMENT_WORDS names a launch segment (status-contract timing.segments).
const SEGMENT_WORDS: Record<string, string> = {
	drain: "the old roster's drain",
	prepare: "preparing the seat",
	mint: "minting its proof",
	apply: "AMQ's apply",
	seat_exec: "the seat's own start",
};

const DISMISS = "and /dismiss hides it once you know";

// WARNING_WORDS maps each seat warning code status shows (Go's
// schedproc.ClearableWarnings, the list words.test.ts reads) to what the
// warning says and what to do, from the warning's recorded fields: the
// seat's launch timing, and the numbers and names in its detail.
export const WARNING_WORDS: Record<string, (detail: string, s: WarningSeat) => [string, string]> = {
	launch_budget_exceeded: (_d, s) => {
		const t = s.launch?.timing;
		const up = typeof t?.to_seat_live_ms === "number" ? `it came up in ${(t.to_seat_live_ms / 1000).toFixed(1)}s` : "it came up";
		const ceiling = t?.ceiling_ms ? `, over the ${t.ceiling_ms / 1000}s ceiling` : ", over the launch ceiling";
		const slow = t?.slowest_segment ? ` (slowest step: ${own(SEGMENT_WORDS, t.slowest_segment) ?? "one step"})` : "";
		return [`${up}${ceiling}${slow}`, `nothing to do unless it repeats, ${DISMISS}`];
	},
	wake_off: () => ["AMQ does not ring it when mail arrives, so it idles after each turn", `ring it by hand, ${DISMISS}`],
	seat_stalled_at_dialog: (d) => {
		const m = /open since \S+ \(([0-9hms.]+)\)/.exec(d);
		return [`it is stopped at a harness dialog${m ? ` open for ${m[1]}` : ""}`, "answer the dialog in its pane"];
	},
	pi_provider_key_missing: (d) => {
		const m = /^model (\S+) needs/.exec(d);
		return [`its model ${m ? m[1] + " " : ""}has no provider key, so it stalls at its first turn`, "add the key to its env needs or its pi login, then relaunch"];
	},
	claude_hooks_silent: () => ["it has run with no hook report since its launch", "check that its Claude hooks are installed"],
	claude_allow_rules_present: () => ["its worktree's Claude settings carry allow rules", "remove the allow rules from the worktree's settings"],
	claude_external_includes_pending: () => ["a CLAUDE.md it loads imports a file outside its worktree", "approve or remove that import"],
};

// warningCode is a warning line's code: the text before its first colon.
export function warningCode(w: string): string {
	return w.split(":")[0].trim();
}

// warningWords is a seat warning above the rule, "<what>; <what to do>",
// and its record below it (the code and the detail verbatim); an unmapped
// code is "warning; its code is below the line".
export function warningWords(handle: string, w: string, s: WarningSeat): { line: string; record: string } {
	const code = warningCode(w);
	const f = own(WARNING_WORDS, code);
	const detail = w.slice(w.indexOf(":") + 1).trim();
	const record = `${handle}: ${w}`;
	if (!f) return { line: "warning; its code is below the line", record };
	const [what, todo] = f(detail, s);
	return { line: `${what}; ${todo}`, record };
}

// TRAIN_RULE_WORDS maps each merge train rule (internal/mergetrain
// checks.go Rule*) to why the train stopped at a PR and what to do.
export const TRAIN_RULE_WORDS: Record<string, [string, string]> = {
	repository: ["its repository is not one the grant permits", "ask the owner to widen the grant"],
	base_branch: ["it targets a base branch the grant does not permit", "retarget the PR"],
	ci_green: ["its CI is not green", "fix the failing check and the train tries again"],
	size_cap: ["it is over the size cap", "split it or ask the owner to raise the cap"],
	force_push: ["its branch was force-pushed", "ask the owner, whose gate a force push needs"],
	protected_path: ["it changes a protected file", "the owner answers its gate on the phone"],
	task_scope: ["it changes files outside its task", "widen the task or split the PR"],
	pr_state: ["the PR is not open or not mergeable", "check the PR"],
	task_unbound: ["no task is bound to it", "bind it through its task"],
	pr_head_moved: ["its head moved after the check", "nothing to do; the train checks it again"],
	history_incomplete: ["its push history could not be read whole", "the train tries again; ask the owner if it repeats"],
};

// trainStop is the train's stop line from a run's refusal ("refusal PR #N
// RULE[,RULE]: detail", internal/status screen.go): "the merge train
// stopped at PR #N: <why>; <what to do>" for its first rule; a refusal of
// another form, or an unmapped rule, says only where it stopped.
export function trainStop(refusal: string): string {
	const m = /^refusal PR #(\d+) ([a-z_,]+):/.exec(refusal ?? "");
	if (!m) return "the merge train stopped; its refusal is below the line";
	const w = own(TRAIN_RULE_WORDS, m[2].split(",")[0]);
	if (!w) return `the merge train stopped at PR #${m[1]}; its rule is below the line`;
	return `the merge train stopped at PR #${m[1]}: ${w[0]}; ${w[1]}`;
}

// RELAY_WORDS maps the relay's refusal names of a phone request
// (status-contract requests[].refused) to words.
export const RELAY_WORDS: Record<string, string> = {
	relay_refused_gate_shape: "the relay held back a reply shaped like a gate answer",
	relay_refused_gate_thread: "the relay held back a reply into a gate question's thread",
	relay_post_unconfirmed: "the relay could not confirm a post",
	relay_record_invalid: "the relay's record of it does not read",
	relay_turn_undelivered: "the owner's turn did not reach the lead",
	relay_rollback_failed: "the relay could not undo a failed post",
	relay_record_failed: "the relay could not write its record",
};

// requestWords is a phone request's plain line: to whom, whether the
// lead's answer reached the phone, and what the relay refused, in words.
export function requestWords(r: { lead: string; relayed_at: string | null; refused: string[] }, relayedAgo: string): string {
	const to = r.lead ? `a phone request to ${r.lead}` : "a phone request";
	const mark = r.relayed_at ? `answered ${relayedAgo} ago` : "not answered yet";
	const refused = [...new Set(r.refused)].map((x) => own(RELAY_WORDS, x) ?? "the relay refused a reply");
	return `${to}, ${mark}${refused.length ? `: ${[...new Set(refused)].join("; ")}; the lead answers again in words` : ""}`;
}

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
