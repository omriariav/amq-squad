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
import * as fs from "node:fs";
import * as net from "node:net";
import * as path from "node:path";
import { Type } from "typebox";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Config, Runner } from "./hooks.ts";
import { describe, run } from "./verbs.ts";

export const PLAN_KEY = "amq-squad-plan";
export const CONFIRM_TTL_MS = 30 * 60 * 1000;
const APPLY_TIMEOUT_MS = 11 * 60 * 1000;
const DIGEST = /^sha256:([0-9a-f]{64})@([0-9a-f]{64}|absent)$/;

type Proposal = { digest: string; prefix: string; plan: string[]; project_dir: string };
// TeamIdentity is the team lead's identity the lead's process returns
// (internal/cli/lead_start.go leadTeam): in a start's reply and a phase
// query's.
export type TeamIdentity = { root: string; base_root?: string; session?: string; handle: string; digest?: string };

// PHASE_KIND asks the lead whether it started its team.
export const PHASE_KIND = "amq-squad.phase.v1";
// RESUME_MESSAGE is the restarted lead's offer, said once on the first turn.
export const RESUME_MESSAGE = "amq-squad-resume";
type StartReply = { outcome: string; refusal?: string; detail?: string; roster?: string[]; owner_steps?: string[][]; team?: TeamIdentity; steps?: { name: string; outcome: string; refusal?: string; detail?: string }[] };

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
	// batch is this turn's assistant batch (Codex on #837, item 3): every
	// tool call of the assistant message (from its end) and every call the
	// tool_call hooks prepared since the turn began, by id. pi prepares a
	// batch's calls before running any, so amq_team_apply runs only alone.
	batch = new Map<string, string>();

	// phaseRefusal is the execute-time recheck of a solo tool (Codex on
	// #837, item 3): a call admitted while solo whose execute runs after
	// the switch refuses phase_team, as the hook would have.
	phaseRefusal(tool: string): string | undefined {
		if (this.phase !== "team") return undefined;
		return `phase_team: ${tool} is not a team lead's tool; after the owner's yes the lead dispatches through the task store (${this.teamTools.join(", ")})`;
	}

	// notAlone refuses amq_team_apply batched with any other call: the
	// model must call it alone, so nothing it admitted while solo is in
	// flight when the tools switch.
	notAlone(id: string): string | undefined {
		const others = [...this.batch].filter(([k]) => k !== id).map(([, name]) => name);
		if (!others.length) return undefined;
		return `apply_not_alone: amq_team_apply runs alone, and this batch also carried ${others.join(", ")}; nothing was started. Call amq_team_apply again on your next turn with nothing else in the batch.`;
	}

	// R53 (brief 31-3 PR 2): the phase, the team lead's tool list the switch
	// activates, and the solo root the lead writes its phase marker in
	phase: "solo" | "team" = "solo";
	teamTools: string[];
	soloRoot: string;
	pi: ExtensionAPI | undefined;

	constructor(cfg: Config, runner: Runner = run, now: () => Date = () => new Date(), socket: string = process.env.AMQ_SQUAD_LEAD_START_SOCKET ?? "", teamTools: string[] = [], pi?: ExtensionAPI) {
		this.cfg = cfg;
		this.socket = socket;
		this.runner = runner;
		this.now = now;
		this.teamTools = teamTools;
		this.soloRoot = cfg.root;
		this.pi = pi;
	}

	// markerInfo reads the lead's phase marker as information only (the
	// plan reviewer: the marker is never authority, a model with write can
	// plant it): a regular file for this project, read for the restart's
	// offer and nothing else.
	markerInfo(): { at: string; digest: string } | undefined {
		const p = path.join(this.soloRoot, "extensions", "amq-squad", "phase.json");
		try {
			if (!fs.lstatSync(p).isFile()) return undefined;
			const m = JSON.parse(fs.readFileSync(p, "utf8")) as { schema?: number; phase?: string; project?: string; at?: string; digest?: string };
			if (m.schema !== 1 || m.phase !== "team" || m.project !== this.project()) return undefined;
			return { at: String(m.at ?? "").replace(/\.\d+Z$/, "Z"), digest: String(m.digest ?? "") };
		} catch {
			return undefined;
		}
	}

	// askPhase asks the lead over its start socket whether it started its
	// team (a session started again in the same lead after the yes).
	async askPhase(): Promise<TeamIdentity | undefined> {
		if (!this.socket) return undefined;
		try {
			const r = (await askLead(this.socket, { kind: PHASE_KIND })) as LeadReply & { team?: TeamIdentity };
			return r.outcome === "team" ? r.team : undefined;
		} catch {
			return undefined;
		}
	}

	// switchToTeam is R53 by mechanism, on the lead's own reply over its
	// start socket (the start's, or a phase query's), never on a file: the
	// lead's identity becomes the squad's (AM_ROOT, AM_BASE_ROOT, AM_SESSION, AM_ME, for this process
	// and every verb it runs), and the tool list declared to the model
	// becomes the team lead's (pi.setActiveTools): the solo set (read, bash,
	// edit, write, amq_team_propose, amq_team_apply) is no longer declared
	// or callable, and the tool_call hook refuses any of it phase_team.
	switchToTeam(m: TeamIdentity): void {
		if (this.phase === "team" || !path.isAbsolute(m.root ?? "") || !m.handle) return;
		Object.assign(process.env, { AM_ROOT: m.root, AM_BASE_ROOT: m.base_root ?? path.dirname(m.root), AM_SESSION: m.session ?? "", AM_ME: m.handle, AMQ_SQUAD_LEAD: "1" });
		delete process.env.AMQ_SQUAD_SOLO_LEAD;
		Object.assign(this.cfg, { root: m.root, me: m.handle, session: m.session ?? "", lead: true, solo: false });
		this.phase = "team";
		this.pi?.setActiveTools([...this.teamTools]);
		this.line = `team phase: the lead is ${m.handle} of ${m.root}; its tools are the team lead's`;
		this.render();
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
		// the lead's reply carries the team identity: the switch rides it
		if (d.team) this.switchToTeam(d.team);
		this.render();
		const phase = this.phase === "team" ? "team phase: your tools are now the team lead's (dispatch through the task store; no shell, no edit)" : "the lead's reply carried no team identity; the tools stay as they are";
		return [...lines, ...(d.roster ?? []), `team started: ${d.detail ?? ""}`, phase].join("\n");
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
export function installSoloTeam(pi: ExtensionAPI, cfg: Config, runner: Runner = run, now: () => Date = () => new Date(), socket?: string, teamTools: string[] = []): SoloTeam {
	const team = new SoloTeam(cfg, runner, now, socket ?? process.env.AMQ_SQUAD_LEAD_START_SOCKET ?? "", teamTools, pi);
	pi.on("session_start", async (_e, ctx) => {
		team.ctx = ctx;
		// a session started again in the same lead process after the yes:
		// the lead answers whether it started its team
		const t = await team.askPhase();
		if (t) team.switchToTeam(t);
		team.render();
	});
	// this turn's batch: the assistant message's calls, and every call the
	// hooks prepare (this handler runs before the extension's guard)
	pi.on("turn_start", () => {
		team.batch.clear();
	});
	pi.on("message_end", (e: any) => {
		if (e?.message?.role !== "assistant" || !Array.isArray(e.message.content)) return undefined;
		for (const c of e.message.content) if (c?.type === "toolCall" && typeof c.id === "string") team.batch.set(c.id, String(c.name));
		return undefined;
	});
	pi.on("tool_call", (e) => {
		team.batch.set(e.toolCallId, e.toolName);
		return undefined;
	});
	// a solo lead restarted in a marked repository says, once, on its first
	// turn, the team it started and the two ways back; it starts nothing
	let offered = false;
	pi.on("before_agent_start", async () => {
		if (offered || team.phase === "team") return undefined;
		offered = true;
		const m = team.markerInfo();
		if (!m) return undefined;
		return { message: { customType: RESUME_MESSAGE, display: true, content: `This session's team was started on ${m.at} with digest ${m.digest}; run \`amq-squad up\` to resume it, or confirm it again at your pane (/squad confirm) or over Buzz.` } };
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
			const refused = team.phaseRefusal("amq_team_propose");
			if (refused) throw new Error(refused);
			return { content: [{ type: "text", text: await team.propose(p, signal) }], details: undefined };
		},
	});
	pi.registerTool({
		name: "amq_team_apply",
		label: "Apply the team",
		description:
			"Apply a team the owner confirmed: give the digest amq_team_propose printed. It is refused until the owner confirms (at the lead's pane, the owner's shell or Buzz). " +
			"On success squad.yaml is written, the team starts and the roster is returned. Call it alone, with no other tool call in the same turn.",
		promptSnippet: "amq_team_apply: start the confirmed team (digest in, roster out)",
		parameters: Type.Object({ digest: Type.String() }),
		// pi runs a batch holding a sequential tool one call at a time
		// (prepare, run, then the next), so nothing runs beside the apply
		executionMode: "sequential",
		async execute(id, p, signal) {
			const refused = team.phaseRefusal("amq_team_apply") ?? team.notAlone(id);
			if (refused) throw new Error(refused);
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
