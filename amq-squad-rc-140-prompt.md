# amq-squad 1.4.0 RC Usage Prompt

Use the local RC binary at `/Users/omri.a/Code/amq-squad/amq-squad-rc-140`:

```sh
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 version
```

Expected:

```text
amq-squad v1.4.0-rc
```

## Goal

Validate the 1.4.0 operator mailbox behavior:

- New team profiles default to schema 3.
- New teams include a virtual operator mailbox handle `user` by default.
- Custom operator handles are respected in JSON, team rules, and bootstrap text.
- The operator is not a runnable agent.
- Teams can opt out with `--no-operator`.
- JSON clients can discover operator support through `operator` and `capabilities.operator_gates`.
- Project-scoped commands accept `--project DIR` so multi-project clients do not depend on cwd.
- Legacy schema 1/2 teams without `operator` are treated as implicit non-runnable `user` operator-gate teams until rewritten. Schema 3 `--no-operator` is the explicit opt-out.
- Agents can send structural gates to the user, and the user can reply manually or through a client such as amq-noc.

## Suggested RC Test

Create a temporary test project:

```sh
tmp="$(mktemp -d)"
cd "$tmp"
git init
```

Initialize a default team:

```sh
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 team init --project "$tmp" --roles cto,fullstack
```

Inspect the profile:

```sh
cat .amq-squad/team.json
```

Expected profile properties:

```json
{
  "schema": 3,
  "operator": {
    "enabled": true,
    "handle": "user"
  }
}
```

Check JSON discovery for clients:

```sh
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 team profiles --project "$tmp" --json
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 up --project "$tmp" --dry-run --json
```

Expected JSON fields include:

```json
{
  "operator": {
    "enabled": true,
    "handle": "user",
    "runnable": false
  },
  "capabilities": {
    "operator_gates": true
  }
}
```

Check opt-out behavior:

```sh
tmp_no_operator="$(mktemp -d)"
cd "$tmp_no_operator"
git init
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 team init --project "$tmp_no_operator" --roles cto --no-operator
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 team profiles --project "$tmp_no_operator" --json
```

Expected:

```json
{
  "operator": {
    "enabled": false,
    "runnable": false
  },
  "capabilities": {
    "operator_gates": false
  }
}
```

Check a custom operator handle:

```sh
tmp_custom="$(mktemp -d)"
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 team init --project "$tmp_custom" --roles cto,fullstack --operator operator
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 team profiles --project "$tmp_custom" --json
/Users/omri.a/Code/amq-squad/amq-squad-rc-140 agent up codex --role cto --session rc-check --team-home "$tmp_custom" --me cto --dry-run
```

Expected custom-handle evidence:

- JSON shows `operator.enabled=true`, `operator.handle="operator"`, `operator.runnable=false`, and `capabilities.operator_gates=true`.
- `$tmp_custom/.amq-squad/team-rules.md` uses `amq send --to operator` and `amq send --me operator`.
- The `agent up --dry-run` bootstrap text also uses `--to operator` and `--me operator`.

## Operator Gate Round Trip

When a running agent needs human approval, it should send a structural gate:

```sh
amq send \
  --to <operator-handle> \
  --thread gate/manual-rc \
  --kind question \
  --subject "APPROVAL: manual RC test + commit decision" \
  --body "Please test the RC, then approve commit or request changes."
```

The user can reply from a terminal or an amq-noc-style client on the same thread:

```sh
amq send \
  --me <operator-handle> \
  --to fullstack \
  --thread gate/manual-rc \
  --kind answer \
  --subject "APPROVED: manual RC test + commit decision" \
  --body "Approved after RC test."
```

Use `DENIED:` or `ANSWER:` for other replies. Use `DONE:` only when the operator is closing a requested manual task. Do not use the operator mailbox for ordinary agent-to-agent coordination.

## Final Checks

From the amq-squad repository:

```sh
go test ./...
git diff --check
```

Report any mismatch with the command, output, and whether the profile had `operator_gates` enabled or disabled.
