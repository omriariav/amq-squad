#!/bin/sh
# R12 probe driver: run probe.mjs against each local model, N runs each.
# Usage: spike/piprobe/probe.sh [runs] [model ...]
# Default models: ollama/qwen2.5:32b ollama/qwen3.6:27b ollama/gemma3:4b
# Results: JSON lines appended to ~/.amq-squad/spike/s5/probe-results.jsonl
set -eu
here=$(cd "$(dirname "$0")" && pwd)
runs=${1:-3}
[ $# -gt 0 ] && shift
[ $# -eq 0 ] && set -- ollama/qwen2.5:32b ollama/qwen3.6:27b ollama/gemma3:4b
out="$HOME/.amq-squad/spike/s5/probe-results.jsonl"
for m in "$@"; do
	# Run 1 of each model includes the Ollama model load in its TTFT; runs 2+ are warm.
	node "$here/probe.mjs" --model "$m" --runs "$runs" | tee -a "$out"
done
