#!/bin/bash
# S3 post-strip probes on the decided build (bin/embedpi-node), stripped env.
#  1. extension: a TypeScript extension loaded with -e imports a value from
#     @earendil-works/pi-coding-agent (jiti + node_modules resolution)
#  2. HTML export: save a session, then --export it (export-html assets)
#  3. image attach: @clankolas.png to a vision model (Photon WASM; the worker
#     vs in-process fallback is NOT distinguished by this probe)
# Not covered: the codemode worker, tool calls, the amq-squad pi package.
set -u
here="$(cd "$(dirname "$0")" && pwd)"
w="$HOME/.amq-squad/spike/s3/poststrip"
/bin/rm -rf "$w"; mkdir -p "$w/agent" "$w/sessions"
cp "$here/agent/settings.json" "$w/agent/"
cat > "$w/agent/models.json" <<'EOF'
{"providers":{"ollama":{"baseUrl":"http://localhost:11434/v1","api":"openai-completions","apiKey":"ollama",
 "models":[{"id":"qwen2.5:32b","contextWindow":32768,"maxTokens":256},
           {"id":"gemma3:4b","input":["text","image"],"contextWindow":32768,"maxTokens":64}]}}}
EOF
run() { (cd "$w" && /usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin TERM=xterm \
  PI_CODING_AGENT_DIR="$w/agent" S3_EXT_MARK="$w/ext-mark" "$here/bin/embedpi-node" "$@"); }
common=(--no-extensions --no-skills --no-prompt-templates --no-themes --no-context-files --no-mcp --no-tools --thinking off)

echo "== 1. extension"
run --provider ollama --model qwen2.5:32b --no-session "${common[@]}" -e "$here/probe-ext/s3-probe.ts" \
  -p 'What is 17 plus 25? Reply with just the number.'
echo "exit=$?"; echo "mark: $(cat "$w/ext-mark" 2>&1)"

echo "== 2. HTML export"
run --provider ollama --model qwen2.5:32b --session-dir "$w/sessions" "${common[@]}" \
  -p 'What is 17 plus 25? Reply with just the number.'
echo "exit=$?"
sess="$(/usr/bin/find "$w/sessions" -name '*.jsonl' | head -1)"
run --export "$sess" "$w/out.html"
echo "exit=$? html_bytes=$(/usr/bin/wc -c < "$w/out.html" 2>/dev/null) has_marked=$(grep -c 'marked' "$w/out.html" 2>/dev/null)"

echo "== 3. image attach"
rt="$(/usr/bin/find "$HOME/.amq-squad/runtime" -maxdepth 1 -name 'pi-1.3.0-node-*-stripped-*' | head -1)"
img="$rt/pi/node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/assets/clankolas.png"
run --provider ollama --model gemma3:4b --no-session "${common[@]}" \
  -p "@$img" 'Is this an image? Answer yes or no.'
echo "exit=$?"
/bin/rm -rf "$w"
