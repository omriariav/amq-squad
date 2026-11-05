// Brief 3.1.1-3, rows 3, 4 and 7: the screen in plain words. One table maps
// every wait and block reason the scheduler and the task store can emit (Go's
// reason constants, read here from the Go sources at test time) and every
// seat reason code; no refusal or outcome line above its rule carries a
// snake_case code or a hex run; free-text titles pass the redaction and a
// control-character strip before the cut; each empty block shows one plain
// line.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { renderScreen, Screen, type ScreenStatus } from "../extensions/lib/screen.ts";
import { Keys, type KeyOutcome } from "../extensions/lib/keys.ts";
import { refusedPaths } from "../extensions/lib/tools.ts";
import { SoloTeam } from "../extensions/lib/team.ts";
import { decisionKind, RESOLVED_BY, ROUTINE_DECISIONS, LAUNCH_WORDS, TRAIN_KIND_WORDS, trainRunWords, BLOCK_WORDS, blockWords, clean, codeIn, DECISION_WORDS, decisionWords, RELAY_WORDS, RULE, SEAT_DOING, seatDoing, TITLE_MAX, TRAIN_RULE_WORDS, trainStop, WAIT_WORDS, waitWords, WARNING_WORDS, warningWords } from "../extensions/lib/words.ts";
import type { Config } from "../extensions/lib/hooks.ts";
import type { RunResult } from "../extensions/lib/verbs.ts";
import { fakeCtx, tmpdir } from "./fakepi.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(here, "..", "..");
const fixture = JSON.parse(fs.readFileSync(path.join(here, "status-fixture.json"), "utf8")) as ScreenStatus;
const now = new Date("2026-11-01T09:05:00Z");

function go(rel: string): string {
	return fs.readFileSync(path.join(REPO, rel), "utf8");
}

// goFiles is a Go package's non-test sources.
function goFiles(pkg: string): string[] {
	return fs
		.readdirSync(path.join(REPO, pkg))
		.filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
		.map((f) => `${pkg}/${f}`);
}

// constants is every `NAME = "value"` or `NAME Type = "value"` in a Go file
// whose name matches: typed or not, in a const block or alone.
function constants(rel: string, name: RegExp): string[] {
	const out: string[] = [];
	for (const m of go(rel).matchAll(/^\s*(?:const\s+)?(\w+)(?:\s+[\w.]+)?\s*=\s*"([a-z][a-z0-9_]*)"/gm)) if (name.test(m[1])) out.push(m[2]);
	return out;
}

// literals is every snake_case string literal in a Go file at the
// positions a pattern names (its first group).
function literals(rel: string, ...patterns: RegExp[]): string[] {
	const src = go(rel);
	return patterns.flatMap((re) => [...src.matchAll(re)].flatMap((m) => m.slice(1).filter(Boolean)));
}

// The packages that write a task's wait and block reasons: the task store,
// the commit step, the scheduler and the router (brief 3.1.1-3, Codex on
// ed3d1e65: every package file, not a list of files).
const REASON_PACKAGES = ["internal/task", "internal/seatcommit", "internal/scheduler", "internal/routing"];
// A wait or block field assigned a literal (a Route{Block: "..."}, a
// WaitReason or BlockReason, a wait or a block call with a literal reason).
const REASON_LITERALS = [/\bBlock:\s*"([a-z][a-z0-9_]*)"/g, /\b(?:WaitReason|BlockReason)\s*=\s*"([a-z][a-z0-9_]*)"/g, /\.wait\([^,()]*,\s*"([a-z][a-z0-9_]*)"/g, /\.(?:Block|BlockWithPaths|RefuseCommit|RefuseCommitPaths|Escalate|SetWait)\([^,()]*,(?:\s*[^,()]*,)?\s*"([a-z][a-z0-9_]*)"/g];

// The wait reasons: every Wait* constant of the four packages and every
// literal wait.
function goWaits(): string[] {
	return REASON_PACKAGES.flatMap(goFiles).flatMap((f) => [...constants(f, /^Wait/), ...literals(f, /\.wait\([^,()]*,\s*"([a-z][a-z0-9_]*)"/g, /\bWaitReason\s*=\s*"([a-z][a-z0-9_]*)"/g)]);
}

// The block reasons: every Reason*, Block* and Refusal* constant of the four
// packages, typed or not, every literal assigned to a block field, and the
// pin check's (internal/registry PinMismatch).
// Not block reasons, and left out by name: the widen refusals of
// task/scope.go (file_lock_conflict, self_widen: the widen verb's replies,
// never a task's block), and the scheduler's verbs and kinds that are not
// codes (RelaunchVerb's text).
const NOT_BLOCKS = new Set(["file_lock_conflict", "self_widen"]);
function goBlocks(): string[] {
	return [
		...REASON_PACKAGES.flatMap(goFiles).flatMap((f) => [...constants(f, /^(Reason|Block|Refusal)/), ...literals(f, ...REASON_LITERALS)]),
		...constants("internal/scheduler/roster.go", /^AMQBindingPresent$/),
		...constants("internal/registry/pins.go", /^PinMismatch$/),
	].filter((r) => !NOT_BLOCKS.has(r));
}

// The launch path's codes (internal/scheduler launchfail.go: a failed
// launch's Code(), its reason code else its stage, is the pool's tasks'
// wait reason and, repeated, their block reason): seat-exec's refusals, the
// proof server's, the launcher's stages, codes and seat states, the pi and
// Claude checks, and their constants. The success stages (prepared counts:
// a launch can stop there) "applied" and "up" are no failure, and "none" is
// a message's placeholder (roster.go's error text), never a code.
const LAUNCH_FILES = [
	...goFiles("internal/seatexec"),
	...goFiles("internal/seatlaunch"),
	"internal/scheduler/launchfail.go",
	"internal/scheduler/roster.go",
	"internal/piruntime/session.go",
	"internal/piruntime/refusal.go",
	"internal/piruntime/launch.go",
	"internal/claudeseat/claudeseat.go",
	"internal/claudeseat/token.go",
];
const LAUNCH_LITERALS = [
	/(?:refusef|asRefusal)\(\s*"([a-z][a-z0-9_]*)"/g,
	/ResumeRefusal\{\s*"([a-z][a-z0-9_]*)"/g,
	/\bCode:\s*"([a-z][a-z0-9_]*)"/g,
	/\b(?:forged|reject)\([^"\n]*"([a-z][a-z0-9_]*)"/g,
	/\.refused(?:,\s*[\w.]+)*\s*=\s*"([a-z][a-z0-9_]*)"/g,
	/\bStage\s*(?:=|:|==)\s*"([a-z][a-z0-9_]*)"/g,
	/\bState:\s*"([a-z][a-z0-9_]*)"/g,
	/State,\s*[\w.\[\]]*ReasonCode(?:,\s*[\w.\[\]]+)*\s*=\s*"([a-z][a-z0-9_]*)",\s*(?:"([a-z][a-z0-9_]*)")?/g,
	/return\s+"([a-z][a-z0-9_]*)",\s*"([a-z][a-z0-9_]*)"/g,
	/firstNonEmpty(?:Str)?\([^)"\n]*,\s*"([a-z][a-z0-9_]*)"\)/g,
];
const LAUNCH_CONSTS = /^(Reason|PiNotEmbedded$|SandboxPinsMissing$|IdentityIncomplete$|PermissionMismatch$|HooksMissing$|TokenInsecure$|RuntimeMismatchReason$|AMQBindingPresent$)/;
function goLaunchCodes(): string[] {
	return LAUNCH_FILES.flatMap((f) => [...literals(f, ...LAUNCH_LITERALS), ...constants(f, LAUNCH_CONSTS)]).filter((c) => !["applied", "up", "none"].includes(c));
}

// The seat warning codes status shows: Go's schedproc.ClearableWarnings,
// which a status row keeps equal to status's own list.
function goWarnings(): string[] {
	const src = go("internal/schedproc/clear.go");
	const list = src.slice(src.indexOf("var ClearableWarnings = []string{"), src.indexOf("}", src.indexOf("var ClearableWarnings")));
	return [...list.matchAll(/"([a-z_]+)"/g)].map((m) => m[1]);
}

// The merge train's rules (internal/mergetrain checks.go Rule*) and the
// relay's refusal names (internal/status relay.go, internal/buzz relay.go).
function goTrainRules(): string[] {
	return constants("internal/mergetrain/checks.go", /^Rule/);
}
// The entry kinds the merge train journals (internal/mergetrain's
// Entry{Kind: ...}): a run's last_kind in status.
function goTrainKinds(): string[] {
	return goFiles("internal/mergetrain").flatMap((f) => [...go(f).matchAll(/Entry\{[^}]*?\bKind:\s*"([a-z][a-z0-9_]*)"/g)].map((m) => m[1]));
}
function goRelayNames(): string[] {
	return [...constants("internal/status/relay.go", /^Relay/), ...constants("internal/buzz/relay.go", /^Relay/)];
}

// The seat reason codes (internal/status classifySeat).
function goSeatCodes(): string[] {
	const src = go("internal/status/classify.go");
	const fn = src.slice(src.indexOf("func classifySeat"), src.indexOf("\nfunc ", src.indexOf("func classifySeat") + 1));
	return [...fn.matchAll(/ReasonCode = State\w+, "([a-z_]+)"/g)].map((m) => m[1]);
}

// Row 7: ONE table maps every wait and block reason the scheduler and the
// task store can emit to plain words; a reason the table lacks fails here,
// by name. Reverted (a reason dropped from the table), its row fails.
test("row 7: every wait and block reason in Go's sources maps to plain words in the one table", () => {
	const waits = goWaits();
	const blocks = goBlocks();
	const seats = goSeatCodes();
	assert.ok(waits.length >= 12, `the wait reasons were not found: ${waits}`);
	assert.ok(blocks.length >= 35, `the block reasons were not found: ${blocks}`);
	assert.ok(seats.length >= 8, `the seat codes were not found: ${seats}`);
	const has = (t: object, k: string) => Object.hasOwn(t, k);
	const launch = goLaunchCodes();
	assert.ok(launch.length >= 60, `the launch codes were not found: ${launch}`);
	for (const r of waits) assert.ok(has(WAIT_WORDS, r), `wait reason ${r} has no words`);
	for (const r of blocks) assert.ok(has(BLOCK_WORDS, r) || has(LAUNCH_WORDS, r), `block reason ${r} has no words`);
	// a launch code is a wait reason and, repeated, a block reason
	for (const r of launch) assert.ok(has(LAUNCH_WORDS, r) || has(BLOCK_WORDS, r) || has(WAIT_WORDS, r), `launch code ${r} has no words`);
	for (const r of seats) assert.ok(has(SEAT_DOING, r), `seat reason code ${r} has no words`);
	const rules = goTrainRules();
	assert.ok(rules.length >= 11, `the train rules were not found: ${rules}`);
	for (const r of rules) assert.ok(has(TRAIN_RULE_WORDS, r), `train rule ${r} has no words`);
	const kinds = goTrainKinds();
	assert.ok(new Set(kinds).size >= 11, `the train's entry kinds were not found: ${kinds}`);
	for (const k of kinds) assert.ok(has(TRAIN_KIND_WORDS, k), `train entry kind ${k} has no words`);
	assert.equal(trainRunWords("made_up_kind"), "its last step is below the line");
	const relay = goRelayNames();
	assert.ok(relay.length >= 4, `the relay names were not found: ${relay}`);
	for (const r of relay) assert.ok(has(RELAY_WORDS, r), `relay refusal ${r} has no words`);
	// each mapped reason's words are plain: no code, no hex run
	const t = { id: "t9", task_state: "pending", depends_on: ["t8"], wait_holders: ["t7"], wait_pool: "claude:opus:high" };
	for (const r of Object.keys(WAIT_WORDS)) assert.equal(codeIn(waitWords({ ...t, wait_reason: r }, { max_seats: 3 }).line), "", r);
	for (const r of Object.keys(BLOCK_WORDS)) assert.equal(codeIn(blockWords({ id: "t9", task_state: "blocked", block_reason: r }).line), "", r);
	// an unmapped reason at run time: "waiting" or "blocked", the code below
	const w = waitWords({ id: "t9", task_state: "pending", wait_reason: "made_up_wait" });
	assert.deepEqual(w, { line: "t9 waiting", record: "t9 waits: made_up_wait" });
	const b = blockWords({ id: "t9", task_state: "blocked", block_reason: "needs_decision" });
	assert.deepEqual(b, { line: "t9 blocked; its reason is below the line", record: "t9 blocked: needs_decision" });
	assert.deepEqual(seatDoing({ handle: "dev", state: "online", reason_code: "made_up_code" }, undefined), { text: "waiting", record: "dev: made_up_code" });
	// the lead's hold
	assert.equal(blockWords({ id: "t9", task_state: "blocked", block_reason: "hold" }).line, "t9 held by the lead");
});

// Row 7 (Codex on ed3d1e65): a failed seat launch's code is the pool's
// tasks' wait reason (launchfail.go waitForSeat) and, repeated, their block
// reason; each of amq-squad's own launch codes reads in words, and AMQ's
// own code, which the decision log records for the failed launch, reads as
// a launch failure with the code below the rule.
test("row 7: a launch failure's code as a wait or block reason reads in words", () => {
	const t = { ...fixture.tasks[2], id: "t9", wait_holders: [] };
	const wait = (r: string) => renderScreen({ ...fixture, tasks: [{ ...t, wait_reason: r }] }, now).find((l) => l.startsWith("  t9 "));
	assert.equal(wait("apply_failed"), "  t9 waiting: its seat's launch failed, AMQ's apply failed; the scheduler tries once more, then blocks it for the lead");
	assert.equal(wait("launch_error"), "  t9 waiting: its seat's launch failed, the launcher could not start it; the scheduler tries once more, then blocks it for the lead");
	const amq = [{ at: "2026-11-01T09:00:00Z", kind: "seat_launch_failed", seat: "dev", task_id: "t9", reason: "x", reason_code: "version_probe_failed", stage: "apply_failed" }];
	const lines = renderScreen({ ...fixture, tasks: [{ ...t, wait_reason: "version_probe_failed" }], decisions: amq }, now);
	assert.ok(lines.includes("  t9 waiting: its seat's launch failed (AMQ's reason is below the line); the scheduler tries once more, then blocks it for the lead"), lines.join("\n"));
	assert.ok(lines.slice(lines.indexOf(RULE)).includes("  t9 waits: version_probe_failed"));
	const blocked = renderScreen({ ...fixture, tasks: [{ ...fixture.tasks[0], id: "t9", block_reason: "proof_expired" }] }, now).find((l) => l.startsWith("  t9 "));
	assert.equal(blocked, "  t9 blocked: its seat's launch failed twice, its proof came after its token expired; fix the cause, then /reset it (or relaunch the roster)");
});

// The decision kinds Go writes to the decision log: every Decision{Kind:
// ...} in the non-test files of its three writers (internal/scheduler, the
// scheduler's own; internal/schedproc, the scheduler process's;
// internal/cli, which appends through schedproc.AppendDecision), a literal
// or a constant of those packages, and up.go's stopped(KIND, ...) helper's
// call sites. An identifier the scan cannot resolve fails, except that
// helper's own parameter.
function goDecisionKinds(): { kinds: string[]; unresolved: string[] } {
	const pkgs = ["internal/scheduler", "internal/schedproc", "internal/cli"];
	const consts = new Map<string, string>();
	for (const f of pkgs.flatMap(goFiles)) for (const m of go(f).matchAll(/^\s*(?:const\s+)?(\w+)(?:\s+[\w.]+)?\s*=\s*"([a-z][a-z0-9_]*)"/gm)) consts.set(m[1], m[2]);
	const kinds: string[] = [];
	const unresolved: string[] = [];
	for (const f of pkgs.flatMap(goFiles)) {
		const src = go(f);
		for (const k of decisionKindFields(src)) {
			if (k.startsWith('"')) kinds.push(k.slice(1, -1));
			else if (consts.has(k.split(".").pop()!)) kinds.push(consts.get(k.split(".").pop()!)!);
			else if (!(f === "internal/cli/up.go" && k === "kind")) unresolved.push(`${f}: Kind: ${k}`);
		}
		for (const m of src.matchAll(/\bstopped\(\s*([A-Za-z_]\w*|"[a-z][a-z0-9_]*")/g)) {
			const k = m[1];
			if (k.startsWith('"')) kinds.push(k.slice(1, -1));
			else if (consts.has(k)) kinds.push(consts.get(k)!);
			else unresolved.push(`${f}: stopped(${k})`);
		}
	}
	return { kinds: [...new Set(kinds)].sort(), unresolved };
}

// goDecisionFields is, per decision kind, whether any of its literals in
// Go records a Seat and a TaskID.
function goDecisionFields(): Map<string, { seat: boolean; task: boolean }> {
	const pkgs = ["internal/scheduler", "internal/schedproc", "internal/cli"];
	const consts = new Map<string, string>();
	for (const f of pkgs.flatMap(goFiles)) for (const m of go(f).matchAll(/^\s*(?:const\s+)?(\w+)(?:\s+[\w.]+)?\s*=\s*"([a-z][a-z0-9_]*)"/gm)) consts.set(m[1], m[2]);
	const out = new Map<string, { seat: boolean; task: boolean }>();
	for (const f of pkgs.flatMap(goFiles)) {
		for (const d of decisionLiterals(go(f))) {
			const kind = d.kind.startsWith('"') ? d.kind.slice(1, -1) : consts.get(d.kind.split(".").pop()!);
			if (!kind) continue;
			const was = out.get(kind) ?? { seat: false, task: false };
			out.set(kind, { seat: was.seat || d.seat, task: was.task || d.task });
		}
	}
	return out;
}

// decisionKindFields is the value of each Decision{...} literal's own Kind
// field in Go source (Codex on #866: a non-greedy match stopped at a brace
// inside a string): it walks each literal with the brace depth, skipping
// interpreted and raw strings, runes and comments, and reads `Kind:` at
// the literal's own depth only (a nested struct's Kind is not the
// decision's).
function decisionKindFields(src: string): string[] {
	return decisionLiterals(src).map((d) => d.kind);
}

// decisionLiterals is each Decision{...} literal's own Kind and whether it
// records a Seat and a TaskID (at the literal's own depth).
function decisionLiterals(src: string): { kind: string; seat: boolean; task: boolean }[] {
	const out: { kind: string; seat: boolean; task: boolean }[] = [];
	for (const m of src.matchAll(/\bDecision\{/g)) {
		let depth = 1;
		let i = m.index! + m[0].length;
		let kind = "";
		let seat = false;
		let task = false;
		while (i < src.length && depth > 0) {
			const c = src[i];
			if (c === '"' || c === "'") {
				i++;
				while (i < src.length && src[i] !== c) i += src[i] === "\\" ? 2 : 1;
				i++;
			} else if (c === "`") {
				i = src.indexOf("`", i + 1) + 1 || src.length;
			} else if (src.startsWith("//", i)) {
				i = src.indexOf("\n", i) + 1 || src.length;
			} else if (src.startsWith("/*", i)) {
				i = src.indexOf("*/", i) + 2 || src.length;
			} else if (c === "{") {
				depth++;
				i++;
			} else if (c === "}") {
				depth--;
				i++;
			} else {
				const own = depth === 1 && /[^\w.]/.test(src[i - 1] ?? " ");
				const k = own ? /^Kind:\s*("[^"\n]*"|[A-Za-z_][A-Za-z0-9_.]*)/.exec(src.slice(i, i + 200)) : null;
				if (own && src.startsWith("Seat:", i)) seat = true;
				if (own && src.startsWith("TaskID:", i)) task = true;
				if (k) {
					kind = k[1];
					i += k[0].length;
				} else i++;
			}
		}
		if (kind) out.push({ kind, seat, task });
	}
	return out;
}

// Codex on #866: the scan finds a decision's Kind behind a brace inside a
// string, a raw string or a comment, and not a nested struct's Kind.
test("row 7: the decision scan reads Kind past braces in strings, and only the decision's own", () => {
	const fixture = [
		'log(Decision{Reason: fmt.Sprintf("{}"), Kind: "new_failed"})',
		"log(Decision{Reason: `a {raw} string`, Kind: \"raw_failed\"})",
		"log(Decision{\n\t// a } comment\n\tReason: \"}\", Seat: h,\n\tKind: KindFromConst,\n})",
		'log(Decision{Pool: Pool{Kind: "not_a_decision"}, Kind: "outer_kind", Reason: \'}\' + "x"})',
	].join("\n");
	assert.deepEqual(decisionKindFields(fixture), ['"new_failed"', '"raw_failed"', "KindFromConst", '"outer_kind"']);
});

// The plan reviewer's ruling on Codex's last finding on #863: every decision
// kind Go writes is a stop with words (DECISION_WORDS) or a routine record
// (ROUTINE_DECISIONS); a new kind in neither fails here, by name, so a new
// stop decision is never hidden from the screen with every row passing.
test("row 7: every decision kind Go writes is a stop with words or a routine record", () => {
	const { kinds, unresolved } = goDecisionKinds();
	assert.deepEqual(unresolved, [], "a decision kind the scan cannot resolve");
	assert.ok(kinds.length >= 68, `the decision kinds were not found: ${kinds}`);
	assert.ok(kinds.includes("legacy_merge_head_missing") && kinds.includes("seat_launch_failed") && kinds.includes("up_stopped_lead"), kinds.join(" "));
	for (const k of kinds) assert.notEqual(decisionKind(k), "unknown", `decision kind ${k} is neither a stop with words nor routine`);
	for (const k of Object.keys(ROUTINE_DECISIONS)) assert.ok(!Object.hasOwn(DECISION_WORDS, k), `${k} is both a stop and routine`);
	// Codex on #866: each sentence names every field Go records for its kind
	// (the scan's Seat and TaskID of each literal, read from Go), and a
	// task's id never stands in a seat's place
	const recorded = goDecisionFields();
	for (const k of Object.keys(DECISION_WORDS)) {
		const f = recorded.get(k) ?? { seat: false, task: false };
		const l = decisionWords({ kind: k, seat: f.seat ? "seatX" : undefined, task_id: f.task ? "taskY" : undefined });
		if (f.seat) assert.ok(l.includes("seatX"), `${k} records a seat its sentence drops: ${l}`);
		if (f.task) assert.ok(l.includes("taskY"), `${k} records a task its sentence drops: ${l}`);
	}
	for (const k of Object.keys(DECISION_WORDS)) {
		const l = decisionWords({ kind: k, task_id: "t9" });
		assert.doesNotMatch(l, /seat t9/, `${k} names the task as a seat: ${l}`);
		assert.doesNotMatch(decisionWords({ kind: k, seat: "dev" }), /undefined/, k);
	}
	assert.equal(decisionWords({ kind: "seat_request_expired", task_id: "t9" }), "the seat request for t9 expired before a seat came up; ask the lead to relaunch the roster");
	assert.equal(decisionWords({ kind: "doorbell_failed", seat: "dev", task_id: "t9" }), "seat dev could not be rung for t9; ring it by hand");
	// every stop's words are plain and say what to do
	for (const k of Object.keys(DECISION_WORDS)) {
		const l = decisionWords({ kind: k, seat: "dev", reason: "x" });
		assert.equal(codeIn(l), "", `${k}: ${l}`);
		assert.ok(l.includes("; "), `${k} says no what to do: ${l}`);
	}
});

// The ruling, the run-time half: the seats block never hides a decision
// kind it does not know. The newest such decision shows "the scheduler
// stopped something" (a kind whose name reads as a failure) or "the
// scheduler recorded something", its kind below the rule; a routine record
// newer than a stop does not hide the stop; each stop kind shows its words.
test("row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop", () => {
	const at = "2026-11-01T09:04:00Z";
	const dec = (kind: string, reason = "x") => ({ at, kind, seat: "dev", task_id: "", reason, reason_code: "" });
	const seats = (decisions: any[]) => {
		const lines = renderScreen({ ...fixture, decisions }, now);
		return { lines, block: lines.slice(lines.findIndex((l) => l.startsWith("seats (")), lines.findIndex((l) => l.startsWith("tasks ("))), rec: lines.slice(lines.indexOf(RULE)) };
	};
	const failed = seats([dec("exit_refused", "ledger_tampered at gone"), dec("made_up_failed")]);
	assert.ok(failed.block.includes("  the scheduler stopped something; its kind is below the line"), failed.block.join("\n"));
	assert.ok(failed.rec.includes("  made_up_failed: dev x"), failed.rec.join("\n"));
	const noted = seats([dec("made_up_note")]);
	assert.ok(noted.block.includes("  the scheduler recorded something; its kind is below the line"), noted.block.join("\n"));
	assert.ok(noted.rec.includes("  made_up_note: dev x"), noted.rec.join("\n"));
	// a routine record newer than a stop hides nothing
	const routine = seats([dec("exit_refused", "ledger_tampered at gone"), dec("dispatched"), dec("lease_renewed")]);
	assert.ok(routine.block.some((l) => l.startsWith("  seat dev did not stop cleanly")), routine.block.join("\n"));
	// each stop kind, the newest decision, shows its words
	for (const k of Object.keys(DECISION_WORDS)) {
		const one = seats([dec(k, "a reason")]);
		assert.ok(one.block.includes(`  ${decisionWords(dec(k, "a reason"))}`), `${k}:\n${one.block.join("\n")}`);
	}
});

// The plan reviewer on #866: what clears a shown stop. A later decision that
// resolves it: the same seat's seat_up (or a roster launch) for a seat's
// stop, a roster launch or reload for a roster's, the scheduler starting
// again for a scheduler's; another seat's seat_up resolves nothing. Other
// stops (a tick error, a refused exit) clear only when they leave the status
// document's last 30 decisions: /dismiss acts on seat warnings, not on
// decisions.
test("row 7: a stop followed by its resolving decision no longer shows", () => {
	const at = "2026-11-01T09:04:00Z";
	const dec = (kind: string, seat = "dev", reason = "x") => ({ at, kind, seat, task_id: "", reason, reason_code: "" });
	const stopLine = (decisions: any[]) => {
		const lines = renderScreen({ ...fixture, decisions }, now);
		const block = lines.slice(lines.findIndex((l) => l.startsWith("seats (")), lines.findIndex((l) => l.startsWith("tasks (")));
		return block.filter((l) => /^ {2}\S/.test(l) && !/^ {2}(qa|scribe|pi-dev) /.test(l));
	};
	// a seat's stop, then that seat's seat_up: cleared; another seat's: not
	assert.deepEqual(stopLine([dec("seat_gone"), dec("seat_up")]), []);
	assert.deepEqual(stopLine([dec("seat_gone"), dec("seat_up", "qa")]), ["  seat dev's process is gone; ask the lead to relaunch the roster"]);
	// a roster launch resolves any seat's stop and the roster's
	assert.deepEqual(stopLine([dec("seat_not_up"), dec("roster_incomplete", ""), dec("roster_launched", "")]), []);
	// the scheduler starting again resolves its stop
	assert.deepEqual(stopLine([dec("scheduler_stopped", "", "lead_gone: x"), dec("scheduler_started", "")]), []);
	// a cleared newest stop leaves an older unresolved one showing
	assert.deepEqual(stopLine([dec("exit_refused", "old", "ledger_tampered at gone"), dec("seat_gone"), dec("seat_up")]), ["  seat old did not stop cleanly: the commits ledger does not verify; ask the owner to inspect the commits ledger before anything commits"]);
	// Codex's sequence on #866: the seat gone, then up, then a roster launch
	assert.deepEqual(stopLine([dec("seat_gone"), dec("seat_up"), dec("roster_launched", "")]), []);
	// a request's stop records the task: that task dispatched resolves it
	const req = (kind: string, task: string) => ({ at, kind, seat: "", task_id: task, reason: "x", reason_code: "" });
	assert.deepEqual(stopLine([req("seat_request_expired", "t9"), req("dispatched", "t9")]), []);
	assert.deepEqual(stopLine([req("seat_request_expired", "t9"), req("dispatched", "t8")]), ["  the seat request for t9 expired before a seat came up; ask the lead to relaunch the roster"]);
	// a tick error has no resolving record: it shows until it leaves the window
	assert.deepEqual(stopLine([dec("tick_error", ""), dec("dispatched"), dec("seat_up")]), ["  a scheduler tick failed; nothing to do unless it repeats; the next tick retries"]);
	for (const [k, r] of Object.entries(RESOLVED_BY)) {
		assert.ok(Object.hasOwn(DECISION_WORDS, k), `${k} resolves but is no stop`);
		for (const x of r.kinds) assert.equal(decisionKind(x), "routine", `${k}'s resolver ${x} is not a routine record`);
	}
});

// Row 7 (Codex on b4df9475): a task still blocked on a launch failure's
// AMQ code reads the launch guidance from its own record (task show's
// block_detail, which blockOnLaunchFailure writes and which lasts as long as
// the block), not from the decision log's tail, which status cuts to its last
// 30 decisions: with no decision at all, the line still names the launch and
// its stage. The Go format the words read is launchfail.go's own.
test("row 7: a launch failure's block reads its guidance from the task's own record", async () => {
	assert.ok(go("internal/scheduler/launchfail.go").includes('"the launch of seat %s (pool %s) failed %d times in a row with reason %s at stage %s: %s; not retried'), "blockOnLaunchFailure's detail changed form");
	const doc = { ...fixture, decisions: [], tasks: [{ ...fixture.tasks[0], id: "t9", block_reason: "version_probe_failed", updated_at: "2026-11-01T09:04:00Z" }] };
	const calls: string[][] = [];
	const runner = async (_b: string, args: string[]): Promise<RunResult> => {
		calls.push(args);
		const detail = "the launch of seat rev-1 (pool codex:gpt-5.6-sol:high) failed 2 times in a row with reason version_probe_failed at stage apply_failed: AMQ refused; not retried: the lead's decision (fix the cause, then x)";
		const out = args[0] === "status" ? JSON.stringify(doc) : JSON.stringify({ data: { task: { id: "t9", block_detail: detail } } });
		return { code: 0, stdout: out, stderr: "", timedOut: false, spawnError: "", ms: 1 };
	};
	const cfg: Config = { bin: "/opt/amq-squad", amq: "", me: "cto", root: "/r", session: "s", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: true };
	const screen = new Screen(cfg, runner, () => ({ mtimeMs: 1, size: 1 }));
	await screen.poll();
	const lines = renderScreen(screen.last!, now, undefined, screen.details);
	assert.ok(lines.includes("  t9 blocked: its seat's launch failed twice, AMQ's apply failed (AMQ's reason is below the line); fix the cause, then /reset it (or relaunch the roster)"), lines.join("\n"));
	assert.ok(lines.slice(lines.indexOf(RULE)).includes("  t9 blocked: version_probe_failed at stage apply_failed"));
	assert.deepEqual(calls[1], ["task", "show", "t9", "--json"]);
	// a seat's own free-text block: its record names no launch, the fallback
	assert.equal(blockWords({ id: "t9", task_state: "blocked", block_reason: "needs_decision" }, { block_detail: "the seat needs a decision" }).line, "t9 blocked; its reason is below the line");
});

// Row 7 (Codex on ed3d1e65): a reason named for an Object property is no
// entry of any table: "constructor", "__proto__" and their kind render the
// fallback line, and the screen renders.
test("row 7: a reason named constructor or __proto__ renders the fallback line", () => {
	for (const r of ["constructor", "__proto__", "toString", "hasOwnProperty"]) {
		const doc: ScreenStatus = {
			...fixture,
			seats: [{ ...fixture.seats[0], reason_code: r, warnings: [`${r}: x`] }],
			tasks: [{ ...fixture.tasks[2], id: "t8", wait_reason: r, wait_holders: [] }, { ...fixture.tasks[0], id: "t9", block_reason: r }],
			decisions: [{ at: "2026-11-01T09:00:00Z", kind: "exit_refused", seat: "old", task_id: "", reason: `${r} at gone`, reason_code: "" }],
			train: { runs: [{ ...fixture.train!.runs[0], refusal: `refusal PR #9 ${r}: x` }], alerts: [] },
			requests: [{ ...fixture.requests![0], refused: [r] }],
		};
		const lines = renderScreen(doc, now);
		assert.ok(lines.includes("  t8 waiting"), `${r}: ${lines.join("\n")}`);
		assert.ok(lines.includes("  t9 blocked; its reason is below the line"), r);
		assert.ok(lines.includes("    ! warning; its code is below the line"), r);
		assert.ok(lines.some((l) => /^ {2}qa .* {2}waiting( |$)/.test(l)), r);
		assert.ok(lines.includes("  seat old did not stop cleanly; ask the owner to inspect it before the seat is relaunched"), r);
		assert.ok(lines.some((l) => /^ {2}the merge train stopped(?: at PR #9)?; its (?:rule|refusal) is below the line$/.test(l)), r);
		assert.ok(lines.some((l) => l.includes("the relay refused a reply")), r);
		assert.equal(seatDoing({ handle: "x", state: "online", reason_code: r }, undefined).text, "waiting", r);
		assert.equal(decisionWords({ kind: r, seat: "x" }), "the scheduler recorded something; its kind is below the line", r);
		assert.equal(trainRunWords(r), "its last step is below the line", r);
	}
});

// Row 7: the size cap's words are composed from the commit step's recorded
// numbers (its block_detail, read from the task's own record), never
// guessed; the Go format the words read is the commit step's own.
test("row 7: the size cap's numbers come from the recorded detail through task show", async () => {
	const src = go("internal/seatcommit/commit.go");
	assert.ok(src.includes('"size_cap_files: %d changed files over the cap of %d"') && src.includes('"size_cap_lines: %d changed lines over the cap of %d"'), "the commit step's size cap detail changed form");
	const t27 = { ...fixture.tasks[0], id: "t27", block_reason: "size_cap_exceeded", updated_at: "2026-11-01T09:04:30Z" };
	const doc = { ...fixture, tasks: [t27] };
	const calls: string[][] = [];
	const runner = async (_b: string, args: string[]): Promise<RunResult> => {
		calls.push(args);
		const out = args[0] === "status" ? JSON.stringify(doc) : JSON.stringify({ data: { task: { id: "t27", block_detail: "size_cap_exceeded (size_cap_files: 16 changed files over the cap of 8)" } } });
		return { code: 0, stdout: out, stderr: "", timedOut: false, spawnError: "", ms: 1 };
	};
	const cfg: Config = { bin: "/opt/amq-squad", amq: "", me: "cto", root: "/r", session: "s", hookTimeoutMs: 1000, holdTimeoutMs: 1000, models: [], lead: true };
	const screen = new Screen(cfg, runner, () => ({ mtimeMs: 1, size: 1 }));
	await screen.poll();
	const lines = renderScreen(screen.last!, now, undefined, screen.details);
	assert.ok(lines.includes("  t27 blocked: the commit step refused it, 16 files over the cap of 8; ask the owner to raise the cap or split it"), lines.join("\n"));
	assert.deepEqual(calls[1], ["task", "show", "t27", "--json"]);
	// read once per change of the task
	screen.pollKeySeen = undefined;
	await screen.poll();
	assert.equal(calls.filter((c) => c[0] === "task").length, 1);
	// no detail on record: no numbers are made up
	assert.ok(renderScreen(doc, now).includes("  t27 blocked: the commit step refused it, the diff is over the size cap; ask the owner to raise the cap or split it"));
	// the lines form and both halves
	assert.match(BLOCK_WORDS.size_cap_exceeded({ block_detail: "size_cap_lines: 900 changed lines over the cap of 400; size_cap_files: 16 changed files over the cap of 8" })[0], /16 files over the cap of 8 and 900 lines over the cap of 400$/);
});

// Row 7 (#859): status composes the dependency-merge wait in words from the
// recorded id and PR number; the screen shows it verbatim. When the Go
// sources carry the form, its words are the ones the screen matches.
test("row 7: the dependency-merge wait shows verbatim", () => {
	const src = go("internal/task/class.go");
	if (src.includes("dependencyMergeWaitPrefix")) {
		assert.ok(src.includes('dependencyMergeWaitPrefix = "waiting for "') && src.includes('dependencyMergeWaitSuffix = " to merge"'), "the dependency-merge wait changed form");
	}
	assert.equal(waitWords({ id: "t29", task_state: "pending", wait_reason: "waiting for t27's PR #812 to merge" }).line, "t29 waiting for t27's PR #812 to merge");
});

// Row 3: a seat with a lease shows "working on <id>: <title>"; an idle seat
// "idle"; a waiting task its plain reason; a blocked task what happened and
// what to do. Reverted (the 3.1.0 state/reason_code columns), none shows.
test("row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do", () => {
	const doc: ScreenStatus = {
		...fixture,
		seats: [
			{ ...fixture.seats[1], handle: "reviewer", reason_code: "lease_claimed", state: "online", task_id: "t28", warnings: [] },
			{ ...fixture.seats[0], handle: "dev", reason_code: "idle", state: "online", task_id: "", warnings: [] },
			{ ...fixture.seats[0], handle: "new", reason_code: "no_signal", state: "unknown", task_id: "", warnings: [], launch: { ...fixture.seats[0].launch!, outcome: "staged" } },
			{ ...fixture.seats[0], handle: "old", reason_code: "no_live_signal", state: "stale", task_id: "", warnings: [] },
		],
		tasks: [
			{ ...fixture.tasks[1], id: "t28", title: "Review the parser", lease_holder: "reviewer", pr_number: 0 },
			{ ...fixture.tasks[2], id: "t29", wait_reason: "no_roster_seat", wait_pool: "claude:opus:high", wait_holders: [] },
			{ ...fixture.tasks[2], id: "t30", wait_reason: "seat_cap", wait_holders: [] },
			{ ...fixture.tasks[0], id: "t31", block_reason: "verdict_failed" },
		],
	};
	const lines = renderScreen(doc, now);
	for (const want of [
		/^ {2}reviewer {2}claude sonnet medium {2}working on t28: Review the parser( {2}· .*)?$/,
		/^ {2}dev {2}codex gpt-5\.6-sol high {2}idle( {2}· .*)?$/,
		/^ {2}new {2}codex gpt-5\.6-sol high {2}starting( {2}· .*)?$/,
		/^ {2}old {2}codex gpt-5\.6-sol high {2}stopped( {2}· .*)?$/,
		/^ {2}t28 in progress on reviewer: Review the parser$/,
		/^ {2}t29 waiting: no seat serves claude:opus:high$/,
		/^ {2}t30 waiting: every seat is busy and the cap of 3 seats is reached$/,
		/^ {2}t31 blocked: its verifier failed; send it back with \/reset to try again$/,
	]) {
		assert.ok(lines.some((l) => want.test(l)), `no line ${want} in\n${lines.join("\n")}`);
	}
});

// Row 7: a free-text title (the model's) passes the redaction and the
// control-character strip before the cut, on the seat line and the task line.
test("row 7: a title with a canary and a control character shows redacted and stripped", () => {
	const canary = "Ab9" + "xY7q1";
	const title = `Fix\u0007 the parser token=${canary}\u001b[2J and then a long tail that runs past the cut`;
	const doc: ScreenStatus = {
		...fixture,
		seats: [{ ...fixture.seats[1], task_id: "t2", warnings: [] }],
		tasks: [{ ...fixture.tasks[1], title }],
	};
	const lines = renderScreen(doc, now).filter((l) => l.includes("Fix"));
	assert.equal(lines.length, 2, lines.join("\n"));
	for (const l of lines) {
		assert.ok(!l.includes(canary), l);
		assert.ok(!/[\u0000-\u001f\u007f]/.test(l), JSON.stringify(l));
		assert.match(l, /Fix the parser token=<redacted> and then a long tail/);
	}
	assert.equal(clean(title).length, TITLE_MAX);
	assert.ok(clean(title).endsWith("..."));
});

// Row 7: each empty block shows its one plain line, never a blank block;
// solo with no plan says what to do.
test("row 7: each empty block says so in one plain line", () => {
	const empty: ScreenStatus = { ...fixture, seats: [], tasks: [], gates: [], attention: [], decisions: [], train: { runs: [], alerts: [] }, requests: [] };
	const lines = renderScreen(empty, now);
	for (const [title, line] of [
		["seats (0)", "  No seat is running yet."],
		["tasks (0)", "  No tasks yet; add one with /add."],
		["train", "  No merge train has run yet."],
		["waiting on you (0)", "  Nothing is waiting on you."],
	]) {
		const i = lines.indexOf(title);
		assert.ok(i >= 0, `${title} in\n${lines.join("\n")}`);
		assert.equal(lines[i + 1], line);
	}
	const solo = new SoloTeam({ bin: "/b", amq: "", me: "lead", root: tmpdir(), session: "", hookTimeoutMs: 1, holdTimeoutMs: 1, models: [], lead: false, solo: true });
	assert.deepEqual(solo.planLines(), ["amq-squad: solo lead", "  No team yet. Tell me what you want built and I will propose a plan.", "/plan  /confirm  /team hide"]);
});

// Row 4 (the plan reviewer's addition): every seat warning code the
// scheduler emits has plain words in the one table, from its recorded
// fields; above the rule a warning is "<what>; <what to do>", below it the
// code and the detail verbatim; an unmapped code at run time reads
// "warning". Reverted (a code dropped, or the 3.1.0 "! <code>: <detail>"
// line), it fails here by name.
test("row 4: every seat warning code in Go's list has plain words; the code is below the rule", () => {
	const codes = goWarnings();
	assert.equal(codes.length, 7, `Go's warning list: ${codes}`);
	for (const c of codes) assert.ok(Object.hasOwn(WARNING_WORDS, c), `seat warning ${c} has no words`);
	const qa = fixture.seats[0];
	for (const c of codes) {
		const w = warningWords("qa", `${c}: a detail_with_code 0de97c89a1b2c3d4`, qa);
		assert.equal(codeIn(w.line), "", `${c}: ${w.line}`);
		assert.ok(w.line.includes("; "), `${c} says no what to do: ${w.line}`);
		assert.equal(w.record, `qa: ${c}: a detail_with_code 0de97c89a1b2c3d4`);
	}
	assert.deepEqual(warningWords("qa", "made_up_warning: x", qa), { line: "warning; its code is below the line", record: "qa: made_up_warning: x" });
	// composed from the recorded fields: the launch timing, the detail's
	// duration and model
	assert.equal(warningWords("qa", qa.warnings![0], qa).line, "it came up in 12.4s, over the 10s ceiling (slowest step: the seat's own start); nothing to do unless it repeats, and /dismiss hides it once you know");
	const lines = renderScreen(fixture, now);
	const rule = lines.indexOf(RULE);
	for (const s of fixture.seats) for (const w of s.warnings ?? []) assert.ok(lines.slice(rule).includes(`  ${s.handle}: ${w}`.length > 202 ? lines.slice(rule).find((l) => l.startsWith(`  ${s.handle}: ${w.slice(0, 60)}`))! : `  ${s.handle}: ${w}`), `${s.handle}'s ${w.slice(0, 30)} is not below the rule`);
	assert.ok(!lines.slice(0, rule).some((l) => /^ {4}! [a-z_]+: /.test(l)), lines.join("\n"));
});

// Row 4 (the plan reviewer's addition): a stop line says what happened and
// what to do, both from the tables; its record stays verbatim below the
// rule. The train's line likewise.
test("row 4: a seat's stop and the train's stop say what happened and what to do", () => {
	assert.equal(decisionWords({ kind: "exit_refused", seat: "old-dev", reason: "ledger_tampered at gone" }), "seat old-dev did not stop cleanly: the commits ledger does not verify; ask the owner to inspect the commits ledger before anything commits");
	assert.equal(decisionWords({ kind: "seat_launch_failed", seat: "rev-1", reason: "x", reason_code: "version_probe_failed", stage: "apply_failed" }), "seat rev-1's launch failed: AMQ's apply failed; ask the lead to relaunch the roster");
	for (const kind of Object.keys(DECISION_WORDS)) {
		const l = decisionWords({ kind, seat: "dev", reason: "made_up_reason: x" });
		assert.equal(codeIn(l), "", `${kind}: ${l}`);
		assert.ok(l.includes("; "), `${kind} says no what to do: ${l}`);
	}
	assert.equal(trainStop("refusal PR #812 ci_green: a required check failed"), "the merge train stopped at PR #812: its CI is not green; fix the failing check and the train tries again");
	// the plan reviewer on #873: a seat branch is never rebased by hand; the
	// strict up-to-date rule's remedy is the lead's
	assert.equal(trainStop("refusal PR #9 strict_required_status_checks_policy: the branch is behind and the train cannot bring it up to date: x"), "the merge train stopped at PR #9: its branch is behind its base; the train brings it up to date and waits for its checks; if the train cannot, reset the task so a seat redoes it on the current base");
	for (const r of Object.keys(TRAIN_RULE_WORDS)) assert.equal(codeIn(trainStop(`refusal PR #9 ${r}: x`)), "", r);
	assert.equal(trainStop("refusal PR #9 made_up_rule: x"), "the merge train stopped at PR #9; its rule is below the line");
	const lines = renderScreen(fixture, now);
	const rule = lines.indexOf(RULE);
	assert.ok(lines.slice(rule).includes("  exit_refused: old-dev ledger_tampered at gone"));
	assert.ok(!lines.some((l) => l.includes("last stop")), "our own term is back");
});

// plainAbove is a screen's lines above its rule: every line but the header
// (the lead's root path), warnings and phone requests included.
function plainAbove(lines: string[]): string[] {
	const rule = lines.indexOf(RULE);
	return (rule < 0 ? lines.slice(0, -1) : lines.slice(0, rule)).slice(1);
}

const res = (code: number, stdout: string, stderr = ""): RunResult => ({ code, stdout, stderr, timedOut: false, spawnError: "", ms: 1 });

// Row 4: no refusal or outcome line above its rule carries a snake_case code
// or a hex run; the code is below the rule. The sweep renders every block
// reason, every wait reason, every seat code, an open gate with its digest,
// and every key's refusals (each verb refusing with a coded line), and the
// solo plan block with its digest and a refused confirm. Reverted (a code
// back above the rule), the line is named.
test("row 4: no line above the rule carries a snake_case code or a hex run; the code is below it", async () => {
	const tasks = [
		...Object.keys(BLOCK_WORDS).map((r, i) => ({ ...fixture.tasks[0], id: `b${i}`, block_reason: r })),
		{ ...fixture.tasks[0], id: "bx", block_reason: "needs_decision" },
		...Object.keys(WAIT_WORDS).map((r, i) => ({ ...fixture.tasks[2], id: `w${i}`, wait_reason: r })),
		{ ...fixture.tasks[2], id: "wx", wait_reason: "made_up_wait" },
	];
	const seats = [...Object.keys(SEAT_DOING), "made_up_code"].map((c, i) => ({ ...fixture.seats[1], handle: `s${i}`, reason_code: c, task_id: "b0", warnings: [] }));
	seats.push({ ...fixture.seats[1], handle: "senior_dev", reason_code: "lease_claimed", task_id: "b0", warnings: [] });
	seats.push({ ...fixture.seats[1], handle: "1deadbeef", reason_code: "idle", task_id: "", warnings: [] });
	seats.push({ ...fixture.seats[0], handle: "warned", reason_code: "idle", task_id: "", warnings: [...goWarnings(), "made_up_warning"].map((c) => `${c}: detail_code 0de97c89a1b2c3d4`) });
	const decisions = [...Object.keys(DECISION_WORDS), "made_up_kind"].map((kind, i) => ({ at: "2026-11-01T09:00:00Z", kind, seat: i % 2 ? "senior_dev" : "1deadbeef", task_id: "t1", reason: "ledger_tampered at gone", reason_code: "" }));
	const runs = [{ run: "mt-20261101T090000Z-a1b2c3d4", started_at: "", last_at: "2026-11-01T09:00:00Z", last_kind: "merge_intent", repository: "o/a", base: "main", prs: [9], merged: [], refusal: "refusal PR #9 task_scope,size_cap: src/x_y.go", done: false, dry_run: false }];
	const requests = Object.keys(RELAY_WORDS).map((r, i) => ({ request_id: `q-0de97c89a1b${i}`, thread: "cockpit/q", lead: "cto", turns: 1, posts: 0, relayed_at: null, refused: [r, "made_up_relay"], closed: false, updated_at: "" }));
	const doc: ScreenStatus = { ...fixture, tasks, seats, decisions, requests, train: { runs, alerts: [{ kind: "gate_store_unreadable", detail: "a gate store of this squad could not be read, so no gate in it clears an alert; fix or remove the record named below the line\n\n----------\nthe gate store /r/s-0de97c89a1b2/gates could not be read (x_y)" }] }, gates: [{ id: "g1", action_class: "github_release", target_digest: "sha256:8502aa0011223344", state: "open" }], warnings: ["gate store: /r/gates/abcdef0123456789.json: unreadable"] };
	// a seat's handle is a name, not a code, and every seat line carries it
	// (Codex on #866): a token equal to a handle of the document's seats is
	// allowed above the rule; the fixture's handles include a snake_case one
	// and a hex-looking one
	const handlesOf = (d: ScreenStatus) => d.seats.map((x) => x.handle);
	// a whole token equal to a handle (codeIn's own separators and its strip
	// of . ` * at a token's ends), never a substring of a longer token
	const SEP = /([\s,;:()"'[\]{}<>!?]+)/;
	const sweep = (lines: string[], what: string, handles: string[] = handlesOf(doc)) => {
		for (const l of plainAbove(lines)) {
			const masked = l
				.split(SEP)
				.map((t) => (handles.includes(t.replace(/^[.`*]+|[.`*]+$/g, "")) ? "" : t))
				.join("");
			assert.equal(codeIn(masked), "", `${what}: a code above the rule: ${JSON.stringify(l)}\n${lines.join("\n")}`);
		}
	};
	const screen = renderScreen(doc, now);
	sweep(screen, "the screen");
	assert.ok(screen.some((l) => l.startsWith("  senior_dev ")) && screen.some((l) => l.startsWith("  1deadbeef ")), "the handles are not on their seat lines");
	// the allowance is for whole handle tokens only: a longer token that
	// carries a handle, or a token that is no handle, fails the sweep itself
	for (const bad of ["seat senior_dev1 did not stop cleanly", "seat a1deadbeef0 did not stop cleanly", "seat not_a_handle did not stop cleanly"]) {
		assert.throws(() => sweep(["header", `  ${bad}`, "/add"], bad), /a code above the rule/, bad);
	}
	sweep(["header", "  seat senior_dev did not stop cleanly; seat 1deadbeef's process is gone", "/add"], "whole handles");
	const rec = screen.slice(screen.indexOf(RULE));
	for (const code of ["needs_decision", "made_up_wait", "made_up_code", "8502aa0011223344", "launch_budget_exceeded", "made_up_warning", "relay_refused_gate_shape", "task_scope,size_cap", "gate_store_unreadable", "mt-20261101T090000Z-a1b2c3d4 merge_intent"]) assert.ok(rec.some((l) => l.includes(code)), `${code} is not below the rule`);
	assert.ok(screen.includes("  running: merging a PR (the merge is pending), 5m ago; PRs #9; 0 merged"), screen.join("\n"));
	// each stop decision kind (the newest shows): its words above, its record below
	for (const d of decisions.filter((x) => Object.hasOwn(DECISION_WORDS, x.kind))) {
		const one = renderScreen({ ...doc, decisions: [d] }, now);
		sweep(one, d.kind);
		assert.ok(one.slice(one.indexOf(RULE)).some((l) => l.includes("ledger_tampered at gone")), `${d.kind}'s record is not below the rule`);
	}
	// every key's refusals and outcomes, from verbs that refuse with a code
	const coded = res(15, "", "error: task widen t1 refused (widen_not_requested): widen refused (widen_not_requested): task t1 is not blocked (state in_progress, reason needs_decision)\n");
	const keys = new Keys({ bin: "/b", amq: "", me: "cto", root: "/r", session: "s", hookTimeoutMs: 1, holdTimeoutMs: 1, models: [], lead: true }, async (_b, args) => (args[0] === "gate" && args[1] === "status" ? res(0, JSON.stringify({ data: [{ gate_id: "g1", class: "github_release", typed_class: "release", target: "gh release", digest: "sha256:8502aa0011223344aabbccdd", state: "open", question_sent: false }] })) : args[1] === "show" ? res(0, JSON.stringify({ data: { task: { id: "t1", state: "blocked", block_reason: "widen_requested", block_detail: "needed", refused_paths: ["a.go"] } } })) : args[1] === "propose" ? res(0, JSON.stringify({ data: { digest: "sha256:c0ffee@d15c" } })) : coded));
	const ctx = fakeCtx({ hasUI: true });
	const answers: Record<string, string[]> = {};
	ctx.ui.select = async (_t: string, o: string[]) => o[0];
	ctx.ui.input = async (title: string) => (answers[title]?.shift() ?? "x");
	ctx.ui.confirm = async () => true;
	const outcomes: KeyOutcome[] = [];
	const st: ScreenStatus = { ...fixture, tasks: [{ ...fixture.tasks[0], block_reason: "widen_requested" }], gates: doc.gates };
	for (const k of ["add", "reset", "dismiss", "answer", "widen", "foo"]) outcomes.push(await keys.press(k, ctx, st));
	outcomes.push(await keys.press("add", ctx, st, "seat"));
	outcomes.push(await keys.press("add", ctx, { ...st, project_path: undefined }, "seat"));
	outcomes.push(await keys.press("add", ctx, st, "bogus"));
	outcomes.push(await keys.press("reset", ctx, { ...st, tasks: [] }));
	outcomes.push(await keys.press("dismiss", ctx, { ...st, seats: [] }));
	outcomes.push(await keys.press("answer", ctx, { ...st, gates: [] }));
	outcomes.push(await keys.press("widen", ctx, { ...st, tasks: [] }));
	outcomes.push(await keys.press("add", fakeCtx({ hasUI: false }), st));
	outcomes.push(await keys.press("add", ctx, undefined));
	for (const o of outcomes) {
		assert.ok(o.line, JSON.stringify(o));
		sweep(renderScreen({ ...fixture, decisions: [] }, now, o), `/${o.key}`);
	}
	assert.ok(outcomes.filter((o) => o.refused).length >= 10, "the sweep saw too few refusals");
	// a successful /widen: the seat's paths (free text, here a long hex run)
	// are below the rule, the count above it
	const hexPath = "src/0de97c89a1b2c3d4e5f6a7b8/helper.go";
	const ok = new Keys({ bin: "/b", amq: "", me: "cto", root: "/r", session: "s", hookTimeoutMs: 1, holdTimeoutMs: 1, models: [], lead: true }, async (_b, args) => (args[1] === "show" ? res(0, JSON.stringify({ data: { task: { id: "t1", state: "blocked", block_reason: "widen_requested", block_detail: "needed", refused_paths: [hexPath, "deadbeef0123456789"] } } })) : res(0, JSON.stringify({ data: { ok: true } }))));
	const widened = await ok.press("widen", ctx, st);
	assert.equal(widened.refused, false);
	const wl = renderScreen({ ...fixture, decisions: [] }, now, widened);
	sweep(wl, "/widen widened");
	assert.ok(wl.slice(wl.indexOf(RULE)).some((l) => l.includes(hexPath)), "the widened paths are not below the rule");
	// the solo plan block: the digest and a refused confirm's code below the rule
	const team = new SoloTeam({ bin: "/b", amq: "", me: "lead", root: tmpdir(), session: "", hookTimeoutMs: 1, holdTimeoutMs: 1, models: [], lead: false, solo: true });
	team.proposal = { digest: "sha256:" + "0123456789abcdef".repeat(4) + "@absent", prefix: "0123456789abcdef", plan: ["goal: x", "seat a (b) pi"], project_dir: "/p" };
	team.show({ key: "confirm", refused: true, line: "The lead did not take the confirmation, so nothing was confirmed.", record: "peer_not_lead_pi: pid 9" }, fakeCtx());
	const plan = team.planLines();
	sweep(plan, "the plan block");
	assert.ok(plan.slice(plan.indexOf(RULE)).some((l) => l.includes("0123456789abcdef")), "the digest is not below the rule");
	assert.ok(plan.slice(plan.indexOf(RULE)).some((l) => l.includes("peer_not_lead_pi")), "the code is not below the rule");
});

// Row 4, the pi fixture: the widen refusal at tools.ts (refusedPaths) says
// what happened and what to do above its rule, the state and the reason
// code below it.
test("row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule", () => {
	for (const t of [
		{ id: "t1", state: "in_progress", block_reason: "" },
		{ id: "t1", state: "blocked", block_reason: "needs_decision" },
		{ id: "t1", state: "blocked", block_reason: "outside_declared_files", refused_paths: [] },
	]) {
		let msg = "";
		try {
			refusedPaths(t);
		} catch (err) {
			msg = (err as Error).message;
		}
		const at = msg.indexOf(`\n\n${RULE}\n`);
		assert.ok(at > 0, `no rule in ${JSON.stringify(msg)}`);
		assert.equal(codeIn(msg.slice(0, at)), "", `a code above the rule: ${msg}`);
		assert.notEqual(codeIn(msg.slice(at)), "", `no code below the rule: ${msg}`);
	}
});
