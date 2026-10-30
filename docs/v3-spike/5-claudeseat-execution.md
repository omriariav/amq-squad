# 3.0.1 t6: a Claude seat's config dir is born past first-run onboarding (execution log)

Branch `v3-301-t6`, from `v3` at a1a67151. Reviewer ruling 2026-10-30: the seat config is born complete. Defect found 2026-10-30 in session v31: a Claude seat's per-seat `CLAUDE_CONFIG_DIR` had no onboarding state, so the pinned Claude Code (2.1.314) opened its first-run flow (the theme picker first) in the pane, the seat never reached its first turn, its hooks never reported, and the scheduler quarantined it as `claude_hooks_silent`. Its worktree was also untrusted, so the trust dialog would have followed.

## What changed

`claudeseat.Write` now writes three facts into the launch's config dir, beside the permission mode and the empty allow list it already wrote, all by value (no symlink anywhere in the dir):

| fact | where | written |
| --- | --- | --- |
| onboarding complete | `.claude.json` | `"hasCompletedOnboarding": true` |
| theme | `settings.json` | `"theme": "dark"` |
| hooks plugin enabled | `settings.json` and `plugins/` | `"enabledPlugins": {"amq-squad@amq-squad": true}`, with the plugin installed at user scope from the embedded files (unchanged since 2b-2 milestone B) |
| worktree trusted | `.claude.json` | `"projects": {"<worktree>": {"hasTrustDialogAccepted": true}}`, under the path as recorded and, when it differs, under its symlink-resolved form |

The worktree pointer (`amq-squad-worktree.json`, a managed file) now also records the trust keys (`"trusted": [...]`), so the digest reads the keys the launch wrote and does not depend on the filesystem at check time.

The digest (`claudeseat.Digest`, the same digest the launch record's `claude_config_sha256` carries for the mode) now covers the first-run facts of `.claude.json` by value: the value of `hasCompletedOnboarding` and of `projects[k].hasTrustDialogAccepted` for each recorded trust key, in a canonical JSON form, or `absent` or `unparsable`. The file is read through the anchored descriptor like the managed files; a symlink there fails. Only those values are hashed because Claude Code rewrites the rest of `.claude.json` as it runs (`numStartups`, `firstStartTime`, its own project state under the canonical repository root); those keys do not move the digest. The theme and the plugin are in `settings.json` and `plugins/`, which were already hashed byte for byte.

`claudeseat.Check` (seat-exec's check, run at preflight and again as the last step before exec) refuses, after the digest and mode checks:

- `claude_onboarding_missing` when `.claude.json` is missing, unreadable or lacks `hasCompletedOnboarding: true`, or `settings.json` names no theme;
- `claude_trust_missing` when no worktree is recorded or `.claude.json` lacks `projects[<worktree>].hasTrustDialogAccepted: true`;
- `claude_hooks_missing` when the plugin is not enabled or does not run the guard (existing).

### Fix round (Codex pass): trust and permissions beyond the launch's keys

The first pass hashed and checked only the seeded values. A dir with the seeded keys plus `projects[<canonical repository root>].hasTrustDialogAccepted: true` kept the same digest and passed Check. The pinned binary checks that root first, so the trust would have extended to every sibling worktree. An extra `allowedTools`, or any other permission input, was ignored the same way. Now:

- **Check is an allowlist.** It runs only before exec, on the dir exactly as the launch wrote it, and it refuses `claude_config_unexpected` (new) for any `.claude.json` key outside three groups. The first is the launch's own keys: `hasCompletedOnboarding`, and `projects` with the recorded trust keys only, each holding `hasTrustDialogAccepted`. The second is the binary's top-level bookkeeping set `pI`. The third is its project-entry bookkeeping set `kI`. So another project's entry is refused whatever it holds, and so is any permission input at either level. The detail names keys only, never a value.
- **The digest covers trust and permission inputs anywhere.** It reads `hasCompletedOnboarding`, the recorded keys' trust, and the non-default permission inputs (listed below) of every project entry and of the top level. An entry that gains trust or a permission input at run time moves the digest, under any project key. Defaults (null, false, empty string, empty list or map, no approved key) and every other key are left out. Maps whose values may hold a secret enter by key names only. A stored key or a key helper enters as set or not set, and approved API keys as a count. No credential value is hashed or printed.

The reviewer asked for the digest to cover the set of `projects` keys itself, so that any added entry moves it. I did not do that literally. Claude Code 2.1.314 keys its own project state on the canonical repository root, which differs from a seat's worktree. Its project-config reader falls back to the default entry `fEe` (`allowedTools:[]`, `mcpServers:{}`, `hasTrustDialogAccepted:!1`, and so on), so its first write at run time can create that root entry with defaults only. A digest over the bare key set would then quarantine every seat (`claude_config_changed`) at its first context call. Instead the digest covers the set of `projects` keys that carry trust or a permission input. Check still refuses any extra entry before exec. If the live row shows that 2.1.314 never writes such an entry, the digest can be tightened to the bare key set.

`seatexec` exports the codes as `ClaudeOnboardingMissing`, `ClaudeTrustMissing` and `ClaudeConfigUnexpected`. No doctor check was added: seat-exec refuses by name before exec, and every Claude seat's dir is written by the launch, so there is nothing on the host for doctor to inspect ahead of a launch.

## Field names and their sources

Binary: `~/.amq-squad/bin/claude`, the registry's pin, sha256 `21d6e7b9a6f07b4cfcb7df13760b3b72aa25e084e13a5354413382987ba832ed`, every chunk stamped `Version: 2.1.314`. Read only with `strings -n 6` (no execution). Each quote below is minified code from those strings.

| name | what it gates | source in 2.1.314 |
| --- | --- | --- |
| `$CLAUDE_CONFIG_DIR/.claude.json` (the global config) | where the next two keys are read | `function XSr(){let t=`.claude${o6()}.json`;return U(process.env.CLAUDE_CONFIG_DIR\|\|g_(),t)}`; `o6()` returns `""` in the prod build unless `CLAUDE_CODE_CUSTOM_OAUTH_URL` is set (a `CLAUDE_CODE_*` host variable the seat scrub drops). A legacy `$CLAUDE_CONFIG_DIR/.config.json` would take precedence when non-empty (`y_()`); the seat dir has none. Writes are temp file then rename (`File ${o} written atomically`). |
| `hasCompletedOnboarding` | the Onboarding screen (theme picker first: "Choose the text style that looks best with your terminal") | the interactive startup sequence `qMi`: `if(_.hasCompletedOnboarding&&i.CLAUDE_CODE_POWERUP_ONBOARDING!=="banner"&&...!=="step")return null; ... import(...).then(({Onboarding:S})=>...)`. Onboarding's own completion writes `hasCompletedOnboarding:!0,lastOnboardingVersion:...` (`KMi`). Claude Code's plugin-eval harness seeds a fresh home with `{hasCompletedOnboarding:!0,autoUpdates:!1,bypassPermissionsModeAccepted:!1}`. |
| `projects[<path>].hasTrustDialogAccepted` | the TrustDialog ("Is this a project you created or one you trust?") | the next step of the same sequence: `if(O=Es(),O&&...)return null; ... TrustDialog`. `Es()` calls `fI()`: `if(e.projects?.[n]?.hasTrustDialogAccepted)return!0;return Mh(e,ve())!==null`. Claude Code's own error text: `set projects[${_(u)}].hasTrustDialogAccepted: true in ${are()}`. |
| the trust key | which `projects` key counts | `fI()` first checks `projects[zOe()]`, where `zOe()` is the canonical repository root (`I0` to `Ei` to `Rt`, which follows a worktree's `.git` file to the main repository). It then runs `Mh`/`Ih`, which walks from the cwd up to the git root (`Gy`, the worktree's own top level) and accepts any `projects[dir].hasTrustDialogAccepted`. A seat's cwd is its worktree root (seatlaunch compiles `CWD: s.Worktree`), so the worktree path is a sufficient key. Claude Code's headless runner writes the raw and the realpath forms of each path; the launch writes both when they differ. |
| `theme` | the theme | in 2.1.314 `theme` is a user-intent setting: `var $Pe=["theme",...]; function Qs(n,i){... Se(t)?.[n] ...}` reads the settings sources first (userSettings is `$CLAUDE_CONFIG_DIR/settings.json`), then the legacy global config value only when it differs from the default, then the default `"dark"`. `/theme` saves through `p9("theme",...)`, which writes userSettings. Valid values: `["auto","dark","light","light-daltonized","dark-daltonized","light-ansi","dark-ansi"]`. With onboarding complete no screen asks for a theme; the seat names `dark` explicitly. |
| `enabledPlugins["amq-squad@amq-squad"]` | the hooks plugin loads | `settings.json` (userSettings); unchanged since 2b-2 milestone B. |
| bookkeeping, top level (`pI`) | allowed by Check, never in the digest | `var pI=new Set(["numStartups","skillUsage","pluginUsage",...,"firstStartTime","claudeCodeFirstTokenDate","startupPrefetchedAt"])`: 43 keys, copied verbatim into `bookkeepingGlobal`. The binary uses it in `TI(e,n)`, its test for whether two configs differ only in bookkeeping: every changed top-level key must be in `pI`, and `projects` is compared entry by entry through `Nh`. |
| bookkeeping, project entry (`kI`) | allowed by Check in a trust key's entry, never in the digest | `kI=new Set(["lastCost",...,"lastSessionId","lastGracefulShutdown",...,"hasClaudeMdExternalIncludesWarningShown","history","projectOnboardingSeenCount","hasCompletedProjectOnboarding","devIntentsDetected"])`: 29 keys, copied verbatim into `bookkeepingProject`. `Nh(e,n)` holds when every changed project key is in `kI`. |
| project permission inputs | in the digest (non-default), refused by Check | The project entry defaults `var fEe={allowedTools:[],mcpContextUris:[],mcpServers:{},enabledMcpjsonServers:[],disabledMcpjsonServers:[],hasTrustDialogAccepted:!1,hasClaudeMdExternalIncludesApproved:!1,hasClaudeMdExternalIncludesWarningShown:!1}`, minus the one that is bookkeeping (`hasClaudeMdExternalIncludesWarningShown`, in `kI`). `allowedTools` and `hasTrustDialogAccepted` are also the binary's `A$i=["allowedTools","hasTrustDialogAccepted"]`. `mcpServers` is the local MCP scope: `case"local":{if(Ua().mcpServers?.[e])...` (names only in the digest). `hasClaudeMdExternalIncludesApproved` gates `ClaudeMdExternalIncludesDialog`. Added defensively: `enableAllProjectMcpServers` (the binary's settings key for approving every `.mcp.json` server) and `permissions` (named by the reviewer; no reader of it in `.claude.json` appears in the strings). |
| top-level permission inputs | in the digest (non-default), refused by Check | `mcpServers`, the user MCP scope: `case"user":{if(ue().mcpServers?.[e])...` (names only). `env`, the legacy global environment: `Je()` default `env:{}` and `getLegacyEnvMap(){return this.legacyEnvMapAtLaunch??ue().env}` (names only). `primaryApiKey`: `let e=ue();if(!e.primaryApiKey)return null;return{key:e.primaryApiKey,source:"/login managed key"}` (set or not set). `customApiKeyResponses`: default `{approved:[],rejected:[]}`, read through `Smr(...)==="approved"` for `ANTHROPIC_API_KEY` (approved count). `apiKeyHelper`, named in the binary's global-config key list `Eno` (set or not set). `bypassPermissionsModeAccepted`: the `tengu_migrate_bypass_permissions_accepted` migration, and the plugin-eval seed sets it false. Added defensively: `allowedTools`, `permissions`, `enableAllProjectMcpServers` and `enabledMcpjsonServers` at the top level. |
| plugin root | where installed plugins resolve | `function oc(){let e=i.CLAUDE_CODE_PLUGIN_CACHE_DIR;if(e)return Du(e);return A(Ne(),J())}`, where `Ne()` is the config home (`legacyPath:H(Ne(),".config.json")`) and `J()` is `"plugins"`, or `"cowork_plugins"` under `CLAUDE_CODE_USE_COWORK_PLUGINS`. So the files are `$CLAUDE_CONFIG_DIR/plugins/installed_plugins.json` (`jk()`), `plugins/known_marketplaces.json` (`b4()`) and the install path each entry names. Both override variables are `CLAUDE_CODE_*` names: the seat env scrub drops them, and a settings `env` block naming them is `claude_settings_env_forbidden`. |

Copy, not link. Claude Code writes under the plugin root as it runs. It rewrites `known_marketplaces.json` under a lock and sets an entry's `lastUpdated` (`Xdt`). It also writes `installed_plugins.json` and the cache. A `plugins/` link into the owner's `~/.claude/plugins` would therefore have the seat write the owner's files. The plugin stays copied from the embedded files, as since 2b-2 milestone B, and `Digest` refuses a symlinked managed file. The new row `TestWriteBearsTheFirstRunFacts` walks the fresh dir and fails on any symlink.

## Not settled from the binary (owner-run verification)

The strings show what each gate reads. They do not show the whole runtime. These need the live row:

1. Whether 2.1.314 keeps the seeded `projects[<worktree>]` entry while it runs. It writes its own state under the canonical repository root, which is a different key, and its config writes spread the existing `projects`. A cleanup that pruned entries would show as `claude_config_changed` at the seat's first context call.
2. Whether a seat's run rewrites `plugins/known_marketplaces.json` (`lastUpdated`) or `installed_plugins.json`. That would be the same drift. It is not new in t6, because those files were already hashed, but no Claude seat has reached a first turn before, so it was never observed.
3. Conditional startup screens that the dir cannot seed, because each depends on the account, the server or the worktree: the policy-update dialog (`GroveDialog`, account side), `ProTrialStartScreen`, `ClaudeMdExternalIncludesDialog` (only when a CLAUDE.md includes files outside the project), the `.mcp.json` server approvals (only when the worktree has one), `ApproveApiKey` (only with `ANTHROPIC_API_KEY`, which the scrub drops), and `BypassPermissionsModeDialog` (only in `bypassPermissions` mode).
4. Whether the path normaliser `Nn` in the trust walk is a realpath. Both forms are written, so this is belt and braces. The live row confirms the dialog does not appear.

`lastOnboardingVersion` is written by onboarding but no gate reads it in this build. Only key lists name it. It is not seeded.

## Rows

| row | test |
| --- | --- |
| a fresh dir written by the core has the three facts under the expected names, written by value (no symlink); fails when any writer is reverted | `TestWriteBearsTheFirstRunFacts` (internal/claudeseat) |
| a worktree reached through a symlink is trusted under both keys, and the resolved key's fact is in the digest | `TestWriteTrustsTheResolvedWorktreeToo` (internal/claudeseat) |
| the digest covers each fact (onboarding false, trust dropped, `.claude.json` removed, theme dropped, plugin disabled); Claude Code's own `.claude.json` keys do not move it | `TestDigestCoversTheFirstRunFacts`, `TestDigestCoversOnlyTheManagedFiles` (internal/claudeseat) |
| Check names the missing fact even when the launch recorded the incomplete dir's digest | `TestCheckNamesTheMissingFirstRunFact` (internal/claudeseat) |
| Check refuses `claude_config_unexpected` for another project's entry (the canonical root trusted, or defaults only) and for each permission input (`allowedTools` at the top level and in the worktree's entry, local and user MCP servers, a global env, a stored key, `permissions`); the detail carries no value | `TestCheckRefusesTrustOrPermissionsBeyondTheLaunch` (internal/claudeseat) |
| bookkeeping keys (`pI`, `kI`) pass Check and keep the digest, and so do the defaults entry for the canonical root and Claude Code's other state (`userID`) | `TestBookkeepingKeysPassAndKeepTheDigest` (internal/claudeseat) |
| trust or any permission input added at run time, in any project entry or at the top level, moves the digest (13 cases) | `TestDigestCoversTrustAndPermissionsAnywhere` (internal/claudeseat) |
| seat-exec refuses `claude_config_unexpected` for the canonical root trusted and for `allowedTools`, with the digest re-recorded | `TestClaudeSeatConfigDirIsChecked` (internal/seatexec), two more cases |
| seat-exec refuses a dir missing any one fact (`claude_onboarding_missing` twice, `claude_trust_missing`, `claude_hooks_missing`), and a trust dropped after the launch is `claude_permission_mismatch` by digest; no exec in any case | `TestClaudeSeatConfigDirIsChecked` (internal/seatexec), five new cases |

Revert check, each change reverted alone, with `go test ./internal/claudeseat ./internal/seatexec`:

| reverted | rows that fail |
| --- | --- |
| the onboarding writer | `TestWriteLaysOutTheSeatConfigDir`, `TestDigestCoversOnlyTheManagedFiles`, `TestWriteBearsTheFirstRunFacts`, `TestCheckNamesTheMissingFirstRunFact`, and every seat-exec row that execs a Claude seat |
| the theme writer | `TestWriteLaysOutTheSeatConfigDir`, `TestWriteBearsTheFirstRunFacts`, `TestCheckNamesTheMissingFirstRunFact`, and the same seat-exec rows |
| the trust writer | `TestWriteLaysOutTheSeatConfigDir`, `TestDigestCoversOnlyTheManagedFiles`, `TestWriteBearsTheFirstRunFacts`, `TestWriteTrustsTheResolvedWorktreeToo`, `TestDigestCoversTheFirstRunFacts/trust_dropped`, `TestCheckNamesTheMissingFirstRunFact`, and the same seat-exec rows |
| the first-run entry in the digest | `TestDigestCoversTheFirstRunFacts` (onboarding false, trust dropped, `.claude.json` removed), `TestWriteTrustsTheResolvedWorktreeToo`, `TestClaudeSeatConfigDirIsChecked/trust_dropped_after_the_launch` |
| the onboarding check | `TestCheckNamesTheMissingFirstRunFact` (three cases), `TestClaudeSeatConfigDirIsChecked` (two cases) |
| the trust check | `TestCheckNamesTheMissingFirstRunFact` (two cases), `TestClaudeSeatConfigDirIsChecked` (one case) |
| fix round: the extra-entry rule in Check | `TestCheckRefusesTrustOrPermissionsBeyondTheLaunch/canonical_root_trusted`, `TestClaudeSeatConfigDirIsChecked/canonical_root_trusted,_digest_re-recorded` |
| fix round: the top-level key rule in Check | `TestCheckRefusesTrustOrPermissionsBeyondTheLaunch` (top-level allowedTools, stored key, user MCP server, global env, permissions), `TestClaudeSeatConfigDirIsChecked/allowedTools,_digest_re-recorded` |
| fix round: the entry key rule in Check | `TestCheckRefusesTrustOrPermissionsBeyondTheLaunch` (worktree allowedTools, local MCP server) |
| fix round: the unexpected-config call in Check | `TestCheckRefusesTrustOrPermissionsBeyondTheLaunch` (all nine cases), `TestClaudeSeatConfigDirIsChecked` (two cases) |
| fix round: the `pI` allowance, the `kI` allowance | `TestBookkeepingKeysPassAndKeepTheDigest` (each) |
| fix round: other project entries in the digest | `TestDigestCoversTrustAndPermissionsAnywhere` (seven project-level cases) |
| fix round: top-level inputs in the digest | `TestDigestCoversTrustAndPermissionsAnywhere` (six top-level cases) |
| fix round: hashing the whole file instead of the projection | `TestBookkeepingKeysPassAndKeepTheDigest`, `TestDigestCoversOnlyTheManagedFiles`, `TestDigestCoversTheFirstRunFacts` |

No test reaches the network or touches the real HOME. The new rows use temp dirs and files only, and seat-exec's fixture mocks the exec. The pre-existing live probe row (`TestModeProbeOnTheInstalledClaude`) runs the real Claude Code only under `AMQ_SQUAD_LIVE=1`, with a scratch HOME, config dir and cwd. The `.claude.json` fixtures carry no credential-shaped value. The canary in the refusal row (`canary-not-a-secret-value`) is a plain word, not a credential shape.

## Owner-run row (3.0.1 owner run)

What it checks: a fresh Claude seat launched through seat-exec reaches its prompt with no interactive screen, and the context hook reports the digest. Run it with the 3.0.1 build that carries this change installed as `amq-squad`, on the owner's host. Paths are examples. The checks read only named keys; never print `.claude.json` whole, because after a run it carries account data.

```sh
amq-squad --version                       # the 3.0.1 build with t6
git -C /Users/omri/code/amq-squad fetch -q origin
git -C /Users/omri/code/amq-squad worktree add /Users/omri/scratch-t6-live -b seat/t6-live origin/v3
printf '# t6 live probe\nReply "ready" to the first prompt and run nothing.\n' > /Users/omri/scratch-t6-role.md
cat > /Users/omri/scratch-t6-seats.json <<'EOF'
{"project_root": "/Users/omri/code/amq-squad", "session": "t6-live",
 "rules_path": "/Users/omri/code/amq-squad/.amq-squad/team-rules.md",
 "seats": [{"handle": "t6probe", "runtime": "claude", "model": "sonnet", "effort": "low",
            "role_file": "/Users/omri/scratch-t6-role.md",
            "worktree": "/Users/omri/scratch-t6-live", "branch": "seat/t6-live"}]}
EOF
amq-squad launch --seats /Users/omri/scratch-t6-seats.json --dry-run
amq-squad launch --seats /Users/omri/scratch-t6-seats.json        # add --decision ID=CHOICE as printed for the trust action
```

Then, with `ROOT` set to the session root the launch prints (by default `/Users/omri/code/amq-squad/.agent-mail/t6-live`):

```sh
ROOT=/Users/omri/code/amq-squad/.agent-mail/t6-live
D=$(ls -dt "$ROOT"/extensions/amq-squad/launch/t6probe/claude-config/* | head -1)
jq '{onboarded: .hasCompletedOnboarding, trusted: .projects["/Users/omri/scratch-t6-live"].hasTrustDialogAccepted}' "$D/.claude.json"
jq '{theme, plugin: .enabledPlugins["amq-squad@amq-squad"], mode: .permissions.defaultMode, allow: .permissions.allow}' "$D/settings.json"
tail -n 3 "$ROOT/extensions/amq-squad/claude-config/t6probe.jsonl"   # hook reports: digest and ok
amq-squad status --session t6-live --json
```

Pass when all of these hold:

- The seat's pane shows the Claude Code prompt. It never shows "Choose the text style that looks best with your terminal" or "Is this a project you created or one you trust?".
- The first `jq` prints `true` and `true`.
- The second prints `"dark"`, `true`, the launch's mode and `[]`.
- The hook reports have at least one line with `"ok":true` and a `sha256:` digest equal to the launch record's `claude_config_sha256`.
- `status` shows no quarantine for `t6probe`. In particular there is no `claude_hooks_silent` and no `claude_config_changed`. A `claude_config_changed` here answers open items 1 or 2 above.

Fail when any screen appears, when seat-exec refuses (record the reason code), or when a quarantine appears. Record the screen text or the reason code with the Claude Code version, then stop.

Clean up after the row:

```sh
git -C /Users/omri/code/amq-squad worktree remove /Users/omri/scratch-t6-live
git -C /Users/omri/code/amq-squad branch -D seat/t6-live
rm /Users/omri/scratch-t6-role.md /Users/omri/scratch-t6-seats.json
```

## Judgment calls

- The theme goes in `settings.json`, not `.claude.json`. In 2.1.314 the settings are the theme's first source, and the global config value is a legacy fallback that is ignored when it equals the default.
- Only the first-run values of `.claude.json` are in the digest, not its bytes. Claude Code rewrites that file on every start, so hashing its bytes would quarantine every seat at its first context call.
- The trust is keyed by the worktree path, not by the canonical repository root that Claude Code's own dialog writes. The root key would trust every worktree of the repository. The worktree key is the narrowest one the trust walk accepts for a seat whose cwd is its worktree.
- Config dirs written before t6 now digest differently, because the first-run entry is always present. No such Claude seat ever reached its first turn (the defect), so no running seat is affected.
- The unused helper `managed()` in claudeseat was left as it was. It is outside this change.
- Check is an allowlist, not a list of permission inputs. It runs only on the dir as the launch wrote it, before Claude Code has run in it, so nothing legitimate is there to refuse. A permission input the strings did not reveal is refused anyway.

## Follow-ups

- 3.0.x: AMQ's wake dialog hook entries must be written into the seat config by claudeseat. Claude seats run under their own config dir, so hooks registered in the owner's config never reach them (t9's finding). Not implemented here.
