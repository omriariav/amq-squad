// A minimal stand-in for pi's ExtensionAPI and ExtensionContext: it records
// handlers and registered tools and lets a test emit events.
import * as crypto from "node:crypto";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

export type Handler = (event: any, ctx: any) => any;

export class FakePi {
	handlers = new Map<string, Handler[]>();
	tools = new Map<string, any>();
	sent: { text: string; opts: unknown }[] = [];
	models: any[] = [];
	model: any = undefined;

	on(name: string, h: Handler): () => void {
		const list = this.handlers.get(name) ?? [];
		list.push(h);
		this.handlers.set(name, list);
		return () => undefined;
	}
	commands = new Map<string, any>();
	// the tools declared to the model, as pi keeps them: a registered tool
	// is active unless it says defaultActive: false; builtins stand for
	// pi's own (read, bash, edit, write) when a test names them
	active = new Set<string>();
	builtins: string[];
	constructor(builtins: string[] = []) {
		this.builtins = builtins;
		for (const b of builtins) this.active.add(b);
	}
	registerTool(def: any): void {
		this.tools.set(def.name, def);
		if (def.defaultActive !== false) this.active.add(def.name);
	}
	setActiveTools(names: string[]): void {
		this.active = new Set(names.filter((n) => this.tools.has(n) || this.builtins.includes(n)));
	}
	getActiveTools(): string[] {
		return [...this.active];
	}
	registerCommand(name: string, def: any): void {
		this.commands.set(name, def);
	}
	sendUserMessage(text: string, opts: unknown): void {
		this.sent.push({ text, opts });
	}
	async setModel(m: any): Promise<boolean> {
		this.model = m;
		return true;
	}
	// batch runs one assistant message's tool calls as pi 1.3.0's agent loop
	// does (pi-agent-core dist/agent-loop.js, executeToolCalls): turn_start,
	// the assistant message's end, then, unless a call's tool is
	// sequential, every call prepared (the tool_call hooks, in order) before
	// any runs, and the admitted ones run together; with a sequential tool
	// in the batch each call is prepared and run before the next. A call to
	// a tool not active when the batch began is not found (pi checks the
	// turn's tool list). between runs after the preparing and before the
	// running (a test's way to change the world in that gap); noMessageEnd
	// leaves the message's end unseen.
	async batch(calls: { toolName: string; input?: any; toolCallId?: string }[], ctx: any, opts: { between?: () => Promise<void>; noMessageEnd?: boolean } = {}): Promise<BatchResult[]> {
		const list = calls.map((c, i) => ({ toolName: c.toolName, input: c.input ?? {}, toolCallId: c.toolCallId ?? `b${++this.calls}-${i}` }));
		const declared = new Set(this.active);
		await this.emit("turn_start", { type: "turn_start", turnIndex: this.calls, timestamp: 0 }, ctx);
		if (!opts.noMessageEnd)
			await this.emit("message_end", { type: "message_end", message: { role: "assistant", content: list.map((c) => ({ type: "toolCall", id: c.toolCallId, name: c.toolName, arguments: c.input })) } }, ctx);
		const prepare = async (c: (typeof list)[number]): Promise<BatchResult | undefined> => {
			if (!declared.has(c.toolName)) return { toolName: c.toolName, toolCallId: c.toolCallId, outcome: "not_found", reason: `Tool ${c.toolName} not found` };
			const out = await this.emit("tool_call", { type: "tool_call", toolCallId: c.toolCallId, toolName: c.toolName, input: c.input }, ctx);
			if (out?.block) return { toolName: c.toolName, toolCallId: c.toolCallId, outcome: "blocked", reason: out.reason };
			return undefined;
		};
		const runOne = async (c: (typeof list)[number]): Promise<BatchResult> => {
			const def = this.tools.get(c.toolName);
			if (!def) {
				this.ran.push(c.toolName);
				return { toolName: c.toolName, toolCallId: c.toolCallId, outcome: "ran", text: `${c.toolName} ran` };
			}
			try {
				const r = await def.execute(c.toolCallId, c.input, undefined, undefined, ctx);
				this.ran.push(c.toolName);
				return { toolName: c.toolName, toolCallId: c.toolCallId, outcome: "ran", text: r?.content?.[0]?.text ?? "" };
			} catch (e) {
				return { toolName: c.toolName, toolCallId: c.toolCallId, outcome: "error", reason: (e as Error).message };
			}
		};
		const sequential = list.some((c) => this.tools.get(c.toolName)?.executionMode === "sequential");
		const results: BatchResult[] = [];
		if (sequential) {
			for (const c of list) results.push((await prepare(c)) ?? (await runOne(c)));
			return results;
		}
		const prepared: (BatchResult | undefined)[] = [];
		for (const c of list) prepared.push(await prepare(c));
		if (opts.between) await opts.between();
		return Promise.all(list.map((c, i) => (prepared[i] ? Promise.resolve(prepared[i] as BatchResult) : runOne(c))));
	}
	calls = 0;
	// ran names every tool whose execute (or, for a builtin, its stand-in)
	// ran, in order
	ran: string[] = [];
	// emitAll runs every handler in order and returns every defined result
	// (pi's runner collects each before_agent_start handler's message).
	async emitAll(name: string, event: any, ctx: any): Promise<any[]> {
		const out: any[] = [];
		for (const h of this.handlers.get(name) ?? []) {
			const r = await h(event, ctx);
			if (r !== undefined) out.push(r);
		}
		return out;
	}
	// emit runs every handler in order like pi's runner and returns the
	// last defined result.
	async emit(name: string, event: any, ctx: any): Promise<any> {
		let out: any;
		for (const h of this.handlers.get(name) ?? []) {
			const r = await h(event, ctx);
			if (r !== undefined) out = r;
		}
		return out;
	}
}

export type BatchResult = { toolName: string; toolCallId: string; outcome: "ran" | "blocked" | "error" | "not_found"; reason?: string; text?: string };

export function fakeCtx(over: Record<string, unknown> = {}): any {
	const statuses: Record<string, string | undefined> = {};
	const widgets: Record<string, { lines: string[] | undefined; placement?: string }> = {};
	return {
		mode: "json",
		hasUI: false,
		cwd: process.cwd(),
		signal: undefined,
		model: undefined,
		modelRegistry: { getAll: () => [] },
		isIdle: () => true,
		hasPendingMessages: () => false,
		sessionManager: { getSessionId: () => "sess-1", getBranch: () => [] },
		ui: {
			notify: () => undefined,
			setStatus: (k: string, v: string | undefined) => {
				statuses[k] = v;
			},
			confirm: async () => false,
			select: async () => undefined,
			setWidget: (k: string, lines: string[] | undefined, opts?: { placement?: string }) => {
				widgets[k] = { lines, placement: opts?.placement };
			},
		},
		statuses,
		widgets,
		...over,
	};
}

export function tmpdir(): string {
	return fs.mkdtempSync(path.join(os.tmpdir(), "amq-squad-pi-test-"));
}

// script writes an executable shell script and returns its path.
export function script(dir: string, name: string, body: string): string {
	const p = path.join(dir, name);
	fs.writeFileSync(p, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
	return p;
}

// testProof is the lead's proof as the stand-in leads compute it, written
// apart from the extension's (pi/extensions/lib/team.ts leadProof) so the
// two must agree; the fixed vector in phase.test.ts ties both to Go's.
export function testProof(nonce: string, challenge: string, r: { outcome?: string; digest?: string; team?: Record<string, string | undefined> }): string {
	const t = r.team ?? {};
	const msg = ["amq-squad.lead-proof.v1", challenge, r.outcome ?? "", r.digest ?? "", t.root ?? "", t.base_root ?? "", t.session ?? "", t.handle ?? "", t.digest ?? ""].join("\n");
	return crypto.createHmac("sha256", nonce).update(msg).digest("hex");
}
