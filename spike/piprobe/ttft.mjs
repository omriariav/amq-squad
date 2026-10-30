#!/usr/bin/env node
// Direct time-to-first-token against Ollama's OpenAI-compatible streaming API.
// Isolates model latency from pi startup and from Ollama's tool-call buffering
// (Ollama sends a tool call as one chunk when complete, so pi's first event on a
// tool-call turn arrives only when the whole call is generated).
//
// Fields (ms from request): first_chunk_ms is the first SSE chunk of any kind;
// first_reasoning_ms is the first non-empty `reasoning` delta (thinking models);
// first_text_ms is the first non-empty visible `content` delta. For a model that
// thinks first, first_text_ms includes the whole reasoning phase.
// Usage: node ttft.mjs <model> [runs=3] [max_tokens=64]
const [model, runsArg, maxArg] = process.argv.slice(2);
const runs = Number(runsArg ?? 3);
const maxTokens = Number(maxArg ?? 64);
for (let i = 1; i <= runs; i++) {
	const t0 = performance.now();
	const res = await fetch("http://localhost:11434/v1/chat/completions", {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({ model, stream: true, max_tokens: maxTokens, messages: [{ role: "user", content: "Name three Go standard library packages, one per line." }] }),
	});
	let first = null, firstReasoning = null, firstText = null, buf = "";
	for await (const chunk of res.body) {
		const now = performance.now() - t0;
		if (first === null) first = now;
		buf += Buffer.from(chunk).toString("utf8");
		let idx;
		while ((idx = buf.indexOf("\n")) >= 0) {
			const line = buf.slice(0, idx).trim();
			buf = buf.slice(idx + 1);
			if (!line.startsWith("data:") || line === "data: [DONE]") continue;
			let d; try { d = JSON.parse(line.slice(5)).choices?.[0]?.delta ?? {}; } catch { continue; }
			if (firstReasoning === null && (d.reasoning || d.reasoning_content)) firstReasoning = now;
			if (firstText === null && d.content) firstText = now;
		}
	}
	const r = (x) => (x === null ? null : Math.round(x));
	console.log(JSON.stringify({ model, run: i, max_tokens: maxTokens, first_chunk_ms: r(first), first_reasoning_ms: r(firstReasoning), first_text_ms: r(firstText), total_ms: r(performance.now() - t0) }));
}
