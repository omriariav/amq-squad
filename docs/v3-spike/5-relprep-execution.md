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

## Fix round (reviewer's narrow pass on 35238073, row 2)

- Fix 1: the printed `ln -f` operands go through the package's `shellQuote`
  (single quotes, the `'\''` escape); when the installed binary and the pin's
  directory are on different devices doctor says "the pin must be on the same
  filesystem as the installed binary" and prints no command (`fileDevice`, a
  var over `statDevice` in doctor_device_unix.go and its non-unix twin).
  Row: TestDoctorClaudePinQuotesTheCommandAndNamesADeviceMismatch.
- Fix 2: `resolveInstalledClaude` no longer joins and Cleans. It resolves
  component by component through the fsanchor walk (Sub for directories,
  readlinkat for links), splices a link's target in front of the remaining
  components, refuses a `..` that follows a symlinked component of its own
  path, and refuses the ninth link before following it. Row:
  TestDoctorClaudePinResolvesLikeTheKernel (os.SameFile on os.Stat is the
  oracle; the tricky input is refused, a plain `../versions/new` link
  resolves to the kernel's file, eight links pass and nine are refused).
- Revert checks (quoting, device note, the `..` rule, the hop bound): in
  5-relprep-evidence/revert-check.txt; gate re-run in gate.txt.

## Release commit list (not changed here; confirmed by grep for 2.31.0 and /v2)

- plugins/codex/.codex-plugin/plugin.json:3 (`"version": "2.31.0"`).
- Fourteen SKILL.md version lines, line 4 each: seven under
  plugins/claude/skills (amq-squad, amq-squad-orchestrator,
  amq-squad-role-creator, amq-team-setup, cli, orchestrator, wizard) and seven
  under plugins/codex/skills (the same seven names).
- README.md:96 and README.html:409 (`go install .../v2/cmd/amq-squad@v2.31.0`);
  also the `@latest` lines README.md:89 and README.html:405 carry the /v2
  module path, which is the v3 module path now.
- scripts/check-release-version.py hardcodes the /v2 install paths at :127 and
  :141 and must learn the 3.0 path before the release gate.
- Optional hardening: pin the actions in .github/workflows/ci.yml (checkout,
  setup-go, upload-artifact, and the new job's) to commit SHAs.
