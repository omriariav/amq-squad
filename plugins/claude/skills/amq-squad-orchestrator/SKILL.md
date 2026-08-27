---
name: "amq-squad-orchestrator"
description: "Deprecated compatibility redirect for the live lead skill. Use amq-squad:orchestrator."
version: "2.30.1"  # x-release-please-version
allowed-tools: "Bash, Read, Write, Edit, Glob, Grep"
argument-hint: "[compose | start | dispatch | status | coordinate | recover | example]"
user-invocable: true
trigger: "/amq-squad-orchestrator"
---
Use `amq-squad:orchestrator`. The old name is retained only for compatibility;
the namespaced skill owns live lead composition, dispatch, monitoring, review,
recovery, and evidence behavior.
