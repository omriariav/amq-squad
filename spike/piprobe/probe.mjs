#!/usr/bin/env node
// R12 probe (amq-squad 3.0 phase 1, spike S5).
//
// Drives one scripted task through pi in non-interactive JSON mode against one
// model: read a file, make a stated one-line edit, run `go test ./...`, report.
// The verdict is mechanical and never trusts the model's own report alone:
//
//   pass = independent `go test ./...` passes after the run
//        AND the diff is exactly one changed line, in calc.go only
//        AND the agent itself ran `go test` through its bash tool
//        AND the agent's final text ends with "RESULT: PASS"
//
// Usage: node probe.mjs --model ollama/qwen2.5:32b [--runs 3] [--timeout-s 600]
//                      [--pi <pi binary>] [--out <dir>] [--fixture <dir>]
// Output: one JSON line per run on stdout, plus <out>/<model>-<n>/{events.jsonl,result.json}.

import { spawn, spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const args = parseArgs(process.argv.slice(2));
const model = args.model ?? die("--model is required");
const runs = Number(args.runs ?? 1);
const timeoutMs = Number(args["timeout-s"] ?? 600) * 1000;
const piBin = args.pi ?? path.join(os.homedir(), ".amq-squad/spike/s5/bin/spi");
const outRoot = args.out ?? path.join(os.homedir(), ".amq-squad/spike/s5/probe-runs");
const fixture = args.fixture ?? path.join(here, "fixture");

const PROMPT = [
	"You are working in a Go module in the current directory.",
	"1. Read the file calc.go.",
	'2. Make exactly one one-line edit: change the return value of Greeting from "hello" to "hello, world". Change nothing else.',
	"3. Run the command `go test ./...` with your bash tool.",
	"4. Report the last line of the test output, then end your reply with a line that is exactly RESULT: PASS if the tests passed, or RESULT: FAIL otherwise.",
].join("\n");

for (let i = 1; i <= runs; i++) {
	const result = await runOnce(i);
	process.stdout.write(JSON.stringify(result) + "\n");
}

async function runOnce(n) {
	const slug = model.replace(/[^A-Za-z0-9._-]+/g, "_");
	const dir = path.join(outRoot, `${slug}-${n}-${Date.now()}`);
	const work = path.join(dir, "work");
	fs.mkdirSync(work, { recursive: true });
	fs.cpSync(fixture, work, { recursive: true });
	git(work, "init", "-q");
	git(work, "add", "-A");
	git(work, "-c", "user.name=probe", "-c", "user.email=probe@local", "commit", "-qm", "fixture");

	const env = {
		...process.env,
		GOCACHE: path.join(os.homedir(), ".amq-squad/spike/s5/gocache"),
		GOFLAGS: "-count=1",
	};
	const piArgs = [
		"--mode", "json", "--no-session", "--model", model,
		"--tools", "read,edit,write,bash",
		"--no-context-files", "--no-skills", "--no-prompt-templates",
		PROMPT,
	];
	const events = fs.createWriteStream(path.join(dir, "events.jsonl"));
	const t0 = performance.now();
	const marks = { assistantStart: null, firstToken: null, firstText: null };
	const tools = [];
	let finalText = "";
	let buf = "";
	let stderr = "";
	let errors = [];

	const child = spawn(piBin, piArgs, { cwd: work, env, stdio: ["ignore", "pipe", "pipe"] });
	const timer = setTimeout(() => child.kill("SIGTERM"), timeoutMs);
	child.stderr.on("data", (d) => (stderr += d));
	child.stdout.on("data", (chunk) => {
		buf += chunk.toString("utf8");
		let idx;
		while ((idx = buf.indexOf("\n")) >= 0) {
			const line = buf.slice(0, idx).replace(/\r$/, "");
			buf = buf.slice(idx + 1);
			if (!line) continue;
			const at = performance.now() - t0;
			events.write(JSON.stringify({ t_ms: Math.round(at), ev: safeParse(line) }) + "\n");
			const ev = safeParse(line);
			if (!ev) continue;
			if (ev.type === "message_start" && ev.message?.role === "assistant" && marks.assistantStart === null) marks.assistantStart = at;
			if (ev.type === "message_update") {
				const t = ev.assistantMessageEvent?.type;
				if (marks.firstToken === null && /^(text_delta|thinking_delta|toolcall_start|toolcall_delta)$/.test(t)) marks.firstToken = at;
				if (marks.firstText === null && t === "text_delta") marks.firstText = at;
			}
			if (ev.type === "tool_execution_start") tools.push({ tool: ev.toolName, args: ev.args, t_ms: Math.round(at) });
			if (ev.type === "tool_execution_end") {
				const last = [...tools].reverse().find((x) => x.tool === ev.toolName && x.isError === undefined);
				if (last) last.isError = ev.isError;
			}
			if (ev.type === "message_end" && ev.message?.role === "assistant") {
				const text = (ev.message.content ?? []).filter((c) => c.type === "text").map((c) => c.text).join("");
				if (text.trim()) finalText = text;
				if (ev.message.stopReason === "error") errors.push(ev.message.errorMessage ?? "error");
			}
		}
	});
	const exit = await new Promise((resolve) => child.on("close", (code, sig) => resolve({ code, sig })));
	clearTimeout(timer);
	events.end();
	const totalMs = performance.now() - t0;

	// Mechanical verification, independent of the model.
	const test = spawnSync("go", ["test", "./..."], { cwd: work, env, encoding: "utf8" });
	const numstat = git(work, "diff", "--numstat").trim().split("\n").filter(Boolean);
	const diff = git(work, "diff", "-U0");
	const oneLine = numstat.length === 1 && /^1\t1\tcalc\.go$/.test(numstat[0]);
	const ranGoTest = tools.some((t) => t.tool === "bash" && /go\s+test/.test(String(t.args?.command ?? "")));
	const readFile = tools.some((t) => t.tool === "read" && /calc\.go/.test(JSON.stringify(t.args ?? {})));
	const reportedPass = /RESULT:\s*PASS\s*$/.test(finalText.trim());
	const pass = test.status === 0 && oneLine && ranGoTest && reportedPass;

	const result = {
		model, run: n, pass,
		checks: { independent_go_test: test.status === 0, one_line_diff: oneLine, ran_go_test: ranGoTest, read_file: readFile, reported_pass: reportedPass },
		ttft_ms: marks.firstToken === null ? null : Math.round(marks.firstToken),
		ttft_text_ms: marks.firstText === null ? null : Math.round(marks.firstText),
		first_assistant_start_ms: marks.assistantStart === null ? null : Math.round(marks.assistantStart),
		total_ms: Math.round(totalMs),
		exit, timed_out: exit.sig === "SIGTERM",
		tools: tools.map((t) => `${t.tool}${t.isError ? "!" : ""}`),
		diff_numstat: numstat, final_text_tail: finalText.trim().slice(-300),
		errors, stderr_tail: stderr.trim().slice(-500), dir,
	};
	fs.writeFileSync(path.join(dir, "result.json"), JSON.stringify({ ...result, diff }, null, 2));
	return result;
}

function git(cwd, ...a) {
	const r = spawnSync("git", a, { cwd, encoding: "utf8" });
	if (r.status !== 0) throw new Error(`git ${a.join(" ")}: ${r.stderr}`);
	return r.stdout;
}
function safeParse(s) { try { return JSON.parse(s); } catch { return null; } }
function die(m) { console.error(m); process.exit(2); }
function parseArgs(a) {
	const o = {};
	for (let i = 0; i < a.length; i++) if (a[i].startsWith("--")) o[a[i].slice(2)] = a[i + 1]?.startsWith("--") ? true : a[++i];
	return o;
}
