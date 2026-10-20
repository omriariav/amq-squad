#!/usr/bin/env node
// Direct time-to-first-token against Ollama's OpenAI-compatible streaming API.
// Isolates model latency from pi startup and from Ollama's tool-call buffering
// (Ollama sends a tool call as one chunk when complete, so pi's first event on a
// tool-call turn arrives only when the whole call is generated).
// Usage: node ttft.mjs <model> [runs=3]
const [model, runsArg] = process.argv.slice(2);
const runs = Number(runsArg ?? 3);
for (let i = 1; i <= runs; i++) {
	const t0 = performance.now();
	const res = await fetch("http://localhost:11434/v1/chat/completions", {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({ model, stream: true, max_tokens: 64, messages: [{ role: "user", content: "Name three Go standard library packages, one per line." }] }),
	});
	let first = null, firstContent = null, buf = "";
	for await (const chunk of res.body) {
		const now = performance.now() - t0;
		if (first === null) first = now;
		buf += Buffer.from(chunk).toString("utf8");
		if (firstContent === null && /"(content|reasoning)":"[^"]/.test(buf)) firstContent = now;
	}
	const total = performance.now() - t0;
	console.log(JSON.stringify({ model, run: i, first_chunk_ms: Math.round(first), first_token_ms: Math.round(firstContent ?? -1), total_ms: Math.round(total) }));
}
