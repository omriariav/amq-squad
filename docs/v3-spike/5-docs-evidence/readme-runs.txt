ok   README.md:64 exit 0: amq-squad version
       amq-squad v3.0.0-20261029210837-f58cfe3c319d
ok   README.md:138 exit 2: amq-squad doctor
       error: doctor: 2 check(s) failed
ok   README.md:139 exit 2: amq-squad doctor --project .
       error: doctor: 2 check(s) failed
ok   README.md:140 exit 2: amq-squad doctor --json
       error: doctor: 2 check(s) failed
ok   README.md:200 exit 0: amq-squad trust .
       Trust <tmp>/inv-4 in codex-cli. amq-squad never writes <tmp>/home/.codex/config.toml: codex-cli owns it.
ok   README.md:211 exit 0: amq-squad roles
       ROLE          DEFAULT RUNTIME  SOURCE
ok   README.md:212 exit 0: amq-squad team propose --project . --roles cto,fullstack,qa --runtime fullstack=codex --worktree fullstack=../app-96 --lead cto --session issue-96
       create  <tmp>/inv-6/squad.yaml                1cc1a8db3fd9
ok   README.md:213 exit 2: amq-squad team apply --digest <digest> --project . --roles cto,fullstack,qa --runtime fullstack=codex --worktree fullstack=../app-96 --lead cto --session issue-96
       error: squad_yaml_digest_mismatch: the planned files changed since the proposal (now sha256:b1ca13542da8f14f5b5fc431360855eab06b1164b6a8ccf6b7a5054bd3f9ef0d@abs
ok   README.md:214 exit 2: amq-squad team show --project .
       error: no squad.yaml at <tmp>/inv-8/squad.yaml; the lead proposes one with amq-squad team propose
ok   README.md:215 exit 2: amq-squad team check --project .
       error: no squad.yaml at <tmp>/inv-9/squad.yaml; the lead proposes one with amq-squad team propose
ok   README.md:225 exit 2: amq-squad brief --goal "fix issue 96" --session issue-96 --dry-run
       error: read team: open <tmp>/inv-10/squad.yaml: no such file or directory
ok   README.md:226 exit 0: amq-squad brief --seed-from file:./prompt.md --session issue-96
       wrote brief <tmp>/inv-11/.amq-squad/briefs/issue-96.md
ok   README.md:240 exit 2: amq-squad lead --team --scheduler-seats seats.json --dry-run
       amq-squad lead: model ollama/qwen3.6:27b (fallback: no registry (run amq-squad doctor --write-registry))
ok   README.md:249 exit 2: amq-squad launch --seats seats.json --dry-run
       error: launch: open seats.json: no such file or directory
ok   README.md:262 exit 0: amq-squad status --project . --session issue-96 --json
       {
ok   README.md:263 exit 0: amq-squad task add --title "Fix the parser" --class implement --files internal/parse/ --pool claude:sonnet:medium --project . --session issue-96
       Added t1 "Fix the parser": pending
ok   README.md:264 exit 0: amq-squad task list --project . --session issue-96
       No tasks.
ok   README.md:265 exit 0: amq-squad gate status --project . --session issue-96 --json
       {
ok   README.md:291 exit 12: amq-squad verify action --project . --session issue-96 --gate rel-v3 --action tag --target "git tag v3.0.0" --json
       error: action authorization no_gate
ok   README.md:375 exit 0: amq-squad uninstall --project . --session issue-96
       amq-squad uninstall (dry run: nothing is changed; pass --write to remove)
ok   README.md:143 exit 2: amq-squad doctor --write-registry
       error: doctor: 2 check(s) failed
ok   README.md:220 exit 0: amq-squad role draft researcher --purpose "Investigate ambiguous behavior"
       No role file was staged.
ok   README.md:232 exit 2: amq-squad launch --seats seats.json
       error: launch: open seats.json: no such file or directory
ok   README.md:233 exit 0: amq-squad launch --help
       amq-squad launch - start a squad's seats through the AMQ launch API
ok   README.md:271 exit 0: amq-squad status --fleet --json
       {
ok   README.md:328 exit 0: amq-squad --help
       amq-squad - role-aware agent team launcher on top of AMQ

26 README invocation(s), 0 failure(s)
6 flagless inline reference(s) not executed:
    README.md:57: amq-squad migrate
    README.md:57: amq-squad uninstall
    README.md:68: amq-squad lead
    README.md:88: amq-squad doctor
    README.md:91: amq-squad doctor
    README.md:252: amq-squad lead
