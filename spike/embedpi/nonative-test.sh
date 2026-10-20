#!/bin/bash
# S3 probe: does one prompt still work with the only native module
# (pi-tui darwin-platform.node: AppKit clipboard + modifier keys) removed?
# Works on a throwaway copy of an unpacked runtime; the real one is untouched.
set -euo pipefail
cand="${1:-bun}"
here="$(cd "$(dirname "$0")" && pwd)"
ver="$(python3 -c 'import json,sys;m=json.load(open(sys.argv[1]));print(m["version"]+"-"+m["sha256"][:12]+" "+m["executable"]+" "+" ".join(m["args"]))' "$here/.work/manifest-$cand.json")"
read -r name exe args <<<"$ver"
src="$HOME/.amq-squad/runtime/$name"
dst="$HOME/.amq-squad/spike/s3/nonative-$cand"
/bin/rm -rf "$dst"
/bin/cp -R "$src" "$dst"
/bin/chmod -R u+w "$dst"
removed="$(/usr/bin/find "$dst" -name '*.node' -print -delete)"
echo "removed native modules:"; echo "$removed"
argv=("$dst/$exe")
[[ -n "${args:-}" ]] && argv+=("$dst/$args")
cd "$HOME/.amq-squad/spike/s3/cwd"
/usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin TERM=xterm \
  PI_CODING_AGENT_DIR="$HOME/.amq-squad/spike/s3/agent" PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 PI_TELEMETRY=0 \
  "${argv[@]}" --provider ollama --model qwen2.5:32b --no-session --no-extensions --no-skills \
  --no-prompt-templates --no-themes --no-context-files --no-mcp --no-tools --thinking off \
  -p 'What is 17 plus 25? Reply with just the number.'
echo "exit=$?"
/bin/rm -rf "$dst"
