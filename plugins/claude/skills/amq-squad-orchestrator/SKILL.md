---
name: "amq-squad-orchestrator"
description: "Deprecated compatibility redirect for the live lead skill. Use amq-squad:orchestrator."
version: "3.1.0"  # x-release-please-version
allowed-tools: "Bash, Read, Write, Edit, Glob, Grep"
argument-hint: "[status | dispatch | recover]"
user-invocable: true
trigger: "/amq-squad-orchestrator"
---
Use `amq-squad:orchestrator`. The old name is retained only for compatibility;
the namespaced skill owns the live lead's planning, dispatch, status review,
widening, merge train, gates, releases, and recovery.
