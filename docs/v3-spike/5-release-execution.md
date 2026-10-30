# Phase 5 release commit execution log

Branch v3-5-release, based on v3 tip 01b29a9d. No manifest or internal/ file touched.

## Version strings to 3.0.0 (line numbers in the new tree)

- plugins/codex/.codex-plugin/plugin.json:3 `2.31.0` -> `3.0.0`.
- SKILL.md line 4 `2.31.0` -> `3.0.0`, fourteen files: amq-squad,
  amq-squad-orchestrator, amq-squad-role-creator, amq-team-setup, cli,
  orchestrator, wizard under plugins/claude/skills and plugins/codex/skills.
- README.md:89 and README.html:405: `.../v2/cmd/amq-squad@latest` -> `.../v3/...@latest`.
- README.md:96 and README.html:409: `.../v2/cmd/amq-squad@v2.31.0` -> `.../v3/cmd/amq-squad@v3.0.0`.
- Found by re-grep, not on the list: docs/skills.md:71 and docs/skills.html:451
  (`@latest`, /v2 -> /v3); RELEASING.md:44-45 (smoke-test text names the /v3 path).
- plugins/claude/.claude-plugin/plugin.json was already 3.0.0 (untouched).

## Script

- scripts/check-release-version.py:127 and :141: `/v2/cmd/amq-squad@{tag}` -> `/v3/cmd/amq-squad@{tag}`.
- Output: 5-release-evidence/version-check.txt ("release metadata matches v3.0.0").

## Release notes

- CHANGELOG.md is retired as a release source, so the canonical file is
  docs/v3.0.0-release-notes.md (the script requires the heading `# amq-squad v3.0.0`).
  One line per shipped capability, no hashes, no sizes or timings except the
  final manifest line; limits are pointed at docs/v3-plan.md sections 7, 13, 16.4.

## Hits deliberately left

- docs/v2.*-release-notes.md, docs/v2.2x-*migration.md: historical.
- MIGRATION.md (2.31.0 heading, /v2 sections at :5, :341-363, :440): describes the
  v2 migration of a v2 user; the 3.0 migration section is the migrate agent's
  concern, not the version-string list.
- docs/v3-plan.md, docs/v3-upstream-*.md, docs/operator-cookbook.md,
  docs/amq-0.75.0-adoption-verdict.md, spike/loadtest/*: 2.31.0 as the
  installed baseline binary, not the project version.
- spike/gateverify (go.mod and imports with /v2): a separate spike module
  with its own replace directive; not built by ./..., out of release scope.
- internal/ (all hits): out of scope by instruction.

## Action pins (.github/workflows/ci.yml, every `uses:` line)

Source: `gh api repos/<owner>/<repo>/git/ref/tags/<tag>` (authenticated); all
three refs are lightweight tags (object type commit), so no dereference was
needed. Response files and sha256 are under 5-release-evidence/ (ref-*.json).

| action | tag | commit | sha256 of response |
|---|---|---|---|
| actions/checkout | v4 | 11d5960a326750d5838078e36cf38b85af677262 | b4be54231ed94c642aaca0da52e6aa598a58f041b39bb887da8efa6e9e9c5826 |
| actions/setup-go | v5 | 40f1582b2485089dde7abd97c1529aa768e1baff | 01ffc9c5f819ce3d15feb87915db3ec5076052ce437802cf30e8db0d0f46bc5e |
| actions/upload-artifact | v4 | ea165f8d65b6e75b540449e92b4886f43607fa02 | 11d527e9dd398225cfa3ccd343842edafe56f865982da11ac358e77eb6a472cf |

The tag stays as a trailing `# vN` comment on each line.

## Gate

5-release-evidence/gate.txt: build, vet, linux and windows builds,
manifest-check --final (OK), workflow YAML parse (ruby; PyYAML absent),
check-release-version, go test -count=1 ./... (exit 0, 55 packages ok), run on
a quiet host.

## Rebase commit (onto v3 at f337b4a5, after the uninstall merge 889c5cd6)

- docs/v3.0.0-release-notes.md: uninstall line replaced, class-verifier line and
  Claude registry-pinned-binary line added, manifest line refreshed to 79,815.
- MIGRATION.md: "Moving to 3.0" section added at the top; v2 sections unchanged.
- Gate re-run (gate.txt, version-check.txt): all green, 56 packages ok.
