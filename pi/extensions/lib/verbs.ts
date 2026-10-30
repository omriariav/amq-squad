// Running the amq-squad hook verbs and the amq CLI from the pi package.
//
// Every child runs with an absolute path (AMQ_SQUAD_BIN, AMQ_SQUAD_AMQ set by
// the launcher), no shell, a bounded time and bounded output. A child that
// cannot start, times out, or exits non-zero is reported as such; the callers
// decide what that means (guard fails closed, R36).
import { spawn } from "node:child_process";

export type RunResult = {
	code: number | null; // exit code; null when the child did not exit normally
	stdout: string;
	stderr: string;
	timedOut: boolean;
	spawnError: string; // "" when the child started
	ms: number;
};

export type RunOptions = {
	stdin?: string;
	timeoutMs: number;
	env?: NodeJS.ProcessEnv;
	cwd?: string;
	signal?: AbortSignal;
};

const MAX_OUTPUT = 4 * 1024 * 1024;

export function run(bin: string, args: string[], opts: RunOptions): Promise<RunResult> {
	const started = Date.now();
	return new Promise((resolve) => {
		let stdout = "";
		let stderr = "";
		let timedOut = false;
		let settled = false;
		const done = (r: Omit<RunResult, "ms" | "stdout" | "stderr" | "timedOut">) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			opts.signal?.removeEventListener("abort", onAbort);
			resolve({ ...r, stdout, stderr, timedOut, ms: Date.now() - started });
		};
		if (!bin) {
			settled = true;
			resolve({ code: null, stdout: "", stderr: "", timedOut: false, spawnError: "no executable configured", ms: 0 });
			return;
		}
		let child: ReturnType<typeof spawn>;
		try {
			child = spawn(bin, args, { cwd: opts.cwd, env: opts.env ?? process.env, stdio: ["pipe", "pipe", "pipe"] });
		} catch (err) {
			settled = true;
			resolve({ code: null, stdout: "", stderr: "", timedOut: false, spawnError: String(err), ms: Date.now() - started });
			return;
		}
		const timer = setTimeout(() => {
			timedOut = true;
			child.kill("SIGKILL");
		}, opts.timeoutMs);
		const onAbort = () => child.kill("SIGKILL");
		opts.signal?.addEventListener("abort", onAbort);
		child.stdout?.on("data", (d: Buffer) => {
			if (stdout.length < MAX_OUTPUT) stdout += d.toString("utf8");
		});
		child.stderr?.on("data", (d: Buffer) => {
			if (stderr.length < MAX_OUTPUT) stderr += d.toString("utf8");
		});
		child.on("error", (err) => done({ code: null, spawnError: err.message }));
		child.on("close", (code) => done({ code, spawnError: "" }));
		child.stdin?.on("error", () => {
			// A child that exits before reading stdin; its exit code decides.
		});
		child.stdin?.end(opts.stdin ?? "");
	});
}

// describe is one line naming why a child failed, for block reasons and logs.
export function describe(r: RunResult, timeoutMs: number): string {
	if (r.spawnError) return `could not start: ${r.spawnError}`;
	if (r.timedOut) return `timed out after ${timeoutMs} ms`;
	if (r.code !== 0) return `exit ${r.code === null ? "signal" : r.code}${r.stderr.trim() ? `: ${firstLine(r.stderr)}` : ""}`;
	return "ok";
}

export function firstLine(s: string): string {
	return s.trim().split("\n")[0].slice(0, 300);
}
