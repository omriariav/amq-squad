// A minimal stand-in for pi's ExtensionAPI and ExtensionContext: it records
// handlers and registered tools and lets a test emit events.
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
	registerTool(def: any): void {
		this.tools.set(def.name, def);
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
