# Brief 0: Codex GPT-6 catalog

The Codex catalog now keeps the 5.6 entries and adds `gpt-5.6-luna` plus the
GPT-6 ids confirmed by the installed Codex CLI: `gpt-6.1-sol`, `gpt-6-sol`,
`gpt-6.1-terra`, `gpt-6-astra`, and `gpt-6-luna`.

`codex-cli 0.166.0` was probed once per catalog id with `doctor --probe --runs
1`. All eight ids returned the probe's structured `OK` completion and are
cached in `internal/registry/codexmodel.go`; the per-id redacted record is
[`probe-codex-0.166.0.txt`](5-catalog-evidence/probe-codex-0.166.0.txt).

The registry cost vocabulary is `low`, `medium`, and `high`: 6.1-sol and
6-sol are `high`; 6.1-terra is `medium`; 6-astra is `high`; and 6-luna is
`low`.

The probe command currently implies `--write-registry`, so it updated the
owner-owned `~/.amq-squad/registry.yaml`; no repository code writes that file,
and the lead should rebuild it after merge as the brief directs.
