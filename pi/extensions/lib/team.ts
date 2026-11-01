// The solo lead opens a team from the conversation (brief 31-3, PR 1):
//   - amq_team_propose: the lead's plan (goal, seats, files, first tasks) to
//     `amq-squad team propose --from`, which renders squad.yaml, checks it
//     against the owner grant and prints the plan with its digest; the plan
//     block below the editor shows it with the confirm command;
//   - /squad confirm <prefix>: R55's TUI door. pi hands a slash command to
//     the extension, never to the model, so only the owner's typed line in
//     the lead's pane reaches it; the extension matches the typed prefix
//     against the digest it printed and sends the digest and its pane to
//     the lead over the start socket. The lead accepts it only from the pi
//     process it spawned (its pid and start stamp) and witnesses it in its
//     own memory; no file is written. It is a command, never a tool;
//   - amq_team_apply: `amq-squad team apply --digest D`, which the lead's
//     process answers over its start socket: a confirmation it witnessed,
//     the mayor's checks, up's shared steps, the roster line;
//   - the solo lead's shell refuses tmux and amq-squad team (confirm, apply)
//     over the dequoted line (3b (ii)): the shell in the lead's pane must not
//     type into its own pane, nor run the owner's verbs.
// The switch to the team lead's tools on the owner's yes (R53) is PR 2.
import * as net from "node:net";
import { Type } from "typebox";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Config, Runner } from "./hooks.ts";
import { describe, run } from "./verbs.ts";

export const PLAN_KEY = "amq-squad-plan";
export const CONFIRM_TTL_MS = 30 * 60 * 1000;
const APPLY_TIMEOUT_MS = 11 * 60 * 1000;
const DIGEST = /^sha256:([0-9a-f]{64})@([0-9a-f]{64}|absent)$/;

type Proposal = { digest: string; prefix: string; plan: string[]; project_dir: string };
type StartReply = { outcome: string; refusal?: string; detail?: string; roster?: string[]; owner_steps?: string[][]; steps?: { name: string; outcome: string; refusal?: string; detail?: string }[] };

// digestPrefix is the 16-hex prefix the owner types (R32's length).
export function digestPrefix(digest: string): string {
	const m = DIGEST.exec(digest);
	return m ? m[1].slice(0, 16) : "";
}

// TUI_KIND is the TUI door's message on the lead's start socket.
export const TUI_KIND = "amq-squad.confirm-tui.v1";

export type LeadReply = { outcome: string; refusal?: string; detail?: string; digest?: string };

// askLead sends one request over the solo lead's start socket and reads its
// one-line reply. The lead proves the peer itself (for the TUI door: the pi
// process it spawned); this process asserts nothing it could forge.
export function askLead(socket: string, req: Record<string, unknown>, timeoutMs = 10000): Promise<LeadReply> {
	return new Promise((resolve, reject) => {
		if (!socket) {
			reject(new Error("no_lead: the lead's start socket is not set (AMQ_SQUAD_LEAD_START_SOCKET)"));
			return;
		}
		let buf = "";
		const c = net.createConnection(socket);
		const timer = setTimeout(() => {
			c.destroy();
			reject(new Error("no_lead: the lead did not answer"));
		}, timeoutMs);
		c.on("connect", () => c.write(JSON.stringify(req) + "\n"));
		c.on("data", (d: Buffer) => {
			buf += d.toString("utf8");
			const i = buf.indexOf("\n");
			if (i < 0) return;
			clearTimeout(timer);
			c.end();
			try {
				resolve(JSON.parse(buf.slice(0, i)) as LeadReply);
			} catch (err) {
				reject(err);
			}
		});
		c.on("error", (err) => {
			clearTimeout(timer);
			reject(new Error(`no_lead: ${err.message}`));
		});
	});
}

const SHELL_TOOLS = new Set(["bash", "Bash", "shell", "exec_command"]);
// AMQ_SQUAD_ALLOW are the amq-squad verbs the solo lead's shell may run,
// all read-only (the shared table pi/test/solo-shell-forms.json carries the
// same list): before the owner's yes no lead lock is held, so `up`, `lead
// --team` or `scheduler run` from its shell would start a team unconfirmed.
export const AMQ_SQUAD_ALLOW = ["status", "context", "doctor", "version", "task show", "task list", "team propose"];

// amqSquadAllowed reports whether the words after an amq-squad name an
// allowed verb (one word, or two).
export function amqSquadAllowed(words: string[]): boolean {
	return AMQ_SQUAD_ALLOW.includes(words[0] ?? "") || AMQ_SQUAD_ALLOW.includes(words.slice(0, 2).join(" "));
}
// argv[0]s that run their arguments as a command line: their argv is read
// as a shell form too.
const LINE_RUNNERS = new Set(["sh", "bash", "zsh", "dash", "ksh", "env", "eval", "exec", "xargs", "nohup", "sudo"]);

function base(word: string): string {
	return word.split("/").pop() ?? word;
}

// dequote drops what the shell removes before it runs a word: quote
// characters, backslashes and the $'...' prefix, so t""mux, t\mux,
// $'tmux' and "tm"'ux' all read tmux. Lowercased: macOS's default file
// system finds TMUX as tmux.
export function dequote(line: string): string {
	return line.replace(/\$'/g, "").replace(/["'\\]/g, "").toLowerCase();
}

// lineRefusal is the shell form's rule over the dequoted line, the same as
// the guard's second layer (internal/cli/hook_guard.go soloLeadShellRefusal).
export function lineRefusal(line: string): string | undefined {
	const d = dequote(line);
	const tokens = d.split(/[\s;&|()<>{}]+/).filter(Boolean);
	if (tokens.some((t) => t.includes("tmux"))) return "tmux";
	for (let i = 0; i < tokens.length; i++) {
		if (base(tokens[i]).includes("amq-squad") && !amqSquadAllowed(tokens.slice(i + 1))) return ("amq-squad " + tokens.slice(i + 1, i + 3).join(" ")).trim();
	}
	// a parameter expansion or a command substitution joined to letters
	// (tm${x}ux, tm$(true)ux) and backticks build a word the split cannot read
	if (/[a-z0-9_]\$[{(a-z_]/.test(d) || /\$\{[^}]*\}[a-z0-9_]/.test(d) || /\$\([^)]*\)[a-z0-9_]/.test(d) || d.includes("`")) return "an expansion joined to letters";
	const runs = tokens.includes("eval") || (tokens.includes("exec") && tokens.includes("-a")) || (tokens.includes("env") && (tokens.includes("-s") || tokens.some((t) => t.startsWith("--split-string"))));
	if (runs && tokens.some((t) => /t.*m.*u.*x/.test(t))) return "tmux";
	return undefined;
}

// soloShellRefusal is 3b (ii): the reason the solo lead's shell refuses a
// call, undefined when it runs. In an argv form (an array the tool runs
// without a shell) argv[0]'s base name decides, and an argv[0] that runs a
// command line (sh -c, env, eval, exec) has its words read as a shell form;
// in a shell form (a command string) the dequoted line decides (lineRefusal).
export function soloShellRefusal(toolName: string, input: unknown): string | undefined {
	if (!SHELL_TOOLS.has(toolName) || !input || typeof input !== "object") return undefined;
	const o = input as Record<string, unknown>;
	const argv = [o.argv, o.args, o.cmd, o.command].find((v) => Array.isArray(v)) as unknown[] | undefined;
	if (argv) {
		const words = argv.map((w) => String(w).toLowerCase());
		const cmd = base(words[0] ?? "");
		if (cmd.includes("tmux")) return refusalText("tmux");
		if (cmd.includes("amq-squad") && !amqSquadAllowed(words.slice(1))) return refusalText(("amq-squad " + words.slice(1, 3).join(" ")).trim());
		const inner = LINE_RUNNERS.has(cmd) ? lineRefusal(words.slice(1).join(" ")) : undefined;
		return inner ? refusalText(inner) : undefined;
	}
	const line = [o.command, o.cmd].find((v) => typeof v === "string") as string | undefined;
	if (line === undefined) return undefined;
	const why = lineRefusal(line);
	return why ? refusalText(why) : undefined;
}

function refusalText(what: string): string {
	return `owner_only: the solo lead's shell does not run ${what} (R55): a tmux command can type into the lead's own pane, and only the read-only amq-squad verbs run here (${AMQ_SQUAD_ALLOW.join(", ")}). Propose with amq_team_propose; the owner confirms at /squad confirm or the Buzz DM; then amq_team_apply.`;
}

function envelopeData<T>(stdout: string): T | undefined {
	try {
		return (JSON.parse(stdout) as { data: T }).data;
	} catch {
		return undefined;
	}
}

// SoloTeam is the solo lead's plan block, its two tools and its command.
export class SoloTeam {
	cfg: Config;
	runner: Runner;
	now: () => Date;
	proposal: Proposal | undefined;
	confirmed: { at: Date } | undefined;
	socket: string;
	line = "";
	ctx: ExtensionContext | undefined;

	constructor(cfg: Config, runner: Runner = run, now: () => Date = () => new Date(), socket: string = process.env.AMQ_SQUAD_LEAD_START_SOCKET ?? "") {
		this.cfg = cfg;
		this.socket = socket;
		this.runner = runner;
		this.now = now;
	}

	project(): string {
		return this.cfg.project || process.env.AMQ_SQUAD_PROJECT || process.cwd();
	}

	// planLines is the plan block: the plan, the digest and its prefix, and
	// the three doors.
	planLines(): string[] {
		const p = this.proposal;
		if (!p) return [];
		const out = ["amq-squad: proposed team", ...p.plan.map((l) => "  " + l), `  digest ${p.digest}`, `  prefix ${p.prefix}`];
		if (this.confirmed) out.push(`  confirmed at ${this.confirmed.at.toISOString()} (door tui, witnessed by the lead); the lead applies it with amq_team_apply`);
		else out.push(`  confirm: /squad confirm ${p.prefix}  (or the signed Buzz DM "yes ${p.prefix}")`);
		if (this.line) out.push(`  ${this.line}`);
		return out;
	}

	render(): void {
		const ui = this.ctx?.hasUI ? this.ctx.ui : undefined;
		if (!ui) return;
		try {
			ui.setWidget(PLAN_KEY, this.proposal ? this.planLines() : undefined, { placement: "belowEditor" });
		} catch {
			// UI is optional
		}
	}

	async propose(plan: unknown, signal?: AbortSignal): Promise<string> {
		const r = await this.runner(this.cfg.bin, ["team", "propose", "--from", "/dev/stdin", "--project", this.project(), "--json"], { stdin: JSON.stringify(plan), timeoutMs: Math.max(this.cfg.hookTimeoutMs, 30000), signal });
		if (r.spawnError || r.timedOut || r.code !== 0) throw new Error(`team propose refused (${describe(r, 30000)})${r.stdout.trim() ? `: ${r.stdout.trim().slice(0, 2000)}` : ""}`);
		const d = envelopeData<Proposal & { doors: string }>(r.stdout);
		if (!d || !digestPrefix(d.digest)) throw new Error("team propose printed no digest");
		this.proposal = { digest: d.digest, prefix: d.prefix, plan: d.plan ?? [], project_dir: d.project_dir };
		this.confirmed = undefined;
		this.line = "";
		this.render();
		return [...(d.plan ?? []), `digest ${d.digest}`, `prefix ${d.prefix}`, d.doors].join("\n");
	}

	async apply(digest: string, signal?: AbortSignal): Promise<string> {
		if (!DIGEST.test(digest)) throw new Error("digest is the sha256:...@... token amq_team_propose printed");
		const r = await this.runner(this.cfg.bin, ["team", "apply", "--digest", digest, "--project", this.project(), "--json"], { timeoutMs: APPLY_TIMEOUT_MS, signal });
		const d = envelopeData<StartReply>(r.stdout);
		if (!d) throw new Error(`team apply refused (${describe(r, APPLY_TIMEOUT_MS)})`);
		const lines = (d.steps ?? []).map((s) => `${s.name} ${s.outcome}${s.refusal ? " " + s.refusal : ""}${s.detail ? ": " + s.detail : ""}`);
		if (d.outcome !== "started") {
			const owner = (d.owner_steps ?? []).map((s) => "owner runs: " + s.join(" "));
			throw new Error([`${d.refusal ?? "refused"}: ${d.detail ?? ""}`, ...lines, ...owner].join("\n"));
		}
		this.line = `team started: ${d.detail ?? ""}`;
		this.render();
		return [...lines, ...(d.roster ?? []), `team started: ${d.detail ?? ""}`].join("\n");
	}

	// confirm is the TUI door: the owner's typed prefix against the printed
	// digest; a match goes to the lead over its start socket, which accepts
	// it only from the pi process it spawned and witnesses it. No file is
	// written.
	async confirm(typed: string, ctx: ExtensionContext): Promise<{ refused: boolean; line: string }> {
		const p = this.proposal;
		if (!p) return { refused: true, line: "no_proposal: the lead has printed no plan to confirm" };
		let prefix = typed.trim();
		if (!prefix && ctx.hasUI) prefix = ((await ctx.ui.input(`confirm the team: type the prefix ${p.prefix}`)) ?? "").trim();
		if (!prefix) return { refused: false, line: "cancelled, nothing sent" };
		if (prefix.toLowerCase() !== p.prefix) return { refused: true, line: `prefix_mismatch: ${JSON.stringify(prefix)} is not the printed prefix ${p.prefix}; nothing sent` };
		let r: LeadReply;
		try {
			r = await askLead(this.socket, { kind: TUI_KIND, digest: p.digest, pane: process.env.TMUX_PANE ?? "" });
		} catch (err) {
			return { refused: true, line: String((err as Error).message ?? err) };
		}
		if (r.outcome !== "witnessed") return { refused: true, line: `${r.refusal ?? "refused"}: ${r.detail ?? ""}` };
		const at = this.now();
		this.confirmed = { at };
		return { refused: false, line: `confirmed ${p.prefix} at ${at.toISOString()}, witnessed by the lead; it stands for 30 minutes` };
	}
}

// installSoloTeam gives the solo lead the two tools, the plan block and
// the /squad command (confirm).
export function installSoloTeam(pi: ExtensionAPI, cfg: Config, runner: Runner = run, now: () => Date = () => new Date(), socket?: string): SoloTeam {
	const team = new SoloTeam(cfg, runner, now, socket);
	pi.on("session_start", (_e, ctx) => {
		team.ctx = ctx;
		team.render();
	});
	pi.registerTool({
		name: "amq_team_propose",
		label: "Propose a team",
		description:
			"Propose a team for this repository from your plan: the goal, the session name, the lead (one of the seats), the seats (handle, role, runtime, model, effort), " +
			"the files the work touches and the first tasks. It renders squad.yaml, checks it against the owner's grant, and returns the plan with its digest and prefix. " +
			"Nothing is written but the proposal. The owner confirms it (you cannot); then call amq_team_apply with the digest.",
		promptSnippet: "amq_team_propose: propose a team (plan in, digest out)",
		parameters: Type.Object({
			goal: Type.String(),
			session: Type.String({ description: "the AMQ session name (lowercase, digits, - or _)" }),
			lead: Type.String({ description: "the seat handle that leads" }),
			seats: Type.Array(
				Type.Object({
					handle: Type.String(),
					role: Type.Optional(Type.String()),
					runtime: Type.String({ description: "claude | codex | pi | cursor-agent | grok" }),
					model: Type.Optional(Type.String()),
					effort: Type.Optional(Type.String()),
				}),
			),
			files: Type.Optional(Type.Array(Type.String())),
			tasks: Type.Optional(Type.Array(Type.Object({ title: Type.String(), class: Type.Optional(Type.String()), files: Type.Optional(Type.Array(Type.String())) }))),
		}),
		async execute(_id, p, signal) {
			return { content: [{ type: "text", text: await team.propose(p, signal) }], details: undefined };
		},
	});
	pi.registerTool({
		name: "amq_team_apply",
		label: "Apply the team",
		description:
			"Apply a team the owner confirmed: give the digest amq_team_propose printed. It is refused until the owner confirms (at the lead's pane, the owner's shell or Buzz). " +
			"On success squad.yaml is written, the team starts and the roster is returned.",
		promptSnippet: "amq_team_apply: start the confirmed team (digest in, roster out)",
		parameters: Type.Object({ digest: Type.String() }),
		async execute(_id, p, signal) {
			return { content: [{ type: "text", text: await team.apply(p.digest, signal) }], details: undefined };
		},
	});
	pi.registerCommand("squad", {
		description: "The proposed team: /squad shows the plan block; /squad confirm <prefix> is the owner's yes",
		getArgumentCompletions: (prefix: string) => (["confirm"].filter((k) => k.startsWith(prefix.trim())).map((k) => ({ value: k, label: k }))),
		handler: async (args, ctx) => {
			team.ctx = ctx;
			const [word, ...rest] = args.trim().split(/\s+/);
			if (!word) {
				team.render();
				return;
			}
			if (word !== "confirm") {
				team.line = `unknown key ${JSON.stringify(word)}; the solo lead's key is confirm`;
				team.render();
				return;
			}
			const r = await team.confirm(rest.join(" "), ctx);
			team.line = `key confirm${r.refused ? " refused" : ""}: ${r.line}`;
			team.render();
			try {
				if (ctx.hasUI) ctx.ui.notify(team.line, r.refused ? "warning" : "info");
			} catch {
				// the line is on the plan block either way
			}
		},
	});
	return team;
}
