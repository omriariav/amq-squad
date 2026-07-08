#!/usr/bin/env bash
#
# Thin forwarder to the canonical CLI verb. The implementation lives in Go
# (internal/cli/orchestrate.go) so it is tested and does not drift; this script
# exists only as a convenience entrypoint / muscle-memory path.
#
#   amq-squad global start --help
#
# Example:
#   scripts/orchestrator/start-global-orchestrator.sh --root ~/Code --agent claude --go
#
exec amq-squad global start "$@"
