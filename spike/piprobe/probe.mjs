#!/usr/bin/env node
// R12 probe (amq-squad 3.0 phase 1, spike S5), hardened predicate (revision 2,
// after the Codex review).
//
// Drives one scripted task through pi in non-interactive JSON mode against one
// model: read a file, make a stated one-line edit, run `go test ./...`, report.
// The verdict is mechanical. A run passes only when ALL hold:
//
//   read_confirmed       the agent read calc.go: a `read` call on calc.go (or a bash
//                        cat/head/sed/less of calc.go) whose tool_execution_end
//                        exists and is not an error
//   go_test_confirmed    a bash call whose command runs `go test`, with a
//                        tool_execution_end that is not an error and whose output
//                        has an `ok` package line (a start event alone never counts)
//   worktree_exact       `git status --porcelain` lists only calc.go (no staged
//                        extras, no untracked files) and `git diff HEAD --numstat
//                        -- calc.go` is exactly 1 added, 1 removed
//   independent_go_test  `go test -count=1 ./...` run by the probe passes
//   final_line_exact     the last non-empty line of the final assistant message is
//                        exactly `RESULT: PASS`
//
// false_success = final_line_exact AND NOT (independent_go_test AND worktree_exact):
// the agent reports PASS but the verifier says FAIL.
//
// Usage:
//   node probe.mjs --model ollama/qwen2.5:32b [--runs 3] [--timeout-s 600]
//                  [--pi <pi binary>] [--out <dir>] [--fixture <dir>]
//   node probe.mjs --recheck <run dir> [<run dir> ...]   re-score finished runs
// Output: one JSON line per run on stdout, plus <out>/<model>-<n>-<ts>/{events.jsonl,result.json}.
//
// Timing fields (ms from spawn): first_any_ms is the first streamed assistant
// delta of any kind; first_reasoning_ms is the first thinking delta; first_text_ms
// the first visible text delta; first_toolcall_ms the first tool-call delta. On a
// tool-call turn Ollama sends the call as one chunk when it is complete.

import { spawn, spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const argv = process.argv.slice(2);
const gocache = path.join(os.homedir(), ".amq-squad/spike/s5/gocache");
const goEnv = { ...process.env, GOCACHE: gocache, GOFLAGS: "-count=1" };

if (argv[0] === "--recheck") {
	for (const dir of argv.slice(1)) {
		const prev = JSON.parse(fs.readFileSync(path.join(dir, "result.json"), "utf8"));
		const r = evaluate(dir, { model: prev.model, run: prev.run, total_ms: prev.total_ms, exit: prev.exit, stderr: prev.stderr_tail ?? "" });
		process.stdout.write(JSON.stringify({ ...r, recheck_of_pass_v1: prev.pass }) + "\n");
	}
	process.exit(0);
}

const args = parseArgs(argv);
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

	const piArgs = [
		"--mode", "json", "--no-session", "--model", model,
		"--tools", "read,edit,write,bash",
		"--no-context-files", "--no-skills", "--no-prompt-templates",
		PROMPT,
	];
	const events = fs.createWriteStream(path.join(dir, "events.jsonl"));
	const t0 = performance.now();
	let buf = "", stderr = "";
	const child = spawn(piBin, piArgs, { cwd: work, env: goEnv, stdio: ["ignore", "pipe", "pipe"] });
	const timer = setTimeout(() => child.kill("SIGTERM"), timeoutMs);
	child.stderr.on("data", (d) => (stderr += d));
	child.stdout.on("data", (chunk) => {
		buf += chunk.toString("utf8");
		let idx;
		while ((idx = buf.indexOf("\n")) >= 0) {
			const line = buf.slice(0, idx).replace(/\r$/, "");
			buf = buf.slice(idx + 1);
			if (line) events.write(JSON.stringify({ t_ms: Math.round(performance.now() - t0), ev: safeParse(line) }) + "\n");
		}
	});
	const exit = await new Promise((resolve) => child.on("close", (code, sig) => resolve({ code, sig })));
	clearTimeout(timer);
	await new Promise((r) => events.end(r));
	return evaluate(dir, { model, run: n, total_ms: Math.round(performance.now() - t0), exit, stderr });
}

function evaluate(dir, meta) {
	const work = path.join(dir, "work");
	const recs = fs.readFileSync(path.join(dir, "events.jsonl"), "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l));
	const calls = new Map(); // toolCallId -> {tool, args, start_ms, end_ms, isError, out}
	const order = [];
	const t = { first_any_ms: null, first_reasoning_ms: null, first_text_ms: null, first_toolcall_ms: null };
	let finalText = "", errors = [];
	for (const { t_ms, ev } of recs) {
		if (!ev) continue;
		if (ev.type === "message_update") {
			const k = ev.assistantMessageEvent?.type;
			const set = (f) => { if (t[f] === null) t[f] = t_ms; };
			if (/^(text_delta|thinking_delta|toolcall_start|toolcall_delta)$/.test(k)) set("first_any_ms");
			if (k === "thinking_delta") set("first_reasoning_ms");
			if (k === "text_delta") set("first_text_ms");
			if (k === "toolcall_start" || k === "toolcall_delta") set("first_toolcall_ms");
		}
		if (ev.type === "tool_execution_start") {
			calls.set(ev.toolCallId, { tool: ev.toolName, args: ev.args, start_ms: t_ms });
			order.push(ev.toolCallId);
		}
		if (ev.type === "tool_execution_end") {
			const c = calls.get(ev.toolCallId) ?? { tool: ev.toolName, args: null };
			Object.assign(c, { end_ms: t_ms, isError: ev.isError, out: (ev.result?.content ?? []).map((x) => x.text ?? "").join("") });
			if (!calls.has(ev.toolCallId)) { calls.set(ev.toolCallId, c); order.push(ev.toolCallId); }
		}
		if (ev.type === "message_end" && ev.message?.role === "assistant") {
			const text = (ev.message.content ?? []).filter((c) => c.type === "text").map((c) => c.text).join("");
			if (text.trim()) finalText = text;
			if (ev.message.stopReason === "error") errors.push(ev.message.errorMessage ?? "error");
		}
	}
	const ended = (c) => c.end_ms !== undefined && c.isError === false;
	const cmd = (c) => String(c.args?.command ?? "");
	const all = order.map((id) => calls.get(id));
	const readCall = all.find((c) => ended(c) && ((c.tool === "read" && /(^|\/)calc\.go$/.test(String(c.args?.path ?? ""))) ||
		(c.tool === "bash" && /\b(cat|head|sed|less)\b[^|;&]*calc\.go/.test(cmd(c)))));
	const goTestCall = all.find((c) => c.tool === "bash" && /\bgo\s+test\b/.test(cmd(c)) && ended(c) && /^ok\s/m.test(c.out ?? ""));

	const test = spawnSync("go", ["test", "-count=1", "./..."], { cwd: work, env: goEnv, encoding: "utf8" });
	const porcelain = git(work, "status", "--porcelain", "--untracked-files=all").split("\n").filter(Boolean);
	const numstat = git(work, "diff", "HEAD", "--numstat", "--", "calc.go").trim();
	const worktreeExact = porcelain.length === 1 && / calc\.go$/.test(porcelain[0]) && !porcelain[0].startsWith("??") && numstat === "1\t1\tcalc.go";
	const lines = finalText.split("\n").map((l) => l.trim()).filter(Boolean);
	const finalLineExact = lines.length > 0 && lines[lines.length - 1] === "RESULT: PASS";
	const finalLineFail = lines.length > 0 && lines[lines.length - 1] === "RESULT: FAIL";

	const checks = {
		read_confirmed: !!readCall,
		go_test_confirmed: !!goTestCall,
		worktree_exact: worktreeExact,
		independent_go_test: test.status === 0,
		final_line_exact: finalLineExact,
	};
	const pass = Object.values(checks).every(Boolean);
	const result = {
		predicate: "v2", model: meta.model, run: meta.run, pass,
		false_success: finalLineExact && !(checks.independent_go_test && worktreeExact),
		reported: finalLineExact ? "PASS" : finalLineFail ? "FAIL" : "none",
		checks, ...t, total_ms: meta.total_ms, exit: meta.exit,
		tools: all.map((c) => `${c.tool}${c.end_ms === undefined ? "?" : c.isError ? "!" : ""}`),
		tool_detail: all.map((c) => ({ tool: c.tool, args: JSON.stringify(c.args ?? null).slice(0, 160), isError: c.isError ?? null, out: (c.out ?? "").slice(0, 160) })),
		porcelain, numstat, final_text_tail: finalText.trim().slice(-300),
		errors, stderr_tail: String(meta.stderr ?? "").trim().slice(-500), dir,
	};
	fs.writeFileSync(path.join(dir, `result-${result.predicate}.json`), JSON.stringify({ ...result, diff: git(work, "diff", "HEAD", "-U0") }, null, 2));
	if (!fs.existsSync(path.join(dir, "result.json"))) fs.writeFileSync(path.join(dir, "result.json"), JSON.stringify(result, null, 2));
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
