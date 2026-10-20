PASS

# S3: darwin-arm64 build with the embedded pi

Recorded 2026-10-21, launch-dev rerun, branch `spike/s3-embed` (cut from `v3`).
Criterion (phase 1 plan, S3): the binary starts the lead from the unpacked copy
with no pi or Node on PATH and answers one prompt. Fail if no packaging runs
without a host Node or the bundle exceeds 250 MB.

Result: all three builds start pi from `~/.amq-squad/runtime/...` under
`env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm`. In that environment
`command -v pi node bun npm` finds nothing. Each build answered the real prompt
"What is 17 plus 25? Reply with just the number." with `42` from Ollama
`qwen2.5:32b`. This worked in print mode (`-p`) and in the interactive TUI
inside tmux. The largest launcher is 67.3 MB, well under 250 MB.

Roster note: implementer swapped from the Codex subagent in this Claude Code harness to Opus because of the Codex agent sandbox; reviewer family is Codex.

## Measurements

Host: Apple M5 Max, macOS Darwin 25.6.0, Go 1.27.1, Bun 1.4.2 (build only),
Node 22.21.1 (build only, `npm ci`). MB means 10^6 bytes. Timings come from
`measure.py`, the final pass (`spike/embedpi/evidence/measure-*.json`). Cold
means this candidate's runtime directory was deleted first. Unpack/verify time
is reported by the launcher. Wall times are end to end from `subprocess`. Cold
runs are the median of 3, warm runs the median of 5 (or 3 for Ollama prompts).

| Measurement | (a) Node 22.21.1 + npm tree | (b1) our `bun build --compile` | (b2) upstream Bun binary |
|---|---|---|---|
| Launcher binary (`bin/embedpi-*`) | **67.30 MB** | 35.98 MB | 34.23 MB |
| Embedded payload (tar.gz) | 60.61 MB | 32.77 MB | 31.03 MB |
| Unpacked runtime | 223.26 MB, 14,988 files | 82.99 MB, 191 files | 84.40 MB, 209 files |
| Cold unpack + SHA-256 verify | 1,878 ms (1.87–1.90 s) | 371 ms | 375 ms |
| Cold `--version` wall | 2.83 s | 1.19 s | 1.23 s |
| Warm skip (stamp check) | 5.5 ms | 0.10 ms | 0.11 ms |
| Warm `--version` wall | 0.116 s | 0.127 s | 0.154 s |
| Warm full re-hash (`EMBEDPI_VERIFY=full`) | 368 ms | 34 ms | 36 ms |
| **Cold start to first token, Ollama qwen2.5:32b** | **4.05 s** | 2.64 s | 2.28 s |
| Warm start to first token, Ollama | 0.219 s | 0.229 s | 0.263 s |
| Cold launch to request at local stub provider | 2.89 s | 1.21 s | 1.24 s |
| Warm launch to request at stub | 0.144 s | 0.154 s | 0.187 s |
| Answer to the prompt (4 Ollama + 6 stub runs) | `42` every run | `42` every run | `42` every run |
| Interactive TUI lead in tmux, answers | yes | yes | yes |
| Prompt with the native `.node` removed | `42`, exit 0 | `42`, exit 0 | not run (same file as b1) |
| Unpacked exe xattrs | `com.apple.provenance` only | same | same |
| Unpacked exe signature | Developer ID Node.js Foundation (HX7739G8FX), hardened runtime | ad-hoc, valid | ad-hoc, **invalid** ("code or signature have been modified") in the upstream archive itself |
| `spctl --assess --type execute` on exe | rejected ("valid but does not seem to be an app"), still runs | rejected, still runs | invalid signature, still runs |
| Launcher signature / spctl | ad-hoc (Go linker), rejected, runs | same | same |
| Starts without a prompt (not quarantined) | yes | yes | yes |
| Quarantined launcher (`com.apple.quarantine` set) | not run | **blocked**: hung 60 s, killed by alarm, nothing unpacked | not run |
| 250 MB limit | pass | pass | pass |

Notes on the numbers:

- Ollama is shared with other spike agents. An earlier pass that overlapped
  their load measured 13 to 35 s to first token for (a) and 11 to 13 s for
  (b2). The model was resident each time. I preloaded it before every pass,
  and the final pass shows a preload of 0.09 s, so it was already hot. To show
  pi's own start cost apart from Ollama, the stub rows point the same pi
  invocation at a local OpenAI-compatible stub that answers `42` at once.
- On a cold start the gap between unpack time and total time is mostly the
  first exec of a new Mach-O. syspolicyd logs a `GK performScan` for every new
  executable path even without quarantine; its unified log has the entries.
  For (a) that means scanning the 111.7 MB Node binary, plus a first run with
  no Node compile cache. All of this happens once per runtime version.
- Warm skip for (a) costs 5.5 ms because the launcher parses a 3.3 MB manifest
  with 14,988 entries. For (b) the manifest is 36 KB.

## Pinned runtime: what pi needs (evidence from the registry and the installed tree)

- **Package scope.** `npm view @mariozechner/pi-coding-agent` returns version
  `0.73.1` with
  `"deprecated": "please use @earendil-works/pi-coding-agent instead going forward"`.
  Its `latest` tag is 0.73.1, engines `node >=20.6.0`, bin `dist/cli.js`. The
  current package is **`@earendil-works/pi-coding-agent`**. Pinned version:
  **`1.3.0`** (`dist-tags.latest`, published 2026-10-15; the repo is
  `github.com/earendil-works/pi`). The registry also has a `legacy-node20`
  tag at `0.74.2`.
- **Node floor.** The brief expected `>=20.6`. That floor belongs to the
  deprecated 0.73.1. **pi 1.3.0 declares `engines.node: ">=22.19.0"`**. Its bin
  is now **`dist/bundle/cli.js`**, an esbuild bundle (about 8.9 MB of chunks)
  that calls `enableCompileCache()`. The embedded Node is the official
  **`node-v22.21.1-darwin-arm64`** build, SHA-256
  `c170d6554fba83d41d25a76cdbad85487c077e51fa73519e41ac885aa429d8af`, the same
  value as nodejs.org `SHASUMS256.txt`. It is not a copy of the nvm binary.
  The build installs with `npm ci --engine-strict`, so the engine check is
  enforced.
- **Native and optional dependencies.** `koffi` and `@mariozechner/clipboard`
  are **not in the 1.3.0 tree**. The lock has no `optionalDependencies`, and
  grep finds them only in `CHANGELOG.md`: koffi was "replaced ... with a tiny
  vendored native helper", and the clipboard sidecar went the same way. The only
  native code is `@earendil-works/pi-tui/native/<os>/prebuilds/<os>-<arch>/*-platform.node`.
  On darwin-arm64 that is `darwin-platform.node` (52 KB, ad-hoc signed,
  AppKit clipboard and modifier-key state). It is loaded inside `try/catch`.
  The build and one prompt work with it deleted: `evidence/nonative-*.txt` show
  `42` and exit 0 for (a) and (b1). The other `.node` files in tree (a), for
  linux, win32 and darwin-x64, total 0.1 MB and can be pruned. The bundle's
  other bare imports (`kerberos`, `bufferutil`, `utf-8-validate`,
  `@aws-sdk/signature-v4-crt`, `supports-color`) are optional peers and are not
  installed. The only install scripts in the tree (`protobufjs`,
  `@google/genai`) are a version warning and a no-op, so the build uses
  `--ignore-scripts`.
- **WASM.** `@silvia-odwyer/photon-node/photon_rs_bg.wasm` (1.88 MB, image
  resize) is loaded at run time by dynamic `import("@silvia-odwyer/photon-node")`.
  `quickjs-wasi/quickjs.wasm` (0.64 MB, codemode VM) is resolved by
  `require.resolve` under Node. In the Bun binary it is embedded in the
  executable (`/$bunfs/root/quickjs-*.wasm`, via `dist/bun/runtime-setup.js`).
- **Bun.** pi supports Bun directly. `config.ts` detects `$bunfs`/`~BUN`, and
  `getPackageDir()` returns `dirname(process.execPath)` for a Bun binary, so
  every asset must sit beside the executable. The upstream recipe is
  `build:binary` plus `copy-binary-assets` (in the package `scripts`). It
  compiles `dist/bun/cli.js` with the image-resize and codemode workers taken
  from **`src/*.ts`**. Upstream's binary reports `Bun v1.3.14`.
- **Asset list beside a Bun executable** (taken from upstream
  `pi-darwin-arm64.tar.gz`, which our (b1) layout matches): `pi`,
  `package.json`, `README.md`, `CHANGELOG.md`, `theme/{dark,light}.json`,
  `assets/clankolas.png`, `export-html/{template.html,template.css,template.js}`,
  `export-html/vendor/{marked.min.js,highlight.min.js}` (upstream also ships
  the compiled `export-html/*.js`), `photon_rs_bg.wasm`,
  `native/darwin/prebuilds/darwin-arm64/darwin-platform.node`, `docs/`,
  `examples/`. For (a) these are already inside the npm package tree at their
  `dist/` paths.
- **Fetched at run time unless offline.** pi downloads `fd` and `ripgrep` from
  the **latest** GitHub release into `<agent-dir>/bin` when they are missing
  from there and from PATH. It also refreshes its model catalog from pi.dev,
  checks its version and sends install telemetry. With `PI_OFFLINE=1` the TUI
  shows "fd not found. Offline mode enabled, skipping download." (same for
  ripgrep) and continues. None of this is in the payload yet; see open
  question 2.

## What is fetched once and what is embedded

`build.sh fetch` is the **only networked step**. It verifies everything it
fetches against a pinned digest:

1. 118 npm packages via `npm ci --omit=dev --ignore-scripts --engine-strict`
   from the committed `spike/embedpi/package-lock.json`, which carries
   per-package `integrity` hashes. The cache is `~/.amq-squad/spike/s3/npm-cache`.
2. `https://nodejs.org/dist/v22.21.1/node-v22.21.1-darwin-arm64.tar.gz`
   (pinned SHA-256 above). Only `bin/node` and `LICENSE` are used.
3. `https://github.com/earendil-works/pi/releases/download/v1.3.0/pi-darwin-arm64.tar.gz`,
   SHA-256 `1f9ae61cdeae04dd68caf1a1e8ab8ec9b292062f6ccb3252e0a9cbe93b5b16f1`,
   the same value as the release `SHA256SUMS`. Only candidate (b2) uses it.

After that, `build.sh node|bun|upstream` runs offline. It stages the runtime
tree, packs it with `pack.py` into a normalized tar.gz (mtime 0, sorted,
regular files only, modes 0444/0555) plus a manifest with the SHA-256, size and
mode of every file, and `go build`s the launcher. The launcher `go:embed`s
both files. **Embedded:** the whole runtime, meaning the Node binary or Bun
executable, the pi code and every asset listed above. **Not embedded:** `fd`,
`rg`, the provider config (`agent/models.json`, `agent/settings.json`, copied
into `PI_CODING_AGENT_DIR`) and the model itself, which Ollama serves on
localhost. Every prompt run used `PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1
PI_TELEMETRY=0`, and the only request any of them made was to localhost.

## Launcher (`spike/embedpi/main.go`)

- `go:embed assets/*`, where `build.sh` writes `payload.tar.gz` and
  `manifest.json`. A launcher built without a payload refuses to start.
- Unpack target: `~/.amq-squad/runtime/<version>-<sha256[:12]>/`, for example
  `pi-1.3.0-node-22.21.1-darwin-arm64-2e174231d741`. The digest suffix stops
  two builds of the same version from colliding. The plan's
  `runtime/<version>/` is the same idea.
- Cold path: verify the embedded payload's SHA-256, take a `flock` on the
  version, unpack into `.unpack-*`, re-hash every file against the manifest,
  write the `.embedpi-complete` stamp, then `rename` atomically. Archive links,
  path traversal, unexpected entries, missing files and size or mode mismatches
  are all rejected.
- Warm path (idempotent skip): when the directory exists, check the stamp and
  the size and mode of the executable and entry file. It never reads the
  payload. A broken runtime fails closed with "remove it explicitly to repair".
  `EMBEDPI_VERIFY=full` re-hashes every file instead (368 ms for (a), 34 ms for
  (b)).
- `syscall.Exec`s the absolute executable (plus the absolute entry script for
  Node). There is no PATH lookup and no fallback to a host Node.
- `go test ./...` passes 6 tests: cold/warm, tamper under full verify, bad
  payload digest before any write, traversal and symlink swap, archive with a
  symlink or missing file, 4-way concurrent unpack with exactly one cold
  install, and a fast warm path that never loads the payload and fails closed
  on a changed executable or a missing stamp. The cold/warm and tamper paths
  were partly reused from the first attempt's scaffold.

## Packaging decision

**Recommendation: (a), the official Node binary plus the npm-installed pi tree
from a committed lockfile.** (b2) is the fallback if launcher size or cold
start become a hard constraint.

Reasons for (a):

1. **Fidelity to "pinned, unforked pi core".** It is exactly what the npm
   registry publishes, resolved by our own `package-lock.json` with integrity
   hashes, and run on pi's primary runtime. Every runtime path works as
   upstream intends: `dist/bundle`, the codemode worker (`codemode-worker.js`),
   Photon via `node_modules`, and jiti-loaded extensions with real Node module
   resolution. That last one matters, because the `@amq-squad/pi` package is
   "a normal pi package" (v3-plan 4.1).
2. **Reproducible from inputs we control.** It needs `npm ci` and a
   nodejs.org tarball, both pinned by digest. We do not depend on upstream
   continuing to publish darwin binaries, or on a Bun version we cannot pick.
3. **Signing is easier.** The Node binary already carries a Developer ID
   signature (Node.js Foundation, hardened runtime). Upstream's Bun binary
   ships with an **invalid** ad-hoc signature, and our own Bun binary is only
   ad-hoc signed.
4. Its costs are acceptable and well inside the limit: 67.3 MB launcher, about
   2.9 to 4.1 s once per pi version, and the same warm start as Bun (0.12 to
   0.22 s).

Why not (b1), our own `bun build --compile` from the npm dist? It runs and
answers, but it is **not faithful**. pi asks a Bun binary for its codemode worker
as `./src/extensions/codemode/worker.ts` (`resolveCodemodeWorkerSpecifier`),
but compiling from npm `dist/` embeds `/$bunfs/root/extensions/codemode/worker.js`
(`strings` on both binaries shows this). Upstream embeds
`/$bunfs/root/src/extensions/codemode/worker.js`. So codemode would break, and
image resize would fall back to running in-process. Fixing this means
compiling from the upstream source tree (a monorepo build with dev
dependencies), and that build uses Bun 1.4.2 where upstream uses 1.3.14.

(b2), upstream's Bun binary, is the smallest and fastest to start cold, and it
is upstream-tested. Against it: its signature is invalid as shipped, so we
would re-sign it with our Developer ID; it is pinned only by an archive
checksum, not a lockfile; and we cannot rebuild it ourselves.

Both Bun options, (b1) and (b2), are viable fallbacks under the criterion.
Neither needs a host Node.

## Gatekeeper, quarantine and signing

- Files the Go launcher writes get only `com.apple.provenance`, never
  `com.apple.quarantine`. In this unquarantined state every unpacked
  executable, including the ad-hoc and invalid-signature ones, starts with no
  prompt, even though `spctl --assess --type execute` rejects all of them.
  `spctl` assesses standalone tools as apps, so its rejection is not the
  exec-time decision. syspolicyd still runs a `GK performScan` on the first
  exec of each new binary, which accounts for part of the cold start.
- `quarantine-test.sh bun` (`evidence/quarantine-test-bun.txt`) copies the
  launcher and sets `com.apple.quarantine` the way a browser download would.
  Run from a shell, that copy **did not start**: it hung for 60 s until the
  alarm killed it, and nothing was unpacked. Even after
  `xattr -d com.apple.quarantine`, the same file hung until I killed it by
  hand. That is consistent with a pending Gatekeeper dialog on the console,
  which **the owner may need to dismiss**. Because the launcher never ran,
  whether files unpacked by a quarantined parent inherit quarantine is still
  untested.
- I signed nothing. Signing will need:
  - a Developer ID Application signature with hardened runtime and secure
    timestamp on the launcher, then notarization of the distributed artifact
    (zip or pkg; a bare Mach-O cannot carry a stapled ticket);
  - keeping Node's own Developer ID signature byte-identical, which the
    packer already does;
  - re-signing the ad-hoc `darwin-platform.node`, and the Bun executable if
    (b) is chosen, with our Developer ID before packing;
  - checking whether notarization inspects a payload stored as opaque bytes
    inside the launcher. It probably does not, so the unpacked binaries would
    rely on not being quarantined plus their own signatures.

## Exact commands

From the worktree root. Results are under `spike/embedpi/evidence/`.

```sh
git checkout -b spike/s3-embed v3
npm --cache ~/.amq-squad/spike/s3/npm-cache view @mariozechner/pi-coding-agent name version deprecated engines bin dist-tags --json
npm --cache ~/.amq-squad/spike/s3/npm-cache view @earendil-works/pi-coding-agent name version deprecated engines bin dist-tags dependencies optionalDependencies --json
gh release view v1.3.0 -R earendil-works/pi --json assets      # read-only
cd spike/embedpi
go vet ./... && go test ./...
bash build.sh fetch
bash build.sh node && bash build.sh bun && bash build.sh upstream
python3 measure.py node; python3 measure.py bun; python3 measure.py upstream
./stripped.sh -- ./bin/embedpi-node --version                  # = env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm ./bin/embedpi-node --version
bash nonative-test.sh node; bash nonative-test.sh bun
bash tui-test.sh node; bash tui-test.sh bun; bash tui-test.sh upstream
bash quarantine-test.sh bun                                     # may raise a Gatekeeper dialog
codesign --verify -vv ~/.amq-squad/runtime/pi-1.3.0-upstream-bun-darwin-arm64-b7ff642cb109/pi
```

The prompt invocation the lead used (inside `measure.py`, cwd
`~/.amq-squad/spike/s3/cwd`):

```sh
env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm \
  PI_CODING_AGENT_DIR=$HOME/.amq-squad/spike/s3/agent PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 PI_TELEMETRY=0 \
  ./bin/embedpi-node --provider ollama --model qwen2.5:32b --no-session --no-extensions --no-skills \
  --no-prompt-templates --no-themes --no-context-files --no-mcp --no-tools --thinking off \
  --mode json -p 'What is 17 plus 25? Reply with just the number.'
```

Provider config: `spike/embedpi/agent/models.json`, copied to
`$PI_CODING_AGENT_DIR/models.json`. pi's documented Ollama setup uses an
OpenAI-compatible endpoint; `apiKey` is a dummy value that Ollama ignores.

```json
{"providers":{"ollama":{"baseUrl":"http://localhost:11434/v1","api":"openai-completions","apiKey":"ollama",
  "models":[{"id":"qwen2.5:32b","contextWindow":32768,"maxTokens":256}]}}}
```

`agent/settings.json` is `{"enableInstallTelemetry": false, "quietStartup": true}`.

## Open questions for phase 2

1. **Decide (a) or (b2).** If (a), prune the tree: `*.map` is 30.4 MB in 3,755
   files, `*.d.ts` is 23.1 MB in 6,027 files, `@types` is 2.6 MB, and the
   non-darwin `.node` files add a little. Without double-counting overlaps,
   that would cut 51.0 MB and 8,560 files, about 57% of the file count, which
   dominates the cold unpack. Pruning has to be checked
   against pi's `docs/` and `examples/` lookups.
2. **fd and ripgrep.** pi fetches the *latest* release of each from GitHub at
   run time. To keep an offline, pinned runtime, embed pinned `fd` and `rg`
   darwin-arm64 binaries and place them in `<agent-dir>/bin`, where pi looks
   first, or leave them to the host. Both would need signing.
3. **Offline by default?** Should the lead and pi seats always run with
   `PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 PI_TELEMETRY=0`? That covers update
   checks, catalog refresh and telemetry. And where does amq-squad keep
   `PI_CODING_AGENT_DIR`? `~/.amq-squad/pi-agent/` would avoid touching a
   user's own `~/.pi/agent`.
4. **Quarantine end to end.** Test with a signed and notarized launcher
   downloaded through a browser. Does the first start show a prompt? Do
   unpacked children inherit quarantine? Does `brew` or `curl` distribution
   avoid quarantine entirely?
5. **Warm-path integrity.** The fast skip checks the stamp, size and mode, not
   content. Should `doctor` run the full re-hash (368 ms for (a))? Who repairs
   a rejected runtime, and how are old runtime versions garbage-collected?
6. **Update cadence.** pi moved from 0.73 to 1.3 with a scope rename and a
   Node floor bump in this window. Phase 2 needs a pin-bump procedure: update
   the lock, the Node floor check and a re-measure.
7. **Workspace and CI.** `spike/embedpi` is its own Go module. The production
   launcher will need the payload build in release CI, not in `go generate`
   on developer machines.
