#!/usr/bin/env node
// Hold-seam test driver (spike S5 step 3).
//
// Each scenario starts pi under `amq coop exec` in JSON mode with hold.ts loaded,
// asks the model to run one bash command that creates a marker file, and acts on
// the pending file from "another shell" (this process). The marker proves whether
// the tool actually executed; the pi event stream proves when.
//
// Usage: node hold-test.mjs [scenario ...]
//   scenarios: approve timeout throw deny mismatch   (default: all)
//   env HOLD_TEST_MODEL (default ollama/qwen2.5:32b), HOLD_TEST_APPROVE_AFTER_S (default 120)

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const ext = path.join(here, "hold.ts");
const HOME = os.homedir();
const spike = path.join(HOME, ".amq-squad/spike/s5");
const root = path.join(spike, "project/.agent-mail/collab");
const handle = "pi-hold";
const holdDir = path.join(root, "agents", handle, "extensions", "io.github.omriariav.amq-squad");
const spi = path.join(spike, "bin/spi");
const model = process.env.HOLD_TEST_MODEL ?? "ollama/qwen2.5:32b";
const approveAfterS = Number(process.env.HOLD_TEST_APPROVE_AFTER_S ?? 120);

const scenarios = {
	approve: { env: { AMQ_SQUAD_HOLD_TIMEOUT_MS: String((approveAfterS + 120) * 1000) }, act: "approve", afterS: approveAfterS },
	timeout: { env: { AMQ_SQUAD_HOLD_TIMEOUT_MS: "8000" }, act: "none" },
	throw: { env: { AMQ_SQUAD_HOLD_THROW: "1" }, act: "none" },
	deny: { env: { AMQ_SQUAD_HOLD_TIMEOUT_MS: "15000" }, act: "deny", afterS: 3 },
	mismatch: { env: { AMQ_SQUAD_HOLD_TIMEOUT_MS: "15000" }, act: "mismatch", afterS: 3 },
};
const pick = process.argv.slice(2).length ? process.argv.slice(2) : Object.keys(scenarios);

for (const name of pick) {
	const r = await run(name, scenarios[name]);
	process.stdout.write(JSON.stringify(r) + "\n");
}

async function run(name, sc) {
	const work = path.join(spike, "hold-runs", `${name}-${Date.now()}`);
	fs.mkdirSync(work, { recursive: true });
	const marker = path.join(work, `marker-${name}`);
	const prompt = `Use your bash tool to run exactly this command and nothing else: touch ${marker} && echo created. Then say whether it worked. If the tool call is blocked, do not retry; just report the block reason.`;
	const before = new Set(fs.existsSync(holdDir) ? fs.readdirSync(holdDir) : []);
	const t0 = Date.now();
	const tl = [];
	const mark = (what, extra = {}) => tl.push({ t_s: +((Date.now() - t0) / 1000).toFixed(2), what, ...extra });

	const child = spawn("amq", [
		"coop", "exec", "--root", root, "--me", handle, "--no-wake", spi, "--",
		"--mode", "json", "--no-session", "--model", model, "--tools", "bash",
		"--no-context-files", "--no-skills", "--no-prompt-templates", "-e", ext, prompt,
	], { cwd: work, env: { ...process.env, AMQ_NO_UPDATE_CHECK: "1", ...sc.env }, stdio: ["ignore", "pipe", "pipe"] });

	const events = fs.createWriteStream(path.join(work, "events.jsonl"));
	let buf = "", stderr = "", finalText = "";
	const toolEnds = [];
	child.stderr.on("data", (d) => (stderr += d));
	child.stdout.on("data", (chunk) => {
		buf += chunk;
		let i;
		while ((i = buf.indexOf("\n")) >= 0) {
			const line = buf.slice(0, i); buf = buf.slice(i + 1);
			events.write(line + "\n");
			let ev; try { ev = JSON.parse(line); } catch { continue; }
			if (ev.type === "tool_execution_start") mark("tool_execution_start", { tool: ev.toolName, marker_exists: fs.existsSync(marker) });
			if (ev.type === "tool_execution_end") {
				const text = (ev.result?.content ?? []).map((c) => c.text ?? "").join("").slice(0, 300);
				toolEnds.push({ isError: ev.isError, text });
				mark("tool_execution_end", { isError: ev.isError, text, marker_exists: fs.existsSync(marker) });
			}
			if (ev.type === "message_end" && ev.message?.role === "assistant") {
				const t = (ev.message.content ?? []).filter((c) => c.type === "text").map((c) => c.text).join("");
				if (t.trim()) finalText = t.trim();
			}
		}
	});

	// "Another shell": watch for pending files and answer per scenario.
	const answered = new Set();
	const poll = setInterval(() => {
		if (!fs.existsSync(holdDir)) return;
		for (const f of fs.readdirSync(holdDir)) {
			if (before.has(f) || !f.startsWith("pending-") || !f.endsWith(".json") || answered.has(f)) continue;
			answered.add(f);
			const pending = JSON.parse(fs.readFileSync(path.join(holdDir, f), "utf8"));
			mark("pending_seen", { id: pending.id, tool: pending.tool, command: pending.input?.command, marker_exists: fs.existsSync(marker) });
			if (sc.act === "none") continue;
			setTimeout(() => {
				const exists = fs.existsSync(marker);
				let file, body = "";
				if (sc.act === "approve") file = `approve-${pending.id}`;
				if (sc.act === "deny") file = `deny-${pending.id}`;
				if (sc.act === "mismatch") { file = `approve-${pending.id}`; body = JSON.stringify({ manifest_hash: "sha256:" + "0".repeat(64) }); }
				fs.writeFileSync(path.join(holdDir, file), body);
				mark(`wrote_${file.split("-")[0]}`, { file, marker_exists_before_answer: exists });
			}, (sc.afterS ?? 0) * 1000);
		}
	}, 100);

	const exit = await new Promise((res) => child.on("close", (code, sig) => res({ code, sig })));
	clearInterval(poll);
	events.end();
	mark("pi_exit", { ...exit });
	const resolvedLog = fs.existsSync(path.join(holdDir, "hold-log.jsonl"))
		? fs.readFileSync(path.join(holdDir, "hold-log.jsonl"), "utf8").trim().split("\n").map((l) => JSON.parse(l)).filter((r) => Date.parse(r.at) >= t0 - 1000)
		: [];
	return {
		scenario: name, model, marker_created: fs.existsSync(marker), timeline: tl,
		hold_log: resolvedLog.filter((r) => r.event !== "session_start"),
		tool_results: toolEnds, final_text: finalText.slice(-400), stderr_tail: stderr.trim().slice(-600), work,
	};
}
