# Phase 5 release prep: execution log

Branch v3-5-relprep from v3 f5bcf370, 2026-10-25. One commit per row. Gate
output: 5-relprep-evidence/gate.txt; revert checks for rows 1, 2 and 5:
5-relprep-evidence/revert-check.txt (both through scripts/redact-evidence).

| Row | Change | Test | Revert check |
|---|---|---|---|
| 1 pin_changed_since_probe | launch.Record.ClaudePin (`claude_pin_sha256`) set by seatlaunch from the pin the mode probe ran on; seat-exec `pinnedProvider` compares the registry's current claude pin digest with it before the tool_unpinned checks, in both the early and the final pre-exec call. A record with no digest is refused too | internal/seatexec TestSeatRefusedWhenPinChangedSinceProbe; internal/seatlaunch milestone B test asserts the staged digest | comparison removed: the re-pinned seat execs, test fails |
| 2 doctor row | `claudePinCheck` in internal/cli/doctor_host.go. The installed claude is found on PATH, its chain resolved by `resolveInstalledClaude`: fsanchor parent walk, readlinkat, 8-hop bound, every hop must stay under the owner's HOME or /opt/homebrew (coordinator addition), hashed from the descriptor, never executed. Differs: warn "pinned claude is behind the installed claude" with `ln -f <real> <pin path>` and the registry sha256 line. Chain outside the roots, loop or unreadable: warn "unresolvable", not followed | internal/cli TestDoctorWarnsWhenThePinnedClaudeIsBehindTheInstalledClaude, TestDoctorClaudePinRefusesAnUnresolvableInstalledChain | comparison forced equal: stale pin reported ok, test fails |
| 3 Linux CI | job `claude-hooks-linux` (ubuntu-latest) appended to .github/workflows/ci.yml; builds amq-squad, runs `HOOK_SHELL=dash python3 scripts/test_claude_hooks.py ./amq-squad Repass5`. Existing jobs untouched | YAML parsed (ruby; PyYAML is not installed on this Mac); the same command passed locally under /bin/dash | none (as briefed) |
| 4 hooks re-check | docs/v3-spike/5-hooks-api-recheck.md | none (documentation read only, no test touches the network) | none |
| 5 version | plugins/claude/.claude-plugin/plugin.json 2.31.0 to 3.0.0; marketplace.json carries no version | internal/claudeseat TestClaudePluginManifestVersionIsTheReleaseVersion | version reverted: test fails |

Judgment calls:
- No Go-side version constant exists (claudeseat reads the embedded manifest
  at run time), so the row-5 test holds the expected "3.0.0" literal, checks
  the embedded copy, the file on disk and that marketplace.json names no
  other version. I did not add a production constant used only by a test.
- The codex plugin manifest (plugins/codex, 2.31.0) and the other release
  files were not bumped (the brief names the Claude plugin only).
  `scripts/check-release-version.py 3.0.0` fails on them until the release
  commit; that is the release step, not this row.
- Row 1 refuses an empty staged digest as well as a different one; the
  seatexec fixture records the digest at stage time.
- Row 2 only warns for a stale pin; a missing pin stays the launch's
  tool_unpinned. An installed claude outside HOME and /opt/homebrew (for
  example /usr/local/bin) is reported unresolvable by design.
- Manifest current_lines (and target_lines of the fate-new rows) refreshed
  for doctor_host.go, launch/record.go, seatexec_unix.go, seatlaunch/run.go.
