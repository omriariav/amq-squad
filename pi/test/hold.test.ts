// The hold-seam tests of plan 4.4 run here (brief 4-1 ruling 4): a local
// confirm racing a remote answer, several held calls in parallel, and the
// retry cap. before_agent_start injection is in extension.test.ts.
import { test } from "node:test";
import assert from "node:assert/strict";
import { DEFAULT_CAP, hold, RetryCap, type Decider } from "../extensions/lib/hold.ts";

const after = (ms: number, outcome: "allow" | "deny", face: "local" | "remote"): Decider => (settle, signal) => {
	const t = setTimeout(() => settle({ outcome, detail: `${face} after ${ms}`, face }), ms);
	signal.addEventListener("abort", () => clearTimeout(t));
	return () => clearTimeout(t);
};

test("the first decision from any face wins: remote before local", async () => {
	let localStopped = false;
	const local: Decider = (settle, signal) => {
		const t = setTimeout(() => settle({ outcome: "deny", detail: "local", face: "local" }), 200);
		signal.addEventListener("abort", () => {
			localStopped = true;
			clearTimeout(t);
		});
	};
	const d = await hold({ deciders: [local, after(20, "allow", "remote")], timeoutMs: 1000 });
	assert.equal(d.face, "remote");
	assert.equal(d.outcome, "allow");
	assert.equal(localStopped, true, "the losing local dialog is dismissed through its signal");
});

test("the first decision from any face wins: local before remote", async () => {
	const d = await hold({ deciders: [after(10, "deny", "local"), after(100, "allow", "remote")], timeoutMs: 1000 });
	assert.equal(d.face, "local");
	assert.equal(d.outcome, "deny");
});

test("a late second answer does not change the decision", async () => {
	const answers: string[] = [];
	const twice: Decider = (settle) => {
		setTimeout(() => {
			settle({ outcome: "allow", detail: "first", face: "remote" });
			settle({ outcome: "deny", detail: "second", face: "remote" });
			answers.push("both sent");
		}, 10);
	};
	const d = await hold({ deciders: [twice], timeoutMs: 500 });
	assert.equal(d.detail, "first");
	await new Promise((r) => setTimeout(r, 30));
	assert.deepEqual(answers, ["both sent"]);
});

test("timeout and abort block", async () => {
	const t = await hold({ deciders: [], timeoutMs: 30 });
	assert.equal(t.outcome, "timeout");
	const ac = new AbortController();
	setTimeout(() => ac.abort(), 10);
	const a = await hold({ deciders: [after(500, "allow", "remote")], timeoutMs: 1000, signal: ac.signal });
	assert.equal(a.outcome, "aborted");
	const pre = new AbortController();
	pre.abort();
	assert.equal((await hold({ deciders: [], timeoutMs: 1000, signal: pre.signal })).outcome, "aborted");
});

test("a throwing face blocks (fail closed)", async () => {
	const d = await hold({ deciders: [() => { throw new Error("boom"); }], timeoutMs: 1000 });
	assert.equal(d.outcome, "refused");
});

test("several held calls in parallel resolve independently", async () => {
	const t0 = Date.now();
	const ds = await Promise.all([
		hold({ deciders: [after(60, "allow", "remote")], timeoutMs: 1000 }),
		hold({ deciders: [after(20, "deny", "local")], timeoutMs: 1000 }),
		hold({ deciders: [], timeoutMs: 40 }),
	]);
	assert.deepEqual(ds.map((d) => d.outcome), ["allow", "deny", "timeout"]);
	assert.ok(Date.now() - t0 < 300, "parallel holds do not serialize inside the seam");
});

test("the retry cap stops a storm", () => {
	const cap = new RetryCap(DEFAULT_CAP);
	assert.equal(cap.admit("1", "a"), null);
	cap.record("a", { outcome: "deny", detail: "", face: "local" });
	// the same call again, under a new id, is blocked at once
	assert.equal(cap.admit("1", "a")?.outcome, "capped");
	// an allowed or aborted hold does not count
	assert.equal(cap.admit("1", "b"), null);
	cap.record("b", { outcome: "allow", detail: "", face: "remote" });
	assert.equal(cap.admit("1", "b"), null);
	// three blocked holds in a session cap every later hold
	for (const k of ["c", "d"]) {
		assert.equal(cap.admit("2", k), null);
		cap.record(k, { outcome: "timeout", detail: "", face: "timer" });
	}
	assert.match(cap.admit("3", "e")?.detail ?? "", /approval cap reached \(3 blocked/);
	// a turn holds at most maxHoldsPerTurn calls
	const turnCap = new RetryCap({ maxBlockedPerSession: 100, maxHoldsPerTurn: 2 });
	assert.equal(turnCap.admit("t", "1"), null);
	assert.equal(turnCap.admit("t", "2"), null);
	assert.equal(turnCap.admit("t", "3")?.outcome, "capped");
	assert.equal(turnCap.admit("u", "4"), null, "a new turn resets the per-turn count");
});
