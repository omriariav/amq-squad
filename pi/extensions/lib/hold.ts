// The hold seam (plan 4.4, S5 step 3, bridge revision 4): a tool_call
// handler that returns a promise holds the call, because pi 1.3.0 awaits every
// tool_call handler before it runs the tool. hold() settles on the first
// decision from any face (a local dialog, a remote answer, the timer, the
// turn's abort signal); every outcome but "allow" blocks the call. pi applies
// no timeout of its own, so the hold owns one.
//
// The retry cap (S5 open question 4) stops an approval storm: a model that
// retries a blocked call makes a new call id each time. The same call
// (tool and input) blocked once is blocked again at once, and after
// maxBlockedPerSession blocked holds, or maxHoldsPerTurn holds in one turn,
// every further call that would hold is blocked without a hold.
import { createHash } from "node:crypto";

export type Outcome = "allow" | "deny" | "timeout" | "aborted" | "refused" | "capped";
export type Face = "local" | "remote" | "timer" | "signal" | "cap" | "error";
export type Decision = { outcome: Outcome; detail: string; face: Face };

// A Decider starts one face. It calls settle at most once and returns a
// cleanup that stops it when another face wins.
export type Decider = (settle: (d: Decision) => void, signal: AbortSignal) => (() => void) | void;

export type HoldOptions = {
	deciders: Decider[];
	timeoutMs: number;
	signal?: AbortSignal; // the turn's signal (Escape aborts the hold)
};

export function hold(opts: HoldOptions): Promise<Decision> {
	return new Promise((resolve) => {
		const ac = new AbortController();
		const cleanups: (() => void)[] = [];
		let done = false;
		const settle = (d: Decision) => {
			if (done) return;
			done = true;
			clearTimeout(timer);
			opts.signal?.removeEventListener("abort", onAbort);
			ac.abort();
			for (const c of cleanups.splice(0)) {
				try {
					c();
				} catch {
					// cleanup is best effort
				}
			}
			resolve(d);
		};
		const onAbort = () => settle({ outcome: "aborted", detail: "turn aborted", face: "signal" });
		const timer = setTimeout(() => settle({ outcome: "timeout", detail: `no decision in ${opts.timeoutMs} ms`, face: "timer" }), opts.timeoutMs);
		if (opts.signal?.aborted) return onAbort();
		opts.signal?.addEventListener("abort", onAbort);
		for (const start of opts.deciders) {
			if (done) break;
			try {
				const c = start(settle, ac.signal);
				if (c) cleanups.push(c);
			} catch (err) {
				settle({ outcome: "refused", detail: `hold face failed: ${String(err)}`, face: "error" });
			}
		}
	});
}

// manifestHash names exactly one call: tool, input and call id.
export function manifestHash(tool: string, input: unknown, toolCallId: string): string {
	return "sha256:" + createHash("sha256").update(JSON.stringify({ tool, input, id: toolCallId })).digest("hex");
}

// callKey names a call by tool and input only, so a retry of the same call
// under a new id has the same key.
export function callKey(tool: string, input: unknown): string {
	return createHash("sha256").update(JSON.stringify({ tool, input })).digest("hex");
}

export type CapLimits = { maxBlockedPerSession: number; maxHoldsPerTurn: number };

export const DEFAULT_CAP: CapLimits = { maxBlockedPerSession: 3, maxHoldsPerTurn: 5 };

export class RetryCap {
	limits: CapLimits;
	blocked = 0;
	turn = "";
	turnHolds = 0;
	blockedKeys = new Set<string>();

	constructor(limits: CapLimits) {
		this.limits = limits;
	}

	// admit returns a capped decision, or null when the call may hold.
	admit(turn: string, key: string): Decision | null {
		if (turn !== this.turn) {
			this.turn = turn;
			this.turnHolds = 0;
		}
		if (this.blockedKeys.has(key)) {
			return { outcome: "capped", detail: "this exact call was already blocked; do not retry it, report the block to your lead", face: "cap" };
		}
		if (this.blocked >= this.limits.maxBlockedPerSession) {
			return { outcome: "capped", detail: `approval cap reached (${this.blocked} blocked holds this session); stop and report to your lead`, face: "cap" };
		}
		if (this.turnHolds >= this.limits.maxHoldsPerTurn) {
			return { outcome: "capped", detail: `approval cap reached (${this.turnHolds} holds this turn); stop and report to your lead`, face: "cap" };
		}
		this.turnHolds++;
		return null;
	}

	record(key: string, d: Decision): void {
		if (d.outcome === "allow" || d.outcome === "aborted") return;
		this.blocked++;
		this.blockedKeys.add(key);
	}
}

// blockReason is what the model reads as the tool result of a blocked call.
export function blockReason(d: Decision, what: string): string {
	return `amq-squad hold: ${what} ${d.outcome} (${d.detail}). Do not retry this call; report the block to your lead.`;
}
