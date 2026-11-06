// 3.2.0-0c-B rule 7 and row B9: the screen's words for a tamper alarm come
// from the decision's reason code, not from the prepare's fixed line; the
// typed block reads with the same words; the seat and the quarantine path
// go on the record line below the rule; "relaunch the roster" does not
// appear for a tamper (it stays the remedy of every other prepare failure).
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { renderScreen, type ScreenStatus } from "../extensions/lib/screen.ts";
import { blockNeedsRecord, blockWords, decisionKind, decisionWords, tamperRecord } from "../extensions/lib/words.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixture = JSON.parse(fs.readFileSync(path.join(here, "status-fixture.json"), "utf8")) as ScreenStatus;
const now = new Date("2026-11-01T09:05:00Z");
const slot = "/Users/owner/code/app/.agent-mail/v31/extensions/amq-squad/quarantine/commits.dev.seq150";
// tamper.go's tamperDetail: the seat, the path first, the seq, the refusal
const detail = `seat dev: quarantined at ${slot} (seq 150): the commit ledger of seat dev: commit ledger dev: ledger_tampered: the tag of commits.dev link 150 fails; the refused document is quarantined at ${slot}`;

function block(lines: string[], title: string): string[] {
	const i = lines.findIndex((l) => l.startsWith(title));
	assert.ok(i >= 0, `no ${title} block in\n${lines.join("\n")}`);
	const j = lines.findIndex((l, k) => k > i && !l.startsWith(" "));
	return lines.slice(i, j < 0 ? undefined : j);
}

function below(lines: string[]): string[] {
	const i = lines.indexOf("----------");
	return i < 0 ? [] : lines.slice(i + 1, -1);
}

test("B9: a tamper prepare failure shows the tamper words and the path; relaunch the roster does not appear", () => {
	const d = { at: "2026-11-01T09:04:00Z", kind: "seat_prepare_failed", seat: "dev", task_id: "t7", reason_code: "ledger_tampered", reason: detail };
	const lines = renderScreen({ ...fixture, decisions: [d] }, now);
	const seats = block(lines, "seats");
	assert.equal(seats.at(-1), "  seat dev could not be prepared for t7: the seat's commit record failed its check; the lead investigates before the seat works again");
	assert.ok(!seats.some((l) => l.includes("relaunch the roster")), seats.join("\n"));
	const record = below(lines).find((l) => l.startsWith("  ledger_tampered: dev"));
	assert.ok(record, below(lines).join("\n"));
	assert.ok(record!.includes(`quarantined at ${slot}`), record);
	// the stop kind's class and its words with and without a task
	assert.equal(decisionKind("seat_prepare_failed"), "stop");
	assert.equal(decisionWords({ kind: "seat_prepare_failed", seat: "dev", reason_code: "ledger_tampered", reason: detail }), "seat dev could not be prepared: the seat's commit record failed its check; the lead investigates before the seat works again");
	// every other prepare failure keeps its remedy
	assert.equal(decisionWords({ kind: "seat_prepare_failed", seat: "dev", task_id: "t7", reason: "the commit ledger of seat dev: dirty" }), "seat dev could not be prepared for t7; ask the lead to relaunch the roster");
	// the count line of a repeated non-tamper failure (rule 2)
	assert.equal(decisionKind("seat_prepare_suppressed"), "stop");
	assert.equal(decisionWords({ kind: "seat_prepare_suppressed", seat: "dev", task_id: "t7", reason: "the same prepare failure repeats; ..." }), "seat dev's prepare failure for t7 repeats; further repeats are counted, not shown, until the task, the seat or the reason changes");
	// the recovery's own line is routine (the task's return is a task_reset)
	assert.equal(decisionKind("ledger_recovered"), "routine");
	assert.equal(decisionKind("ledger_tampers_restored"), "routine");
});

test("B9: the typed block reads with the same words, the seat and the path on its record line", () => {
	const t = { id: "t7", task_state: "blocked", block_reason: "ledger_tampered" };
	assert.ok(blockNeedsRecord("ledger_tampered"));
	assert.deepEqual(blockWords(t, { block_detail: detail }), {
		line: "t7 blocked: the seat's commit record failed its check; the lead investigates before the seat works again",
		record: `t7: seat dev, quarantined at ${slot}`,
	});
	// the commit step's write-time refusal (rule 4) carries the same path,
	// in Go's own form (seatcommit commit.go writeOutcome: no "seat H:"
	// prefix; Codex's P3 on #904): the record names the seat and the path
	const write = `the commits ledger refused the write: commit ledger dev: ledger_tampered: the tag of commits.dev link 150 fails; the refused document is quarantined at ${slot}; nothing is recorded or pushed until the lead recovers the ledger (scheduler recover)`;
	assert.equal(tamperRecord(write), `seat dev, quarantined at ${slot}`);
	assert.deepEqual(blockWords(t, { block_detail: write }), {
		line: "t7 blocked: the seat's commit record failed its check; the lead investigates before the seat works again",
		record: `t7: seat dev, quarantined at ${slot}`,
	});
	// with no detail on record yet, the line still reads; no record line
	assert.deepEqual(blockWords(t), { line: "t7 blocked: the seat's commit record failed its check; the lead investigates before the seat works again", record: undefined });
	// the tasks block of the screen
	const row = { ...fixture.tasks[0], id: "t7", task_state: "blocked", block_reason: "ledger_tampered", block_route: "lead", title: "the task" } as ScreenStatus["tasks"][number];
	const lines = renderScreen({ ...fixture, tasks: [row], decisions: [] }, now, undefined, { t7: { block_detail: detail } });
	const tasks = block(lines, "tasks");
	assert.ok(tasks.includes("  t7 blocked: the seat's commit record failed its check; the lead investigates before the seat works again"), tasks.join("\n"));
	assert.ok(below(lines).includes(`  t7: seat dev, quarantined at ${slot}`), below(lines).join("\n"));
	assert.ok(!lines.some((l) => l.includes("relaunch the roster")), lines.join("\n"));
});
