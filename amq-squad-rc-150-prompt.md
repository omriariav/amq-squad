# amq-squad v1.5.0 RC Usage Prompt

Use the local RC binary at `/Users/omri.a/Code/amq-squad/amq-squad-rc-150`:

```sh
/Users/omri.a/Code/amq-squad/amq-squad-rc-150 version
```

Expected:

```text
amq-squad v1.5.0-rc
```

## Goal

Validate the v1.5.0 first-class tmux runtime orchestration contract that
amq-noc consumes (amq-noc#6). amq-squad owns the tmux execution/control layer;
amq-noc renders/calls stable project-scoped commands.

- Each agent launched inside tmux persists its exact tmux identity in the
  launch record (`tmux.session`, `tmux.window_id`, `tmux.window_name`,
  `tmux.pane_id`, `tmux.target`).
- `status --json`, `history --json`, and `resume --json` expose that identity
  plus a computed `pane_alive`.
- `status --json` members carry an `actions` array (focus / send / resume /
  status) with the exact runnable command and an `available` flag.
- Control verbs `focus` / `open` / `send` target exact pane ids.
- `send` delivers prompts deterministically (paste buffer via stdin + explicit
  Enter); multi-line / quoted / shell-metacharacter text arrives verbatim.

## Suggested RC test (inside tmux)

Run from inside a tmux session so launches capture pane ids.

```sh
RC=/Users/omri.a/Code/amq-squad/amq-squad-rc-150
tmp="$(mktemp -d)"; cd "$tmp"; git init -q
"$RC" new team --roles cto,qa --binary qa=codex --session issue-96
"$RC" up --session issue-96 --target current-window
```

Inspect the runtime contract:

```sh
"$RC" status --session issue-96 --json | jq '.data.records[] | {role, status, tmux, actions}'
```

Expected per live member: a non-null `tmux` block with a real `pane_id`
(`%NN`), `pane_alive: true`, and an `actions` array whose `focus`/`send`
commands are `available: true`.

Resume planner JSON (kind `resume_plan`):

```sh
"$RC" resume --session issue-96 --json | jq '.kind, .data.plan'
```

History JSON carries the tmux block for restorable records:

```sh
"$RC" history --json | jq '.data.records[] | select(.tmux) | {role, tmux}'
```

Control verbs:

```sh
"$RC" focus --session issue-96 --role cto              # bring cto's pane into view
"$RC" send  --session issue-96 --role cto --body "hi from the RC"   # delivers + Enter
printf 'multi\nline\nprompt with "quotes" and $(echo metas)\n' | \
  "$RC" send --session issue-96 --role qa --body-file -
```

Dead-pane behavior (close the pane, then):

```sh
"$RC" send --session issue-96 --role cto --body "should fail clearly"
# -> error: tmux pane %NN is not available (it may have been closed)
```

## What amq-noc should consume

- `tmux.pane_id` / `tmux.pane_alive` to decide whether focus/send/open are
  offered.
- `actions[].command` + `actions[].available` to render copyable/runnable
  operator actions per row.
- `resume_plan` for "resume here" previews.

No tmux scraping in amq-noc; rely on the JSON above.
