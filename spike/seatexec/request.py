"""Render the S4 PrepareRequestV1 (contract 0.61.1). Printing does not prepare or apply.

Two runnable participants (Claude, Codex) plus a non-runnable operator.
Both run under the seat-exec wrapper with only `--seat <handle>`.
The Codex seat's cwd is a linked git worktree of the scratch project, so its
shared .git directory sits outside the seat's cwd (upstream ask 5 evidence).
"""
import argparse
import json
from pathlib import Path

home = Path.home()
p = argparse.ArgumentParser()
p.add_argument("--project", default=str(home / ".amq-squad/spike/s4/project"))
p.add_argument("--codex-cwd", default=str(home / ".amq-squad/spike/s4/project-wt"))
p.add_argument("--wrapper", default=str(home / ".amq-squad/spike/bin/seat-exec"))
p.add_argument("--model", default="gpt-6-astra", help="Codex model: the default id in ~/.codex/config.toml")
p.add_argument("--session", default="s4")
p.add_argument("--only", choices=["claude", "codex"], default=None)
p.add_argument("--claude-name", default=None, help="value for Claude --name (default: the handle)")
p.add_argument("--launcher", choices=["tmux", "commands"], default="tmux")
a = p.parse_args()

project = str(Path(a.project).resolve())
base = project + "/.agent-mail"
role_prompt = ("Your seat role file is named by the environment variable AMQ_SQUAD_ROLE_PATH. "
               "Run exactly one shell command: cat \"$AMQ_SQUAD_ROLE_PATH\". "
               "Then reply with the token S4_ROLE_VISIBLE, your seat handle from the role, and stop.")
seats = {
    "claude": ("s4-claude", ["--model", "sonnet", "--effort", "medium",
                             "--name", a.claude_name or "s4-claude"], project),
    "codex": ("s4-codex", ["--model", a.model, "-c", "model_reasoning_effort=medium",
                           "--sandbox", "workspace-write", "--ask-for-approval", "on-request"],
              str(Path(a.codex_cwd).resolve())),
}
participants = [{"handle": "operator", "runnable": False}]
for runtime, (handle, args, cwd) in seats.items():
    if a.only and runtime != a.only:
        continue
    participants.append({
        "handle": handle, "runnable": True, "executable": runtime, "args": args,
        "wrapper": {"executable": str(Path(a.wrapper)), "args": ["--seat", handle]},
        "cwd": {"kind": "absolute", "path": cwd},
        "resume_policy": "fresh",
        "execution": {"require_wake": False, "no_gitignore": True,
                      "wake": {"mode": "disabled", "audit_reason": "S4 manually driven launch probe"}},
        "initial_input": {"kind": "argument", "text": role_prompt},
    })
print(json.dumps({
    "request_version": 1,
    "target": {"project_root": project, "base_root": base,
               "session_root": base + "/" + a.session, "session": a.session},
    "launcher": a.launcher,
    "intent": {"intent_version": 1, "participants": participants},
}, indent=2))
