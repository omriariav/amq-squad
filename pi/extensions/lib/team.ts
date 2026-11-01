// The solo lead opens a team from the conversation (brief 31-3, PR 1):
//   - amq_team_propose: the lead's plan (goal, seats, files, first tasks) to
//     `amq-squad team propose --from`, which renders squad.yaml, checks it
//     against the owner grant and prints the plan with its digest; the plan
//     block below the editor shows it with the confirm command;
//   - /squad confirm <prefix>: R55's TUI door. pi hands a slash command to
//     the extension, never to the model, so only the owner's typed line in
//     the lead's pane reaches it; the extension matches the typed prefix
//     against the digest it printed and writes the confirmation record from
//     its own process (proof: the pane and the pid). It is a command, never
//     a tool;
//   - amq_team_apply: `amq-squad team apply --digest D`, which the lead's
//     process answers over its start socket: the confirmation checked, the
//     mayor's checks, up's shared steps, the roster line;
//   - the solo lead's shell refuses tmux and amq-squad team (confirm, apply)
//     by argv[0]'s base name in an argv form and by token in a shell form
//     (3b (ii)): the shell in the lead's pane must not type into its own
//     pane, nor run the owner's verbs.
// The switch to the team lead's tools on the owner's yes (R53) is PR 2.
import * as fs from "node:fs";
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
type StartReply = { outcome: string; refusal?: string; detail?: string; roster?: string[]; owner_steps?: string[][]; steps?: { name: string; outcome: string; refusal?: string; detail?: string }[] };

// digestPrefix is the 16-hex prefix the owner types (R32's length).
export function digestPrefix(digest: string): string {
	const m = DIGEST.exec(digest);
	return m ? m[1].slice(0, 16) : "";
}

// confirmPath is the record's path under the solo lead's root.
export function confirmPath(root: string, digest: string): string {
	const m = DIGEST.exec(digest);
	if (!m) throw new Error(`not a team propose digest: ${digest}`);
	return path.join(root, "extensions", "amq-squad", "confirm", `${m[1]}.json`);
}

// writeConfirmRecord writes the TUI door's record by temp name and rename.
export function writeConfirmRecord(root: string, digest: string, now: Date, pane: string, pid: number): string {
	const p = confirmPath(root, digest);
	fs.mkdirSync(path.dirname(p), { recursive: true, mode: 0o700 });
	const rec = { schema: 1, digest, door: "tui", at: now.toISOString(), by: `pane ${pane || "-"}`, proof: { pane, pid } };
	const tmp = `${p}.tmp-${process.pid}`;
	fs.writeFileSync(tmp, JSON.stringify(rec, null, 2) + "\n", { mode: 0o600 });
	fs.renameSync(tmp, p);
	return p;
}

const SHELL_TOOLS = new Set(["bash", "Bash", "shell", "exec_command"]);
const OWNER_WORDS = new Set(["team", "confirm", "apply"]);

function base(word: string): string {
	return word.split("/").pop() ?? word;
}

// soloShellRefusal is 3b (ii): the reason the solo lead's shell refuses a
// call, undefined when it runs. In an argv form (an array the tool runs
// without a shell) argv[0]'s base name decides; in a shell form (a command
// string) every token does, so tmux or amq-squad anywhere in the line is
// seen, behind a pipe, a subshell, sh -c or a path.
export function soloShellRefusal(toolName: string, input: unknown): string | undefined {
	if (!SHELL_TOOLS.has(toolName) || !input || typeof input !== "object") return undefined;
	const o = input as Record<string, unknown>;
	const argv = [o.argv, o.args, o.cmd, o.command].find((v) => Array.isArray(v)) as unknown[] | undefined;
	if (argv) {
		const words = argv.map(String);
		const cmd = base(words[0] ?? "");
		if (cmd === "tmux") return refusalText("tmux");
		if (cmd === "amq-squad" && words.slice(1).some((w) => OWNER_WORDS.has(w))) return refusalText("amq-squad " + words.slice(1, 3).join(" "));
		return undefined;
	}
	const line = [o.command, o.cmd].find((v) => typeof v === "string") as string | undefined;
	if (line === undefined) return undefined;
	const tokens = line.split(/[\s;&|()<>`$'"{}\\]+/).filter(Boolean);
	if (tokens.some((t) => base(t) === "tmux")) return refusalText("tmux");
	if (tokens.some((t) => base(t) === "amq-squad") && tokens.some((t) => OWNER_WORDS.has(t))) return refusalText("amq-squad team");
	return undefined;
}

function refusalText(what: string): string {
	return `owner_only: the solo lead's shell does not run ${what} (R55): a tmux command can type into the lead's own pane, and amq-squad team confirm and apply are the owner's. Propose with amq_team_propose; the owner confirms at /squad confirm, amq-squad team confirm or the Buzz DM; then amq_team_apply.`;
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
	confirmed: { at: Date; path: string } | undefined;
	line = "";
	ctx: ExtensionContext | undefined;

	constructor(cfg: Config, runner: Runner = run, now: () => Date = () => new Date()) {
		this.cfg = cfg;
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
		if (this.confirmed) out.push(`  confirmed at ${this.confirmed.at.toISOString()} (door tui); the lead applies it with amq_team_apply`);
		else out.push(`  confirm: /squad confirm ${p.prefix}  (or amq-squad team confirm ${p.digest} in your shell, or the signed Buzz DM "yes ${p.prefix}")`);
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
	// digest; a match writes the record from this process.
	async confirm(typed: string, ctx: ExtensionContext): Promise<{ refused: boolean; line: string }> {
		const p = this.proposal;
		if (!p) return { refused: true, line: "no_proposal: the lead has printed no plan to confirm" };
		let prefix = typed.trim();
		if (!prefix && ctx.hasUI) prefix = ((await ctx.ui.input(`confirm the team: type the prefix ${p.prefix}`)) ?? "").trim();
		if (!prefix) return { refused: false, line: "cancelled, nothing written" };
		if (prefix.toLowerCase() !== p.prefix) return { refused: true, line: `prefix_mismatch: ${JSON.stringify(prefix)} is not the printed prefix ${p.prefix}; nothing written` };
		const at = this.now();
		const where = writeConfirmRecord(this.cfg.root, p.digest, at, process.env.TMUX_PANE ?? "", process.pid);
		this.confirmed = { at, path: where };
		return { refused: false, line: `confirmed ${p.prefix} at ${at.toISOString()}; it stands for 30 minutes` };
	}
}

// installSoloTeam gives the solo lead the two tools, the plan block and
// the /squad command (confirm).
export function installSoloTeam(pi: ExtensionAPI, cfg: Config, runner: Runner = run, now: () => Date = () => new Date()): SoloTeam {
	const team = new SoloTeam(cfg, runner, now);
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
