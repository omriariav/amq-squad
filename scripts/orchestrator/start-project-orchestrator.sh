#!/usr/bin/env bash
#
# Thin forwarder to the canonical CLI verb. The implementation lives in Go
# (internal/cli/orchestrate.go) so it is tested and does not drift; this script
# exists only as a convenience entrypoint / muscle-memory path.
#
#   amq-squad run start --help
#
# Example (preview, then create):
#   scripts/orchestrator/start-project-orchestrator.sh -p ~/Code/app -s issue-96 --roles "cto,fullstack,qa"
#   scripts/orchestrator/start-project-orchestrator.sh -p ~/Code/app -s issue-96 --roles "cto,fullstack,qa" --go
#
exec amq-squad run start "$@"
