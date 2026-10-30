# Brief 0: Codex GPT-6 catalog

The Codex catalog now keeps the 5.6 entries and adds `gpt-5.6-luna` plus the
GPT-6 ids confirmed by the installed Codex CLI: `gpt-6.1-sol`, `gpt-6-sol`,
`gpt-6.1-terra`, `gpt-6-astra`, and `gpt-6-luna`.

`codex-cli 0.166.0` was probed once per catalog id with `doctor --probe --runs
1`. All eight ids returned the probe's structured `OK` completion and are
cached in `internal/registry/codexmodel.go`; the per-id redacted record is
[`probe-codex-0.166.0.txt`](5-catalog-evidence/probe-codex-0.166.0.txt).

The registry cost vocabulary is `low`, `medium`, and `high`. Cost mirrors the
capability twin until the owner or a price source says otherwise: 5.6-sol,
6.1-sol, and 6-sol are `high`; 5.6-terra and 6.1-terra are `medium`; and
5.6-luna, 6-astra, and 6-luna are `medium` as the balanced, implementation
twins of terra. The mirror rule is the source of these values. The probe
answers usable only and says nothing about cost.

The probe command implied `--write-registry`, so it updated the owner-owned
`~/.amq-squad/registry.yaml`; no repository code writes that file. The owner
registry was rebuilt from the installed binary before the next seat launch
(reviewer ruling 2 of 2026-10-30). The GPT-6 rows return when the owner
installs a binary carrying this merge. Seats never run doctor against owner
state again (the 3.0.x fail-closed item: doctor refuses `--probe` and
`--write-registry` inside a seat).
