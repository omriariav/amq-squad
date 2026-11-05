# 3.1.2 hotfix: the HostFacts duplicate in the pi extension

PR #883, branch `v3-312-hotfix`, from `origin/v3` at `e10bbef40c072b186c288fc5226c613a93557e9e`.

## The regression

The released 3.1.1 lead (`amq-squad lead --team`, pi 1.3.0 from the
runtime `pi-1.3.0-node-22.21.1-darwin-arm64-0a8a721e40df`) exits at once:

```
Error: Failed to load extension ".../pi/extensions/amq-squad.ts": Failed to load extension: ParseError: Identifier 'HostFacts' has already been declared.
 .../pi/extensions/lib/firstturn.ts:114:13
Hint: Start without extensions using "pi -ne".
```

`pi/extensions/lib/firstturn.ts` declared both `export type HostFacts`
(line 32: context `--json --host`'s host object, used by `hostLine` and
`hostOf`) and `export class HostFacts` (line 114: the first-turn host-line
holder that `amq-squad.ts` constructs). TypeScript and Babel both treat a
type alias and a class of one name in one module as a duplicate
declaration.

Why nothing caught it: the pi suite runs under Node's type stripping
(`node --test test/*.test.ts`), which deletes the type alias and runs the
class, so all 110 tests passed, `phase.test.ts` (which imports
`firstturn.ts`) among them. The lint (`pi/scripts/lint.mjs`) checks that
each file is erasable with `stripTypeScriptTypes`, on the stated assumption
that "pi's jiti loader and Node's type stripping read the same code". They
do not. pi 1.3.0 loads an extension through jiti, and its bundled Node
runtime passes `tryNative: false` (`dist/core/extensions/loader.js`,
`loadExtensionModule`), so every extension module goes through jiti's
Babel TypeScript transform. Babel tracks scope and refuses the second
`HostFacts`.

## The commits

| Declaration | Commit | Merged via |
|---|---|---|
| `export type HostFacts` | `f95b70ab` (2026-11-01 09:43, brief 31-3 PR 2, sections 4 and 5: the host line from status and context) | #837 (`v3-31-3b`) |
| `export class HostFacts` | `43933bc4` (2026-11-01 10:08, the plan reviewer on #837: the line only on a key change) | #837 (`v3-31-3b`) |

Both commits are in `v3.1.0` as well as `v3.1.1`, and neither is in
`v3.1.0-rc.1` (rc.1 has no `firstturn.ts`). `firstturn.ts` is
byte-identical at v3.1.0 and v3.1.1, and `amq-squad.ts` imports the class
at both. So this is a 3.1.0 regression, first seen on 3.1.1. It went
unseen because the installed binary on PATH stayed rc.1 through the 3.1.0
and 3.1.1 cuts (`release-v3.1.1/README.md`), so no lead ran on 3.1.0. I did
not run a 3.1.0 binary. The plan reviewer verified the tags.

The scan for the same shape elsewhere covered every `.ts` under
`pi/extensions/` and `pi/test/` (28 files at the base). It ran two checks:
jiti's static transform with `tryNative: false` on each file, and a scan of
top-level declarations plus `import` and `import type` bindings for one name
declared twice. Both found only `firstturn.ts` `HostFacts` (type plus
class).

## The fix

The type alias is renamed `ContextHost`, after Go's `hookHost` (the
`host` object of `context --json --host`, `internal/cli/hook_context.go`).
Its four uses in `firstturn.ts` are updated: the declaration, `hostLine`'s
parameter, `hostOf`'s return type and its cast. The class keeps the name
`HostFacts`, which `amq-squad.ts` and `phase.test.ts` use. No other file
named the type, so no test changed. A comment above the alias says why it
has its own name. Types only, so there is no behaviour change.

The plan reviewer preferred renaming the class. I kept the alias rename
because it is the smaller change. The alias has four uses, all in
`firstturn.ts`, so one file changes. The class is used in `amq-squad.ts`
(its import and its `new`) and in `phase.test.ts` (its import and its
`new`), and three comments name it. Renaming it would touch three files,
including the extension's entry and a test.

No `tsc --noEmit` step was added. TypeScript does not ship offline here.
It is absent from `pi/node_modules`, from the 3.1.1 runtime's
`pi/node_modules`, and from the S3 npm cache the runtime build uses. The
pi package's lint says it runs "without a compiler download" (brief 4-1).
Adding `typescript` as a devDependency would change the lockfile and need
a registry fetch. That is a separate decision for the reviewer; row 1
below covers the class of defect without a compiler.

## The row: `pi/test/piload.test.ts`

Choice: (b), pi's own loading code imported in the suite, plus (a), pi's
bundled CLI started as a process. (b) alone already fails on the
duplicate. (a) is the lead's actual entry (`dist/bundle/cli.js`, the
`-entry` that `scripts/build-pi-runtime.sh` packs), where the bundle's
`isBundledNode` path selects `jiti/static` with `tryNative: false`. The
rows use the pinned devDependency `@earendil-works/pi-coding-agent@1.3.0`,
which `npm ci` installs before `npm test` (the `pi-test` target in
`make ci`). That is the same package tree the runtime ships, so CI runs
the first three rows without any runtime. If the devDependency is absent,
the rows fail; they never skip.

1. **pi's transform (jiti static, tryNative off) takes every extensions/ .ts.**
   Every file under `extensions/`, not only the ones `amq-squad.ts`
   reaches. jiti returns `exports.__JITI_ERROR__ = {...}` instead of
   throwing, and the row asserts that marker is in no output.
2. **pi's loadExtensions loads extensions/amq-squad.ts.** It imports
   `dist/core/extensions/loader.js` and asserts no errors and one
   extension.
3. **pi's bundled CLI starts with -e amq-squad.ts (offline, stops at no key).**
   The command is `node cli.js --offline --no-session -p -e amq-squad.ts hi`.
   It runs with a scratch HOME, `PI_CODING_AGENT_DIR` and cwd, with
   `PI_OFFLINE=1` and only PATH inherited, so there is no provider key. The
   row asserts that the output has no `Failed to load extension` and that
   it does have `No API key found`. In `main.js` the extension error check
   exits before the model check, so seeing the model message proves the
   load passed. No model is called.
4. **a staged pi runtime's own node starts with -e amq-squad.ts.** Same as
   row 3, but it uses the runtime's own `node/bin/node`, with
   `--require pi/preload/runtime-check.cjs`. The preload does nothing
   without the launcher's manifest variables. The runtime comes from
   `build/pi/stage` (`scripts/build-pi-runtime.sh build`) or from
   `AMQ_SQUAD_PI_RUNTIME`. If none is staged, the row skips with this
   reason: `no staged pi runtime at <dir>: run scripts/build-pi-runtime.sh
   build, or set AMQ_SQUAD_PI_RUNTIME to an unpacked runtime`. **This row
   is local-only: CI stages no runtime, so it skips there.** Rows 1 to 3
   are the CI guard. The reviewer decides whether CI should also run
   `build-pi-runtime.sh build` (it needs the S3 download cache).

Without `AM_ME` and `AM_ROOT` the package is inactive. Every module it
imports is evaluated, which is where the ParseError arises. Then the
factory registers only a `session_start` note, so no row reaches a squad
or the network.

### Revert run

I put `firstturn.ts` back to `origin/v3`, keeping the row, and ran it with
`AMQ_SQUAD_PI_RUNTIME` pointing at the released 3.1.1 runtime (read only):

```
✖ pi's transform (jiti static, tryNative off) takes every extensions/ .ts
✖ pi's loadExtensions loads extensions/amq-squad.ts
✖ pi's bundled CLI starts with -e amq-squad.ts (offline, stops at no key)
✖ a staged pi runtime's own node starts with -e amq-squad.ts
ℹ tests 4  ℹ pass 0  ℹ fail 4  ℹ skipped 0
  extensions/lib/firstturn.ts: exports.__JITI_ERROR__ = {..."line":114,"column":13,"code":"ParseError","message":"Identifier 'HostFacts' has already been declared. "}
  actual: [ { path: '.../pi/extensions/amq-squad.ts', error: "Failed to load extension: ParseError: Identifier 'HostFacts' has already been declared.  \n .../pi/extensions/lib/firstturn.ts:114:13" } ]
  actual: `Error: Failed to load extension ".../pi/extensions/amq-squad.ts": Failed to load extension: ParseError: Identifier 'HostFacts' has already been declared. ...
```

On the duplicate the whole pi suite gave tests 114, pass 110, fail 3,
skipped 1. The 110 old tests still pass. After restoring the fix, all four
rows pass against the released runtime and against the fresh `build/pi/stage`.

## Load proof: the built lead in tmux

I built the binary the release way:
`scripts/build-pi-runtime.sh all` with `NPM_OFFLINE=1`, and
`AMQ_SQUAD_PI_DL` and `AMQ_SQUAD_NPM_CACHE` pointing at scratch copies of
the S3 caches. The script needs no tag; it versions with `git describe`,
which gave `v3.1.1-6-gda8d97b3`.

```
{"files":6447,"manifest_sha256":"78652b333d893f8c6d9ac96e18dacef1a88886c8e12ac5ecc9e556db3d1f7f89","payload_bytes":55703943,"payload_sha256":"31613f1e96b91f8713ad3def5baa03afcf413d0d84fd1b1deaf102251fb1d75c",...}
{"binary":".../build/pi/amq-squad","binary_bytes":72345074}
```

I started each lead on its own tmux socket. The cwd was a scratch
directory outside any repository (`GIT_CEILING_DIRECTORIES` set, no
squad.yaml). The command used `env -i` with a scratch HOME, so the
runtime, the agent dir and the solo root all landed under that HOME. PATH
was `/usr/bin:/bin:/usr/sbin:/sbin`, so there was no `amq`, and no key was
set. The command was `amq-squad lead --team -- --offline`. I captured the
pane at 10 s, then killed the tmux server.

Fixed (`build/pi/amq-squad`, runtime `...-31613f1e96b9`): alive at 10 s
(`dead=0 cmd=amq-squad`). The extension's team screen and commands are on
screen:

```
 amq-squad: lead - · root <scratch HOME>/.amq-squad/solo/proj-79837012c343/root
 seats (1)
   lead  -  stopped
 tasks (0)
   No tasks yet; add one with /add.
 train
   No merge train has run yet.
 waiting on you (0)
   Nothing is waiting on you.
 /add  /reset  /dismiss  /answer  /widen  /team hide
<scratch>/proj
0.0%/131k (auto)                                              qwen3.6:27b • medium
```

Before (the same tree with `firstturn.ts` from `origin/v3`, built to a
scratch path): the runtime digest is `...-0a8a721e40df`, the same
directory as the released 3.1.1 runtime. Its pane was dead at 3 s
(`dead=1 status=1`):

```
Error: Failed to load extension "<scratch HOME>/.amq-squad/runtime/pi-1.3.0-node-22.21.1-darwin-arm64-0a8a721e40df/pi/extensions/amq-squad.ts": Failed to load extension: ParseError: Identifier 'HostFacts' has already been declared.
 <scratch HOME>/.amq-squad/runtime/pi-1.3.0-node-22.21.1-darwin-arm64-0a8a721e40df/pi/extensions/lib/firstturn.ts:114:13
Hint: Start without extensions using "pi -ne".
Pane is dead (status 1, Thu Nov  5 14:47:36 2026)
```

Afterwards I rebuilt the fixed binary. The payload matched the first
fixed build (`manifest_sha256 78652b33...`).

## Gates

Run at `da8d97b3`, then all three again on the commit that adds the
RELEASING.md step and these corrections. Both runs had the same results;
the second run's pi suite had the stage, so it was pass 114, skipped 0.


- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0. 56
  packages ok, 0 FAIL. The tail is `ok .../scripts 2.932s` and
  `ok .../scripts/pipack 1.137s`.
- `cd pi && npm ci --offline --ignore-scripts && npm test`: tests 114,
  pass 113, fail 0, skipped 1. The skip is the staged row, because no
  stage existed yet. After the build it runs: pass 114, skipped 0. `npm run lint`
  gives `lint ok: 29 files, pi 1.3.0`.
- `NPM_FLAGS=--offline make ci` with the PyYAML venv on PATH: exit 0.
  pi-test gave tests 114, pass 113, fail 0, skipped 1.

## The cut procedure gains a step

`RELEASING.md`, Patch Release Checklist, step 6 (in #883):

> Start the lead on the installed new binary before the release counts as
> done. Install the binary built from the tag (`scripts/build-pi-runtime.sh
> all`, the one attached to the release). Then either restart the squad's
> own lead on it, or run `amq-squad lead --team -- --offline` in a scratch
> repository (`git init` in an empty directory, no `squad.yaml`, a scratch
> `HOME`). Record the lead's first screen, a tmux `capture-pane` at about
> 10 s, in the release evidence. A dead pane or any `Failed to load
> extension` line means the release is not done.

## For the 3.1.2 release notes

The lead starts again on the built-in pi. In 3.1.0 and 3.1.1 the pi extension
declared `HostFacts` twice, as a type and as a class. pi's loader refused
the module, and `amq-squad lead` exited with "Identifier 'HostFacts' has
already been declared". The pi suite now loads the extension through pi
1.3.0's own loader and bundled CLI, so it fails on what pi fails on.
