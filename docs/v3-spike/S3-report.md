PASS

# S3: darwin-arm64 build with the embedded pi

Recorded 2026-10-21, launch-dev rerun, branch `spike/s3-embed` (cut from `v3`).
Criterion (phase 1 plan, S3): the binary starts the lead from the unpacked copy
with no pi or Node on PATH and answers one prompt. Fail if no packaging runs
without a host Node or the bundle exceeds 250 MB.

Result: every build starts pi from `~/.amq-squad/runtime/...` under
`env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm`. In that environment
`command -v pi node bun npm` finds nothing. Each build answered the real prompt
"What is 17 plus 25? Reply with just the number." with `42` from Ollama
`qwen2.5:32b`, in print mode (`-p`) and in the interactive TUI inside tmux. The
decided build's launcher is 59.27 MB, well under 250 MB.

Roster note: implementer swapped from the Codex subagent in this Claude Code harness to Opus because of the Codex agent sandbox; reviewer family is Codex.

**Scope of the evidence.** Every measured run used `--no-tools --no-extensions
--no-skills --no-mcp`. The probes cover: one text prompt, the TUI, one probe
extension, HTML export and an image attach. **Untested:** tool calls, the
codemode worker, whether image resize ran in the worker or fell back
in-process, the amq-squad pi package, hooks, and seat integration.

## Decision (lead rulings after review)

- **Packaging (a), with the strip applied, is the decision.** That means the
  official Node 22.21.1 binary plus the npm pi tree from the committed
  lockfile, pruned as in "Pruning patterns", plus digest-pinned `fd` and `rg`.
  `build.sh node` builds it; `build.sh node-full` builds the unstripped tree.
- **(b2), upstream's Bun binary, is the only documented fallback.** Its
  signature is **invalid as shipped**.
- **(b1), our own `bun build --compile` from the npm dist, is dropped and is
  not a fallback.** Its embedded worker path lacks the `src/` prefix pi asks
  for, so it is not faithful (see below).
- **Pin:** `@earendil-works/pi-coding-agent` **1.3.0** and Node **22.21.1**
  (official nodejs.org build). Both are digest-pinned: the npm lock carries
  integrity hashes and `build.sh` holds the SHA-256. AMQ's own pi evidence pin,
  **0.85.1** (AMQ `docs/remote-compat.md`, see the S5 report), lags this. The
  AMQ bridge is evidenced against an older pi than the one we embed, and
  upstream ask 1 covers re-verifying it.
- **Quarantine hang: a release risk, not a spike failure.** The cause is
  unexplained; reproduce it under notarization. Phase 5 ships a signed and
  notarized launcher, and release-smoke includes a browser-download start on
  a clean user account (see "Signing acceptance criteria"). Until then the
  install paths are `go install` and `brew` only.

## Measurements

Host: Apple M5 Max, macOS Darwin 25.6.0, Go 1.27.1, Bun 1.4.2 (build only),
Node 22.21.1 (build only, `npm ci`). MB means 10^6 bytes. Raw data is in
`spike/embedpi/evidence/measure-*.json`.

**Sample counts and labels.**
- Per packaging: 3 cold `--version` runs (median shown), 5 warm `--version`
  runs (median shown), **1** cold Ollama prompt, 3 warm Ollama prompts
  (median shown), **1** full-verify run, 1 cold and 5 warm stub prompts. That
  makes **four Ollama answers and six stub answers per packaging**.
- Cold means this candidate's runtime directory was deleted first.
- **"First token" is the first `message_update` event**, which can be a
  `text_start` rather than a nonempty delta. The decided build also records
  `first_text_s`, the first nonempty `text_delta`, which was within 0.3 ms of
  it on every run.
- "Install/check" is the launcher's own timer. For the decided build it starts
  before the manifest is read and parsed and includes the launch integrity
  check. For the other three columns (older launcher), **the timer excluded
  the manifest JSON parse and the warm path was a stamp check only**.

| Measurement | **(a) stripped, decided** | (a) unstripped, first pass | (b2) upstream Bun, fallback | (b1) our `bun --compile`, dropped |
|---|---|---|---|---|
| Launcher binary (`bin/embedpi-*`) | **59.27 MB** | 67.30 MB | 34.23 MB | 35.98 MB |
| Embedded payload (tar.gz) | 54.52 MB | 60.61 MB | 31.03 MB | 32.77 MB |
| Unpacked runtime | **179.25 MB, 6,432 files** (fd 2.97 MB and rg 4.01 MB included) | 223.26 MB, 14,988 files | 84.40 MB, 209 files | 82.99 MB, 191 files |
| Cold install (unpack + full SHA-256) | **1,127 ms** (1.11–1.14 s) | 1,878 ms | 375 ms | 371 ms |
| Cold `--version` wall | 2.03 s | 2.83 s | 1.23 s | 1.19 s |
| **Warm launch check** | **43.5 ms** (43.2–44.0): manifest hash + 21 executables (119.1 MB) + path checks + parse | 5.5 ms, stamp only, parse excluded | 0.11 ms, stamp only | 0.10 ms, stamp only |
| Warm `--version` wall | 0.147 s | 0.116 s | 0.154 s | 0.127 s |
| Full re-hash (doctor, `EMBEDPI_VERIFY=full`), 1 sample | 190 ms | 368 ms | 36 ms | 34 ms |
| **Cold start to first token, Ollama qwen2.5:32b**, 1 sample | **2.38 s** | 4.05 s | 2.28 s | 2.64 s |
| **Warm start to first token, Ollama** | **0.248 s** | 0.219 s | 0.263 s | 0.229 s |
| Ollama preload before the pass | 0.26 s | 0.09 s | **7.406 s** | 0.09 s |
| Cold launch to request at local stub, 1 sample | 2.09 s | 2.89 s | 1.24 s | 1.21 s |
| Warm launch to request at stub | 0.176 s | 0.144 s | 0.187 s | 0.154 s |
| Answers (4 Ollama + 6 stub) | `42` ×10 | `42` ×10 | `42` ×10 | `42` ×10 |
| Interactive TUI lead in tmux, answers | yes, no fd/rg warnings | yes, "fd/ripgrep not found" | yes | yes (`evidence/tui-bun.txt`, older launcher) |
| Prompt with the native `.node` removed | `42`, exit 0 | `42`, exit 0 | not run | `42`, exit 0 |
| Probe extension, HTML export, image attach | pass, pass, pass | not run | not run | not run |
| Unpacked exe xattrs | `com.apple.provenance` only | same | same | same |
| Unpacked exe signature | Developer ID Node.js Foundation (HX7739G8FX), hardened runtime | same | ad-hoc, **invalid as shipped** | ad-hoc, valid |
| Launcher signature | ad-hoc (Go linker); `spctl` rejects; runs | same | same | same |
| Quarantined launcher | not run | not run | not run | **hung**, cause unexplained |
| 250 MB limit | pass | pass | pass | pass |

Notes:

- **What the strip saved.** On the launcher, measured: 67.30 to 59.27 MB.
  That 8.0 MB is net of the roughly 3 MB of compressed fd and rg added in the
  same build. Unpacked, from the manifest: 51.0 MB and 8,560 files removed
  before fd and rg added 7.0 MB in 4 files. Cold install fell from 1.88 to
  1.13 s.
- **Warm cost of the launch check.** It is **43.5 ms**, mostly from hashing
  the 111.7 MB Node binary. It raised warm `--version` from about 0.105 s
  (stamp-only launcher, same tree) to 0.147 s.
- **Shared Ollama.** Ollama is shared with other spike agents, and an earlier
  pass that overlapped their load measured 11 to 35 s to first token. The
  stub rows point the same pi invocation at a local OpenAI-compatible stub, to
  show pi's own start cost apart from Ollama.
- **Where cold time goes.** On a cold start, most of the gap between install
  time and total time is the first exec of a new Mach-O. syspolicyd logs a
  `GK performScan` for every new executable path even without quarantine.

## Launch-time integrity (warm-path behaviour)

Implemented in `spike/embedpi/main.go` (`ensure`, `warmOK`, `launchCheck`).

- **Cold install.** Verify the embedded payload SHA-256, then take a `flock`
  and unpack into `.unpack-*`. Re-hash every file against the manifest
  (path, size, mode, SHA-256). Write `.embedpi-manifest.json`, which is
  byte-identical to the embedded manifest, and then the `.embedpi-complete`
  marker, which records the payload digest. Finally `rename` atomically to
  `~/.amq-squad/runtime/<version>-<sha256[:12]>/`.
- **Every warm launch** (`launchCheck`), before pi is exec'd:
  1. The SHA-256 of `.embedpi-manifest.json` must equal the SHA-256 of the
     embedded manifest.
  2. Every file with mode 0555 must be a regular file with the manifest's
     size and mode, and its full SHA-256 must match. In the decided build
     that is 21 files, 119.1 MB: `node/bin/node`, `tools/fd`, `tools/rg`, the
     darwin-arm64 `.node` addon, and JS files that ship with an exec bit,
     including the entry `cli.js`. They are hashed in parallel.
  3. Every component of the path from the runtime root to the executable, the
     entry script and each tool must be a real directory (the last one a
     regular file). No component may be a symlink.
- **Marker absent or for another payload, or on-disk manifest absent.** Full
  re-hash of every file. If it passes, the manifest and marker are rewritten;
  if it fails, the launch is refused.
- **Doctor** (`EMBEDPI_VERIFY=full`). Full re-hash of every file, every
  launch it is set.
- **On any failure** the launcher prints
  `existing runtime <dir> rejected (remove it explicitly to repair): <reason>`,
  exits 1 and does not exec pi. It never repairs, deletes or re-unpacks the
  directory itself.
- **Limits:**
  - Non-executable files are not re-hashed at launch. That includes the
    bundle chunks `dist/bundle/chunks/*.js` (8.9 MB) that the hashed `cli.js`
    imports, along with the rest of the JavaScript tree, the assets and the
    WASM. Hashing them as well would add a fraction of the 190 ms full cost. A same-size edit to one passes the launch check and
    is caught only by doctor or a missing marker. `TestLaunchCheck` asserts
    this limit.
  - The check-then-exec has a TOCTOU window.
  - Node reads JS files lazily after the check.
  - The check does not defend against a same-user attacker who can replace
    the launcher, or the agent-dir links in `<agent-dir>/bin`. Those links
    are re-pointed at every launch, but their targets are covered only by the
    executable hash.
- **Tests.** `go test ./...` passes 8 tests: cold and warm; tamper under a
  full re-hash; payload digest checked before any write; traversal and
  symlink swap; archive with a symlink or a missing file; 4-way concurrent
  install with exactly one cold install; and `TestLaunchCheck`. The last one
  covers executable tamper caught at launch, the non-executable limit,
  on-disk manifest change, a symlinked directory on the entry path, and the
  marker-loss full re-hash. Two more cover fd/rg linking and the seat name.

## 2b defaults (R1)

The launcher sets these itself (`piDefaults`, `seatName`, `linkTools`) for
the embedded lead and every pi seat, unless the caller already set the
variable:

| Setting | Value |
|---|---|
| `PI_OFFLINE` | `1`: no automatic catalog refresh and no fd/rg download |
| `PI_SKIP_VERSION_CHECK` | `1`: no pi.dev latest-version request |
| `PI_TELEMETRY` | `0`: no install telemetry or provider attribution headers |
| `PI_CODING_AGENT_DIR` | `~/.amq-squad/pi-agent/<seat>`, seat-specific. The seat comes from `AMQ_SQUAD_SEAT`, then `AM_ME`, else `lead`; it must match `[A-Za-z0-9._-]+` and must not be `.` or `..` |
| fd, rg | `<agent-dir>/bin/{fd,rg}` are symlinks into `runtime/<version>/tools/`, the managed agent-bin directory. Each launch re-points an existing link atomically; a regular file a user placed there is left alone |

- **These variables do not isolate the network.** Model requests still go to
  the configured provider. In these runs that was Ollama on localhost. No
  other traffic was observed, but nothing was packet-captured.
- **Why fd and rg are embedded.** On darwin-arm64, a missing fd or rg makes
  pi download the **latest** GitHub release with no digest check
  (`TOOLS`/`getToolPath` in the bundle; `tools-manager` in the source).
  Binaries in `<agent-dir>/bin` take precedence over PATH.
- **The pins.** **fd 10.5.0**: `fd-v10.5.0-aarch64-apple-darwin.tar.gz`, SHA-256
  `b67e1836c468e42e411984b56e52fa7abec08c2bd22c867398e7cc134aac5e12`.
  **ripgrep 15.2.0**: `ripgrep-15.2.0-aarch64-apple-darwin.tar.gz`, SHA-256
  `3750b2e93f37e0c692657da574d7019a101c0084da05a790c83fd335bad973e4`. Both
  match the GitHub release asset digests.
- **How they get in.** `build.sh fetch` downloads them once. `build.sh node`
  re-checks the digests and copies `fd`, `rg` and their MIT licenses into
  `tools/`. `pack.py --tool fd=tools/fd --tool rg=tools/rg` records the tool
  map in the manifest.
- **Evidence.** `evidence/defaults-node-stripped.txt` runs the launcher with
  no `PI_*` set under a throwaway HOME. pi wrote only `.amq-squad/pi-agent/`
  state plus `bin/fd` and `bin/rg`, and no `~/.pi`. That run predates the
  per-seat suffix; `TestSeatName` covers the seat path. With fd and rg
  linked, the TUI no longer prints "fd not found".
- **Provider config.** `models.json` is not in the payload. Phase 4 writes it
  per seat.

## Pruning patterns

Applied by `build.sh node` to `pi/node_modules` before packing:

- `*.map`
- `*.d.ts`, `*.d.mts`, `*.d.cts`
- the whole `@types/` tree
- every `*.node` whose path does not contain `darwin-arm64` (foreign addons)
- directories left empty afterwards

After pruning, the following were exercised on the repackaged build
(`evidence/poststrip-node-stripped.txt`, all exit 0):

- a TypeScript probe extension (`probe-ext/s3-probe.ts`, loaded with `-e`)
  that imports `VERSION` from `@earendil-works/pi-coding-agent` and wrote
  `loaded pi 1.3.0`;
- a saved session exported with `--export` (276,662-byte HTML containing the
  vendored `marked`);
- an image attach of `clankolas.png` to `gemma3:4b`, answered `yes`;
- one prompt in print mode and the TUI; and a run with the native addon
  removed.

**Not exercised after pruning:** the codemode worker, and whether image
resize ran in its worker or fell back in-process.

## Pinned runtime: what pi needs (evidence from the registry and the installed tree)

- **Package scope.** `npm view @mariozechner/pi-coding-agent` returns version
  `0.73.1` with
  `"deprecated": "please use @earendil-works/pi-coding-agent instead going forward"`.
  Its `latest` tag is 0.73.1, engines `node >=20.6.0`, bin `dist/cli.js`. The
  current package is **`@earendil-works/pi-coding-agent`**, pinned at
  **`1.3.0`** (`dist-tags.latest`, published 2026-10-15). The registry also
  has a `legacy-node20` tag at `0.74.2`.
- **Node floor.** pi 1.3.0 declares **`engines.node: ">=22.19.0"`**; the
  `>=20.6` floor belongs to the deprecated 0.73.1. Its bin is
  **`dist/bundle/cli.js`**, an esbuild bundle. The embedded Node is the
  official **`node-v22.21.1-darwin-arm64`** build, SHA-256
  `c170d6554fba83d41d25a76cdbad85487c077e51fa73519e41ac885aa429d8af`, the same
  value as nodejs.org `SHASUMS256.txt`. The build uses `npm ci --engine-strict`.
- **Native and optional dependencies.** `koffi` and `@mariozechner/clipboard`
  are not in the 1.3.0 tree (no `optionalDependencies`; the CHANGELOG
  records their replacement). The only native code is pi-tui's
  `darwin-platform.node` (52 KB, ad-hoc signed, AppKit clipboard and
  modifier-key state), loaded inside `try/catch`; one prompt works with it
  deleted. The bundle's other bare imports (`kerberos`, `bufferutil`,
  `utf-8-validate`, `@aws-sdk/signature-v4-crt`, `supports-color`) are
  optional peers and are not installed. The tree's install scripts are a
  version warning and a no-op, so the build uses `--ignore-scripts`.
- **WASM.** `photon_rs_bg.wasm` (1.88 MB, image resize) is loaded by dynamic
  import. `quickjs.wasm` (0.64 MB, codemode VM) is resolved by
  `require.resolve` under Node, and is embedded in a Bun executable.
- **Bun and (b1).** For a Bun binary pi takes every asset from
  `dirname(process.execPath)`. It asks for its codemode worker as
  `./src/extensions/codemode/worker.ts`.
  - Upstream compiles from `src/*.ts` and embeds
    `/$bunfs/root/src/extensions/codemode/worker.js` (built with Bun v1.3.14).
  - Our (b1), compiled from npm `dist/`, embeds
    `/$bunfs/root/extensions/codemode/worker.js`, with no `src/` prefix
    (`strings` on both binaries shows this). Its codemode worker would not
    resolve, so (b1) is not faithful.
  - We did not attempt a source build of upstream's binary.
- **Asset list beside a Bun executable** (for the (b2) fallback, from
  upstream `pi-darwin-arm64.tar.gz`): `pi`, `package.json`, `README.md`,
  `CHANGELOG.md`, `theme/{dark,light}.json`, `assets/clankolas.png`,
  `export-html/` (templates, compiled JS, `vendor/{marked,highlight}.min.js`),
  `photon_rs_bg.wasm`,
  `native/darwin/prebuilds/darwin-arm64/darwin-platform.node`, `docs/`,
  `examples/`. (b2) is a build from upstream's release CI; its test coverage
  was not verified here.

## What is fetched once and what is embedded

`build.sh fetch` is the only networked build step, and every download is
digest-checked:

1. 118 npm packages via `npm ci --omit=dev --ignore-scripts --engine-strict`
   from the committed `package-lock.json`, which carries integrity hashes.
2. The Node 22.21.1 darwin-arm64 tarball from nodejs.org.
3. fd 10.5.0 and ripgrep 15.2.0.
4. Upstream `pi-darwin-arm64.tar.gz` v1.3.0, SHA-256
   `1f9ae61cdeae04dd68caf1a1e8ab8ec9b292062f6ccb3252e0a9cbe93b5b16f1`
   (for the (b2) fallback only).

`build.sh node|node-full|bun|upstream` then runs offline. It stages, prunes
(for `node`), packs (`pack.py`: normalized tar.gz plus a per-file manifest and
the tool map) and `go build`s.

- **Embedded:** Node, the pi tree and assets, fd and rg.
- **Not embedded:** the provider config, and the model, which Ollama serves.

## Gatekeeper, quarantine and signing

- Files the Go launcher writes get only `com.apple.provenance`, never
  `com.apple.quarantine`. Unquarantined, every unpacked executable starts
  with no prompt even though `spctl --assess --type execute` rejects it.
- `quarantine-test.sh bun` (`evidence/quarantine-test-bun.txt`) set
  `com.apple.quarantine` on a copy of the (b1) launcher. That copy hung for
  60 s and unpacked nothing; after `xattr -d` it still hung until killed.
  **This is unexplained; reproduce it under notarization.** The evidence shows
  no dialog, no stuck-process diagnosis and no cached Gatekeeper decision.
  Earlier drafts suggested a pending dialog and suggested that notarization
  would not see the embedded payload. Both were guesses.
- **Untested:** whether files unpacked by a quarantined parent inherit
  quarantine.

## Signing acceptance criteria (phase 5, section 16.4)

1. **Launcher signature.** Developer ID Application, hardened runtime, secure
   timestamp. `codesign --verify --strict` passes.
2. **Embedded Mach-O.** Every Mach-O inside the payload is signed: Node keeps
   the Node.js Foundation Developer ID byte-identical; `darwin-platform.node`,
   `fd`, `rg` (and the Bun binary, if (b2)) are re-signed with our Developer
   ID before packing. A check fails the build if any embedded Mach-O is
   ad-hoc or invalid.
3. **Notarization.** The distributed artifact (zip or pkg, since a bare
   Mach-O cannot carry a stapled ticket) is notarized and stapled.
4. **Acceptance test (release-smoke).** On a clean macOS user account,
   download the artifact in a browser so it carries quarantine. Cold-launch
   the launcher from Terminal. Pass means it starts with no blocking dialog,
   unpacks, and the **extracted children** (Node, addon, fd, rg) exec and
   answer one prompt. Record the xattrs and `spctl` results of the extracted
   files.
5. **Interim install paths.** Until 1 to 4 pass, the install paths are
   `go install` and `brew` only.

## Housekeeping

- `~/.pi/agent/` (empty `auth.json` and `models-store.json`, created
  00:16:36) **came from this run**. My first smoke tests ran pi without
  `PI_CODING_AGENT_DIR`, and I reproduced the cause: `pi --help` creates
  exactly those two files, while `pi --version` creates nothing. **I removed
  `~/.pi/agent/` and the then-empty `~/.pi`.** The 2b agent-dir default
  prevents a recurrence.
- Everything else is under `~/.amq-squad/spike/s3/` or `~/.amq-squad/runtime/`.
  The real `~/.amq-squad/pi-agent/` was never created; the defaults probe
  uses a throwaway HOME.

## Exact commands

```sh
git checkout -b spike/s3-embed v3
npm --cache ~/.amq-squad/spike/s3/npm-cache view @mariozechner/pi-coding-agent name version deprecated engines bin dist-tags --json
npm --cache ~/.amq-squad/spike/s3/npm-cache view @earendil-works/pi-coding-agent name version deprecated engines bin dist-tags dependencies optionalDependencies --json
gh release view v1.3.0 -R earendil-works/pi --json assets      # read-only
gh release view -R sharkdp/fd --json tagName,assets             # read-only
gh release view -R BurntSushi/ripgrep --json tagName,assets     # read-only
cd spike/embedpi
go vet ./... && go test ./...
bash build.sh fetch
bash build.sh node            # decided: stripped tree + fd/rg
bash build.sh node-full       # unstripped tree
bash build.sh bun && bash build.sh upstream
python3 measure.py node; python3 measure.py bun; python3 measure.py upstream
./stripped.sh -- ./bin/embedpi-node --version                  # = env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm ./bin/embedpi-node --version
bash poststrip-test.sh
bash nonative-test.sh node; bash nonative-test.sh bun
bash tui-test.sh node; bash tui-test.sh bun; bash tui-test.sh upstream
bash defaults-test.sh node
bash quarantine-test.sh bun
codesign --verify -vv ~/.amq-squad/runtime/pi-1.3.0-upstream-bun-darwin-arm64-b7ff642cb109/pi
```

The unstripped, (b1) and (b2) columns were measured with the earlier
stamp-only launcher. The decided column was measured with the current
launcher.

Prompt invocation (inside `measure.py`, cwd `~/.amq-squad/spike/s3/cwd`):

```sh
env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm \
  PI_CODING_AGENT_DIR=$HOME/.amq-squad/spike/s3/agent PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 PI_TELEMETRY=0 \
  ./bin/embedpi-node --provider ollama --model qwen2.5:32b --no-session --no-extensions --no-skills \
  --no-prompt-templates --no-themes --no-context-files --no-mcp --no-tools --thinking off \
  --mode json -p 'What is 17 plus 25? Reply with just the number.'
```

Provider config, `spike/embedpi/agent/models.json`. pi's documented Ollama
setup uses an OpenAI-compatible endpoint with a dummy `apiKey`:

```json
{"providers":{"ollama":{"baseUrl":"http://localhost:11434/v1","api":"openai-completions","apiKey":"ollama",
  "models":[{"id":"qwen2.5:32b","contextWindow":32768,"maxTokens":256}]}}}
```

## Open questions for phase 2

1. **Repair, GC and doctor.** Who repairs a rejected runtime, and how do old
   runtime versions and stale `<agent-dir>/bin` links get garbage-collected?
   Should doctor run the full re-hash (190 ms) on a schedule?
2. **Pin-bump procedure.** It must update the lock, the Node floor, fd and rg,
   and AMQ's 0.85.1 evidence pin, then re-measure.
3. **Release CI.** It must do the fetch, pack and sign steps; this cannot be
   `go generate` on developer machines.
4. **Coverage before production.** Test tool calls, the codemode worker, the
   amq-squad pi package and hooks on the decided build.

## Codex review (reviewer-openai, xhigh)

Verdict: **accept S3 PASS, with edits** (read-only review). All edits are
applied above.

- **Release blocker:**
  - A signed and notarized first run. The quarantined launcher hang is
    unexplained; "Signing acceptance criteria" covers it.
- **Majors, before production:**
  - Warm-cache integrity. The stamp-only warm skip was not tamper-safe. It is
    replaced by the launch check, which costs 43.5 ms; non-executables are
    covered only by doctor.
  - Own-Bun worker compatibility. The missing `src/` prefix means (b1) is
    dropped.
- **Minors, as applied:**
  - sample counts and the meaning of "first token";
  - the (b2) preload figure, 7.406 s;
  - the old warm timer excluded the JSON parse;
  - claims of "every runtime path works", "only localhost requests",
    "upstream-tested" and "cannot rebuild" are restated as tested, observed or
    unverified;
  - tools and extensions were disabled in the measured runs;
  - fd and rg are pinned and digest-checked in a managed agent-bin;
  - the launcher sets the 2b defaults itself, with a seat-specific agent dir,
    and they do not isolate the network;
  - the strip saving is split into the measured launcher saving and the
    unpacked saving, with patterns and post-strip tests;
  - the quarantine explanation is withdrawn;
  - 2b defaults, pruning patterns and signing acceptance criteria each have
    their own heading.
