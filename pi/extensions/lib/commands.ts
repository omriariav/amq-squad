// The lead's commands (brief 3.1.1-3): one pi command per action, each
// named for its verb, never a subcommand of /squad and never a pi built-in
// name. The team phase: /add (a task or a seat), /reset, /dismiss (the
// 3.1.0 clear key), /answer and /widen. The solo phase: /plan (the proposed
// plan again) and /confirm (the owner's yes, through pi's confirm dialog).
// Both phases: /team shows the screen and /team hide hides it. A bare
// /squad stays one release as a pointer that lists the phase's commands and
// toggles nothing; it goes in 3.2. pi hands a command to the extension and
// never to the model, and no command is a tool. The verbs each one runs are
// the 3.1.0 keys' (task add, task reset, scheduler clear, gate show, team
// apply's confirm), and task widen for /widen.
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { ADD_KINDS, type KeyOutcome } from "./keys.ts";
import type { Screen } from "./screen.ts";
import type { SoloTeam } from "./team.ts";

export type Phase = "solo" | "team";

// listed is how a command shows on the screen's last line ("" for one
// that is not listed: the /squad pointer).
type CommandDef = { description: string; phases: Phase[]; listed: string };

// COMMANDS is every command the extension registers, in the order the
// screen's last line lists them; each description is one plain line saying
// what it does.
export const COMMANDS: Record<string, CommandDef> = {
	plan: { description: "show the proposed team plan again", phases: ["solo"], listed: "/plan" },
	confirm: { description: "confirm the proposed team plan: a dialog quotes it and your yes starts nothing but that plan", phases: ["solo"], listed: "/confirm" },
	add: { description: "add a task or a seat to the running team", phases: ["team"], listed: "/add" },
	reset: { description: "send a blocked task back to the queue, on its pool or another", phases: ["team"], listed: "/reset" },
	dismiss: { description: "acknowledge a seat's warning line (the seat itself does not change)", phases: ["team"], listed: "/dismiss" },
	answer: { description: "show an open owner gate's question and where to answer it", phases: ["team"], listed: "/answer" },
	widen: { description: "accept a seat's request to touch files outside its task", phases: ["team"], listed: "/widen" },
	team: { description: "show the team screen; /team hide hides it", phases: ["solo", "team"], listed: "/team hide" },
	squad: { description: "the old name: lists the commands you can use now (it goes in 3.2)", phases: ["solo", "team"], listed: "" },
};

// phaseCommands is what the screen's last line lists in a phase.
export function phaseCommands(phase: Phase): string[] {
	return Object.values(COMMANDS)
		.filter((c) => c.listed && c.phases.includes(phase))
		.map((c) => c.listed);
}

// commandsLine is the screen's last line in a phase.
export function commandsLine(phase: Phase): string {
	return phaseCommands(phase).join("  ");
}

const TEAM_KEYS = ["add", "reset", "dismiss", "answer", "widen"];

// notify says a line in pi's notice area when pi has a UI.
function notify(ctx: ExtensionContext, line: string, refused: boolean): void {
	try {
		if (ctx.hasUI) ctx.ui.notify(line, refused ? "warning" : "info");
	} catch {
		// the line is on the screen either way
	}
}

export type CommandHost = {
	// the phases this pi can be in: a lead started with --team is only ever
	// the team's; the solo lead may become it on the owner's yes (R53)
	phases: Phase[];
	phase: () => Phase;
	screen?: Screen;
	solo?: SoloTeam;
};

// installCommands registers every command the host's phases use; a command
// of the other phase refuses in plain words, naming the commands of now.
export function installCommands(pi: ExtensionAPI, host: CommandHost): string[] {
	const names = Object.keys(COMMANDS).filter((n) => COMMANDS[n].phases.some((p) => host.phases.includes(p)));
	// show leaves an outcome on whichever screen the phase has
	const show = (o: KeyOutcome, ctx: ExtensionContext) => {
		if (host.phase() === "team" && host.screen) host.screen.show(o, ctx);
		else if (host.solo) host.solo.show(o, ctx);
		else notify(ctx, o.line, o.refused);
	};
	const notNow = (name: string): KeyOutcome => {
		const phase = host.phase();
		const why = phase === "solo" ? `There is no team yet, so /${name} has nothing to act on.` : `The team is already running, so there is no plan for /${name}.`;
		return { key: name, refused: true, line: `${why} The commands you can use now: ${commandsLine(phase)}`, record: `phase_${phase}: /${name}` };
	};
	for (const name of names) {
		const def = COMMANDS[name];
		pi.registerCommand(name, {
			description: def.description,
			getArgumentCompletions: name === "add" ? (prefix: string) => ADD_KINDS.filter((k) => k.startsWith(prefix.trim())).map((k) => ({ value: k, label: k })) : name === "team" ? (prefix: string) => (["hide"].filter((k) => k.startsWith(prefix.trim())).map((k) => ({ value: k, label: k }))) : undefined,
			handler: async (args, ctx) => {
				const arg = args.trim();
				if (host.screen) host.screen.ctx = ctx;
				if (host.solo) host.solo.ctx = ctx;
				const phase = host.phase();
				if (name === "squad") {
					const head = arg ? `There is no /squad ${arg.split(/\s+/)[0]} any more: each action is its own command.` : "/squad is retired: each action is its own command.";
					show({ key: "squad", refused: Boolean(arg), line: `${head} The commands you can use now: ${commandsLine(phase)} (/squad goes in 3.2)`, record: arg ? `unknown_command: squad ${arg}` : undefined }, ctx);
					return;
				}
				if (name === "team") {
					if (arg && arg !== "hide") {
						show({ key: "team", refused: true, line: "There is no such /team command: /team shows the screen and /team hide hides it.", record: `unknown_argument: team ${arg}` }, ctx);
						return;
					}
					const on = arg !== "hide";
					if (phase === "team" && host.screen) host.screen.setExpanded(on);
					else host.solo?.setExpanded(on);
					return;
				}
				if (!def.phases.includes(phase)) {
					show(notNow(name), ctx);
					return;
				}
				if (TEAM_KEYS.includes(name)) {
					if (!host.screen) return;
					await host.screen.press(name, ctx, arg);
					return;
				}
				if (!host.solo) return;
				if (name === "plan") {
					host.solo.setExpanded(true);
					return;
				}
				// confirm: nothing to type; pi's dialogs quote the plan
				const o = await host.solo.confirm(ctx);
				show(o, ctx);
			},
		});
	}
	return names;
}
