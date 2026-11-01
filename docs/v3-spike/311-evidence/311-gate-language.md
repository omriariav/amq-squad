# 3.1.1-1: the gate question in plain words, answered with a bare yes or no

Brief: docs/v3-spike/briefs/brief-311-gate-language.md (copied verbatim in
this PR). Authority: the owner, 2026-11-01, after answering the rc.1 gates
on the phone ("simple language, not these hash and unclear language"; "Why
adding a code? It's Buzz and it's authenticated and safe."), with the plan
reviewer's shape: no code of any kind, the thread reply plus the signature
is the binding. Branch v3-311-gate, from origin/v3 `6191a997` (#835: the
two-way phone and the squad verbs merged), with origin/v3 merged in once
when it moved to `ca35fd3b` (#836).

**This is 3.1.1's first item.** It lands only after the v3.1.0 tag exists:
R54 cuts 3.1.0 with the installed binary, so the 3.1.0 gates read the old
way whatever merges.

## 1. What the tip had, before any code

- **The question text** was one renderer, `gatestore.TextFor`: the gate id,
  the squad, `class: release (tag)`, the target line (`git tag v3.1.0-rc.1
  4852f761... on omriariav/amq-squad`), the full digest, then "Reply in this
  thread with one line:" and the two typed tuples to copy. The record holds
  that text, and `Record.Validate` requires it to be the rendering of the
  record's own fields. Every raiser (the release flow, the merge train's
  escalator, the widen's growth gate, the guard's `gate raise`) goes through
  it; none of them had its own text.
- **The answer** was the tuple only (`gatestore.ParseAnswer`): `approve|deny
  <gate id> <class> <16+ hex>`. The core took the gate id from the tuple;
  `Verify` then checked the thread (every e tag names this gate's question
  or re-ask) and the signature under the pinned owner key. So the thread and
  the signature already bound the answer; the tuple was a second binding.
- **The re-ask** (`ReaskText`) and the history re-ask asked for the tuple
  again, "with one line".
- **The screen's gates block** (pi/extensions/lib/screen.ts) showed `id
  action prefix age waiting on Buzz` from `status --json`, whose gate rows
  carried no text; **the answer key** (keys.ts) showed `id class (typed)
  target prefix <16 hex>` from `gate status`.
- **The team confirmation over Buzz** (brief 31-3) was `yes <16 hex>` in a
  DM; brief 31-4's relay posts the lead's replies to the owner's DM as the
  share body. 31-3's note named the 3.1.1 seam: the handler's `teamYes` and
  `confirmByPrefix`.
- **No relaunch gate exists.** A relaunch is a screen key and a scheduler
  request (internal/schedproc, `RelaunchRequestKind`); it raises no owner
  gate, so there is no relaunch question to compose (section 8).
- **The PR's title was read nowhere.** The forge's `gh pr view` asked for
  number, state, draft, base, heads, mergeable and merge commit only.

## 2. The change

1. **Three plain lines above a rule** (internal/gatestore/plain.go).
   `TextOf` is now the one renderer: what is asked (a question), the quoted
   title when the raiser read one, why now, and what yes and no do; a blank
   line; the rule `----------`; the identifiers for the record, unchanged
   for the verifier (the gate id, the squad, the class and action, the
   target line, the full digest, whose first 16 hex are the typed form's
   prefix). Each target shape of internal/gatetarget's table has its
   sentence frame, the class in words from one fixed list (`ClassWords`: tag,
   GitHub release, package release, message outside the squad, credential,
   growth past the grant, widen, merge). No word above the rule is a code
   token (a double underscore, a path separator, a dot inside other than a
   version or an email address): an external send names its service from
   the tool's family and the destination as the tool names it; a credential
   change says what changes; a repository is named without its owner. A field
   value shown above the rule has any hex run of 8 or more cut to 7 and an
   ellipsis; a title is quoted on its own line and cut at 80 characters with
   an ellipsis.
2. **The second line states only what a record says.** `Record.Facts` is a
   new additive field, the values a raiser read from its own records: the
   release prepare's branch (prepare raises its two gates only after
   `Flow.CheckCommit` found the commit on the branch: "The prepare found its
   commit on v3."), the merge train's PR title (now read with `gh pr view
   --json ...,title,...`) and base, the widen's task title. The train's
   refused rules are in the target already: "The train refused it: its
   checks are not green and it changes files outside its task." A widen's
   refusal is its target's limit: "The grant refused its widen onto 2
   protected paths." With no recorded fact (a seat's gated command, the
   squad's growth by seats) the line names the raiser: "Asked by lead-a."
   The record's text stays the rendering of its own fields (facts included),
   so a record whose facts were edited no longer validates. `SafeFacts`
   leaves out a value that would not validate, so what a raiser read never
   stops its gate. A record raised by 3.1.0 (the old text, no facts) still
   validates and verifies.
3. **The bare answer** (`gatestore.ParseBare`, gateverify). `yes`, `no`,
   `approve`, `deny` alone, case and trailing punctuation ignored. `Verify`
   takes it as the record's own answer (`Record.BareAnswer`) and then checks
   the thread exactly as for a tuple: every thread reference must name this
   gate's question or re-ask. The core finds the gate a bare answer is for by
   its thread (`Core.bareGate`): the one gate whose question or re-ask the
   references name. A bare answer with no reference goes to the one open
   gate, where `Verify` refuses it as not in the thread and the core re-asks
   there; with no open gate, or several, it names none and is refused
   `not_in_gate_thread` with no re-ask. A reference to two gates, or to
   something that is no gate's, is `wrong_gate_thread`. The tuple form keeps
   verifying; no row of it changed.
4. **The plain re-ask.** "Reply yes or no to this message." above the rule,
   the gate id below. The history re-ask shows the question as raised and
   the same line; neither carries a tuple.
5. **The Buzz handler** sends a bare yes or no that continues no request to
   the gate path (`isGateAnswer`), never to a lead as a phone request, and
   to the squad whose question its thread names (`bareCore`; outside every
   thread, the one squad with an open gate; with two, `ambiguous_gate`).
6. **The screen and the key.** `status --json` gains `gates[].question`,
   additive: the question's plain lines (`gatestore.Plain`, the text above
   the rule; `""` for a 3.1.0 gate). The gates block shows them verbatim,
   then the identifiers line indented below. The answer key shows the
   record's text above the rule verbatim (from `gate status`'s `text`), then
   the identifiers, and says to reply yes or no in the question's Buzz
   thread.
7. **The team confirmation, a bare yes replied to the lead's plan post**
   (as revised after Codex's two rounds on #838). **The lead process is the
   authority for which post is its plan.** `amq_team_propose` takes
   `reply_to` (the phone request's AMQ message id). Before it sends
   anything, the lead's pi records the exact plan text with the lead over
   the start socket (`amq-squad.plan-sent.v1`, accepted only from the pi
   process the lead spawned, its pid and start stamp, as the TUI door); the
   lead keeps it in memory, by digest, for a recent proposal only. Then pi
   sends the text as the reply labelled `team-plan:<content digest>`. The
   relay records `plan_for` from that label (status.RelayPost, additive),
   for routing only. A bare yes goes to the team door only when its direct
   reply target (the NIP-10 parent) is a post the relay records as a plan
   and no event it names, whatever the tag's marker, is a gate question or
   re-ask. The attachment hands the lead the owner's yes and the raw post;
   no digest travels with them. The lead verifies the yes as before (the
   owner's pinned key, the id recomputed, kind, channel, window,
   once-only), the post (its id recomputed, the share body's pinned key,
   kind, channel: the model cannot sign as the body), the yes's direct
   reply target equals the post, and **the post's content carries every
   line of a plan text its pi recorded, redacted and trimmed as the relay
   posts it** (line containment: the relay adds its `re <request id>`
   header; a plan split over several posts proves nothing) for exactly one
   recent proposal; anything else is `plan_post_unproved`. It reads every
   e, E and q tag (`gateverify.EventRefs`) and refuses
   `conflicting_thread` when one names a gate question or re-ask in its
   root (either form) or, for a bare yes, anything beyond the plan post and
   the post's own thread. A lead restart forgets the recorded texts (and
   the confirmations), so the owner proposes again. The solo lead's shell
   and the guard's second layer refuse a `team-plan:` label in any shell
   command. The doors text and the plan block say "reply yes to the lead's
   Buzz post of this plan"; the prefix form keeps verifying, and no text
   asks for it.
8. **Redaction and the title** (Codex's round). The whole question, its
   plain lines and the stored title pass `redact.Block` at composition, so a
   credential-shaped title reaches neither the question nor the record
   `gate status` prints; status's `redactDocument` covers
   `gates[].question` for a record it reads without validating. A hex run
   of 8 or more is cut in either case, and each code token in a title (a
   double underscore, a path separator, a dot inside) becomes an ellipsis.

### 2.1 Choices where the brief left one (the smallest diff)

- The rule is ten hyphens: shorter than the relay's 16-character question
  line match (`buzz.MinQuestionLine`), so a lead reply with a markdown rule
  is never refused for it, and set off by a blank line, so a markdown
  renderer never reads the third line as a heading.
- Facts are a record field, not text the raiser passes, because the record's
  text must stay the rendering of the record's own fields (`Validate`).
- A bare answer outside every thread is re-asked only when exactly one gate
  is open; the brief did not say which gate a threadless yes is for.
- The answer key's gate picker keeps its labels (id, action, prefix); the
  line the key shows after the pick carries the question.
- The merge and release yes/no lines say what the approval lets happen: "Yes
  lets the train merge it." (the approval lifts the refusal; the train
  merges on its run), "Yes creates the tag." (the brief's words).
- The status field is additive and always present (`""` for a 3.1.0 gate),
  so the golden fixtures carry it.

## 3. The text of each gate class, before and after

Composed by the code for the sample targets of gatestore's row (the squad and digest lines are each sample's own). Every target shape of internal/gatetarget's table is in the row; one per class and raiser here.

### tag (the release flow)

Before (3.1.0):

```text
amq-squad gate rel-tag-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (tag)
target: git tag v3.1.0-rc.1 4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d on omriariav/amq-squad
digest: sha256:f6a51dd34f46edebc69c9b727bbb850189b63dfd326ffd4dbab5d62d80d71fe5
Reply in this thread with one line:
approve rel-tag-0 release f6a51dd34f46edeb
deny rel-tag-0 release f6a51dd34f46edeb
```

After (3.1.1):

```text
Create a tag for v3.1.0-rc.1 in amq-squad?
The prepare found its commit on v3.
Yes creates the tag. No does nothing.

----------
amq-squad gate rel-tag-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (tag)
target: git tag v3.1.0-rc.1 4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d on omriariav/amq-squad
digest: sha256:f6a51dd34f46edebc69c9b727bbb850189b63dfd326ffd4dbab5d62d80d71fe5
```

### GitHub release (the release flow)

Before (3.1.0):

```text
amq-squad gate rel-github-release-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (github_release)
target: gh release create v3.1.0-rc.1 on omriariav/amq-squad
digest: sha256:0ab60a1e35035211dbb92fdc81fb45ac6ed11bd96fe1553232ca9014be570698
Reply in this thread with one line:
approve rel-github-release-0 release 0ab60a1e35035211
deny rel-github-release-0 release 0ab60a1e35035211
```

After (3.1.1):

```text
Publish a GitHub release for v3.1.0-rc.1 in amq-squad?
The prepare found its commit on v3.
Yes creates the release. No does nothing.

----------
amq-squad gate rel-github-release-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (github_release)
target: gh release create v3.1.0-rc.1 on omriariav/amq-squad
digest: sha256:0ab60a1e35035211dbb92fdc81fb45ac6ed11bd96fe1553232ca9014be570698
```

### tag (a seat's git tag)

Before (3.1.0):

```text
amq-squad gate rel-tag-2
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (tag)
target: git tag v1.2.0
digest: sha256:c71e53c9e17862bc992180544d0a0931b42bdc37d03e0154ce925468d02a5af3
Reply in this thread with one line:
approve rel-tag-2 release c71e53c9e17862bc
deny rel-tag-2 release c71e53c9e17862bc
```

After (3.1.1):

```text
Create a tag named v1.2.0?
Asked by lead-a.
Yes lets it run. No stops it.

----------
amq-squad gate rel-tag-2
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (tag)
target: git tag v1.2.0
digest: sha256:c71e53c9e17862bc992180544d0a0931b42bdc37d03e0154ce925468d02a5af3
```

### package release

Before (3.1.0):

```text
amq-squad gate rel-package-publish-1
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (package_publish)
target: npm publish @scope/pkg
digest: sha256:72435ede734eb86d2fbbf627401890ed066ed3d167558777b62b5ca448905124
Reply in this thread with one line:
approve rel-package-publish-1 release 72435ede734eb86d
deny rel-package-publish-1 release 72435ede734eb86d
```

After (3.1.1):

```text
Publish a package release with npm?
Asked by lead-a.
Yes lets it run. No stops it.

----------
amq-squad gate rel-package-publish-1
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: release (package_publish)
target: npm publish @scope/pkg
digest: sha256:72435ede734eb86d2fbbf627401890ed066ed3d167558777b62b5ca448905124
```

### external send

Before (3.1.0):

```text
amq-squad gate rel-external-send-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: send (external_send)
target: mcp__slack__send_message C1
digest: sha256:51270c5c6a6a4fcbdfae84e0a6e870495d1e79dd43c868027ef3525b9b89caf0
Reply in this thread with one line:
approve rel-external-send-0 send 51270c5c6a6a4fcb
deny rel-external-send-0 send 51270c5c6a6a4fcb
```

After (3.1.1):

```text
Send a message outside the squad, on Slack, to channel C1?
Asked by lead-a.
Yes lets it run. No stops it.

----------
amq-squad gate rel-external-send-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: send (external_send)
target: mcp__slack__send_message C1
digest: sha256:51270c5c6a6a4fcbdfae84e0a6e870495d1e79dd43c868027ef3525b9b89caf0
```

### credential change

Before (3.1.0):

```text
amq-squad gate rel-credential-change-1
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: credential (credential_change)
target: git config credential.helper
digest: sha256:b7176979a6478f79ed9cf435fb75a92eebaf0ca3592d62d4cedb8618ece91446
Reply in this thread with one line:
approve rel-credential-change-1 credential b7176979a6478f79
deny rel-credential-change-1 credential b7176979a6478f79
```

After (3.1.1):

```text
Change which program git uses for credentials?
Asked by lead-a.
Yes makes the credential change. No stops it.

----------
amq-squad gate rel-credential-change-1
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: credential (credential_change)
target: git config credential.helper
digest: sha256:b7176979a6478f79ed9cf435fb75a92eebaf0ca3592d62d4cedb8618ece91446
```

### growth beyond the grant (seats)

Before (3.1.0):

```text
amq-squad gate rel-spawn-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow /r/s1 seats=13
digest: sha256:fb08c49d1ac220a4c75ab9de6802347dee6e26ccb301ee8f9916edb4dff42fec
Reply in this thread with one line:
approve rel-spawn-0 grow fb08c49d1ac220a4
deny rel-spawn-0 grow fb08c49d1ac220a4
```

After (3.1.1):

```text
Allow growth past the grant to 13 seats?
Asked by lead-a.
Yes lets the squad grow. No keeps the grant.

----------
amq-squad gate rel-spawn-0
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow /r/s1 seats=13
digest: sha256:fb08c49d1ac220a4c75ab9de6802347dee6e26ccb301ee8f9916edb4dff42fec
```

### widen's growth gate (protected paths)

Before (3.1.0):

```text
amq-squad gate rel-spawn-2
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow /r/s1 task=t3 protected_paths=["docs/**","internal/x/**"]
digest: sha256:f4c6e08997f261386b97f3002ac460431b6b4fb089d3974dbd9f4441fdd84661
Reply in this thread with one line:
approve rel-spawn-2 grow f4c6e08997f26138
deny rel-spawn-2 grow f4c6e08997f26138
```

After (3.1.1):

```text
Let task t3 change protected files?
"Ship the plain gate questions"
The grant refused its widen onto 2 protected paths.
Yes widens the task. No leaves it as it is.

----------
amq-squad gate rel-spawn-2
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow /r/s1 task=t3 protected_paths=["docs/**","internal/x/**"]
digest: sha256:f4c6e08997f261386b97f3002ac460431b6b4fb089d3974dbd9f4441fdd84661
```

### widen's growth gate (size cap)

Before (3.1.0):

```text
amq-squad gate rel-spawn-4
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow /r/s1 task=t4 size_cap={"lines":900,"files":30}
digest: sha256:c7a5a83b0176851ec47439fd028df0db176f327d95b036dc14e67519900dc484
Reply in this thread with one line:
approve rel-spawn-4 grow c7a5a83b0176851e
deny rel-spawn-4 grow c7a5a83b0176851e
```

After (3.1.1):

```text
Let task t4 grow past the size cap?
"Refactor the relay"
The grant refused 900 lines in 30 files.
Yes widens the task. No leaves it as it is.

----------
amq-squad gate rel-spawn-4
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow /r/s1 task=t4 size_cap={"lines":900,"files":30}
digest: sha256:c7a5a83b0176851ec47439fd028df0db176f327d95b036dc14e67519900dc484
```

### merge outside the grant

Before (3.1.0):

```text
amq-squad gate rel-spawn-5
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow merge omriariav/amq-squad#834 head=4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d rules=ci_green,task_scope
digest: sha256:8fa1bec46f1304e5aa33eaf72008d00463ab012f1b3dcc9a1014440c34e61545
Reply in this thread with one line:
approve rel-spawn-5 grow 8fa1bec46f1304e5
deny rel-spawn-5 grow 8fa1bec46f1304e5
```

After (3.1.1):

```text
Allow a merge of pull request #834 into v3?
"gate: the question in plain words, answered with a bare yes or no replied in its…"
The train refused it: its checks are not green and it changes files outside its task.
Yes lets the train merge it. No leaves it open.

----------
amq-squad gate rel-spawn-5
squad: root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8
class: grow (spawn)
target: grow merge omriariav/amq-squad#834 head=4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d rules=ci_green,task_scope
digest: sha256:8fa1bec46f1304e5aa33eaf72008d00463ab012f1b3dcc9a1014440c34e61545
```

### 3.1 The plan reviewer's round on draft PR #838

Three texts of the first round put a token where the plain words go. Above
the rule now (the tool, the setting, the path and the globs are in the
target line below it):

| Class | First round | Now |
| --- | --- | --- |
| external send | Send an outside message with mcp__slack__send_message to C1? | Send a message outside the squad, on Slack, to channel C1? |
| credential change | Make a credential change to git setting credential.helper? | Change which program git uses for credentials? |
| widen's growth gate | Allow a task widen of t3 onto protected paths? (second line: The grant refused the widen at docs/** and 1 more.) | Let task t3 change protected files? (second line: The grant refused its widen onto 2 protected paths.) |

The other forms of the same classes:

- external_send, `mcp__slack__send_message U024BE7LH`: "Send a message outside the squad, on Slack, to user U024BE7LH?\nAsked by lead-a.\nYes lets it run. No stops it."
- external_send, `mcp__gmail__send_email owner@example.com`: "Send a message outside the squad, on Gmail, to owner@example.com?\nAsked by lead-a.\nYes lets it run. No stops it."
- external_send, `gws gmail send`: "Send a message outside the squad, on Google Workspace, as a gmail send?\nAsked by lead-a.\nYes lets it run. No stops it."
- external_send, `mail owner@example.com`: "Send a message outside the squad, by mail, to owner@example.com?\nAsked by lead-a.\nYes lets it run. No stops it."
- credential_change, `gh auth login`: "Log gh in?\nAsked by lead-a.\nYes makes the credential change. No stops it."
- credential_change, `write /home/u/.ssh/config`: "Write a credentials file for SSH?\nAsked by lead-a.\nYes makes the credential change. No stops it."
- credential_change, `write /home/u/.netrc`: "Write a credentials file?\nAsked by lead-a.\nYes makes the credential change. No stops it."
- credential_change, `git config user.signingkey`: "Change the key git signs commits with?\nAsked by lead-a.\nYes makes the credential change. No stops it."
- spawn, `grow /r/s1 task=t5 protected_paths=[".claude/settings.json"]`: "Let task t5 change protected files?\n\"tune settings\"\nThe grant refused its widen onto 1 protected path.\nYes widens the task. No leaves it as it is."

The row's new token check (no word with a double underscore, a path
separator or a dot inside, other than a version, an email address or a
sentence end) also moved four first-round texts the reviewer did not name:
a repository is named without its owner ("in amq-squad", not "in
omriariav/amq-squad"); a package release names its tool only (a scoped
spec or a file path stays below); a credential command shows its words up
to the first token ("Make a credential change with security
add-generic-password?"); the generic fallbacks do the same.

## 4. Rows

| Brief row | Test |
| --- | --- |
| 1. Each class's question composes as three plain lines above a rule and the identifiers below; the plain-language check passes on all of them and fails on the old text | gatestore `TestEveryClassTheCatalogRaisesAsksInPlainWords` (the classes read from `operatorauth.ActionCapabilities()`: every human-only action the gate store raises needs samples, one per target shape; a human-only action it does not raise, destructive_filesystem, must stay unrenderable; the check: no code token above the rule (a word with a double underscore, a path separator, or a dot inside other than a version, an email address or a sentence end; the check is the row's own), three plain lines, a question mark ending the first, `Yes` and `No` starting the third's sentences, no sentence over 20 words, the class in words from `ClassWords`, no hex run of 8 or more, no gate id and no identifier above the rule, the gate id, target and digest below; `legacyText` fails it), `TestTheRowFailsTheReviewersThreeTexts` (the review's three first-round texts fail; a version, an email address and a sentence end pass), `TestRenderAndValidate`; at the raisers: mergetrain `TestTheMergeGateAsksInPlainWordsFromWhatTheTrainRead` (in process and through the gh subprocess), cli `TestReleaseVerbPassesTheFileAsTheBodyAndNotesAsTheGateNote` (prepare's two questions), `TestSeatCommitAndWidenThroughTheGrowthGate` (the widen's) |
| 2. A bare yes replied in the thread, signed by the pinned key, is accepted for its gate; a bare no refuses; `Yes.` and `yes` are the same; a bare yes outside the thread gets the plain re-ask; a bare yes in another gate's thread is refused for this gate | gateverify `TestABareYesOrNoInTheThreadIsTheAnswer` (yes, Yes., YES!, approve; no, No., deny; the answer file verifies with `VerifyAnswerFile`; a seat's bare yes `wrong_pubkey`; "yes please" `bad_tuple`), `TestABareYesOutsideTheThreadGetsThePlainReask` (the re-ask's text, in the thread; the yes replied to it is the answer; two open gates: refused, no re-ask), `TestABareYesInAnotherGatesThreadIsThatGatesNeverThisOnes` (`Verify` refuses B's thread for A `wrong_gate_thread`; the core decides B, A stays open; a yes naming both threads binds neither); gatestore `TestParseBare`; buzz `TestABareYesIsAGateAnswerAtTheHandler`, `TestABareYesGoesToTheSquadWhoseThreadItNames` |
| 3. The tuple form still verifies (unchanged rows), and no composed text contains "with one line" or a tuple | gatestore `TestNoQuestionAsksForATuple`; gateverify `TestNoReaskAsksForATuple` (the re-ask and the history re-ask; the tuple in the thread still approves) and every tuple row of core_test.go unchanged; cli `TestTheDoorsTextAsksForNoPrefix`; pi "brief 3.1.1-1: the plan block asks for a bare yes to the plan's post, never yes <prefix>" |
| 4. The screen's gates block and the answer key show the question's text above the rule verbatim | pi "brief 3.1.1-1: the gates block and the answer key show the question above the rule verbatim"; cli `TestSeatCommitAndWidenThroughTheGrowthGate` (status's `gates[].question` is `gatestore.Plain` of the record's text); status `TestGoldenRoundTripAdditiveBothWays`, `TestFleetGoldenIsTheSquadGoldenWrapped`, `TestThePiScreensFixtureIsAStatusDocument` (the field in the goldens and the screen's fixture) |
| Codex P2 and P1: no hash in either case and no token in a title; the question redacted | gatestore `TestATitleCarriesNoHashOrTokenAboveTheRule` ("Fix DEADBEEF", a title with a path, one with a double underscore), `TestACanaryInATitleIsRedactedAtComposition`; cli `TestACanaryInATitleReachesNoQuestionStatusOrKey` (absent from the question, `gate status` and status, a hand-written record redacted by status) |
| 5. Every word of the three lines traces to a field the composer read; a quoted title is exempt from the sentence count and cut at 80 characters with an ellipsis | gatestore `TestEveryWordOfThePlainLinesTracesToAField` (every word is the composer's fixed glue, which holds no claim, a word of a field it read (the target and the values in it, the raiser, the facts, the refused rules in words) or a count; the second line names a recorded fact; "Its checks passed on the train." and "The reviewer accepted it." fail), `TestAQuotedTitleIsCutAt80WithAnEllipsis`, `TestTheOldTextStillValidatesAndFactsAreSafe` |
| 6. The team confirmation (Codex's re-check: the lead proves its plan from the text its pi recorded; every reference read) | cli `TestTheLeadWitnessesABareYesRepliedToThePlanPost` (a plan recorded by a child of pi `peer_not_lead_pi`, for no proposal `no_proposal`; a yes before pi recorded the plan, a shell-labelled post with the live digest, a real yes to an ordinary post as a local socket caller, a post with part of the plan: `plan_post_unproved`; a gate question marked mention or quoted with a q tag beside the plan, another event marked mention, a yes <prefix> mentioning a gate question: `conflicting_thread`; the lead's own plan post with the owner's yes as a direct reply witnessed), `TestTheAttachmentRoutesABareYesByTheRecordedPlan` (an ordinary post routed by a stray label `plan_post_unproved`); buzz `TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam` (a yes to the plan mentioning a gate question is no team confirmation); pi "amq_team_propose with reply_to records the plan with the lead, then sends it labelled" (the text sent is the text recorded; a lead that refuses means nothing is sent), "the solo shell refuses a team-plan label in an argv form", and three command-string forms in solo-shell-forms.json (both layers); gateverify `TestABareYesVerifiesAgainstARedactedQuestion` (a bare yes to a question composed from a canary title approves, the answer file verifies, the target, its digest and the text's digest bound unchanged). Before the re-check: | buzz `TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam` (the relay records plan_for from the label only; a yes to a post whose text names the live digest continues the request and confirms nothing; a yes to the recorded plan post confirms with the post and its digest; outside the thread a gate answer; rooted at the plan but replying elsewhere, nothing; a body post the relay never posted, nothing; a bare no, nothing; a malformed plan_for fails the record), `TestTheRelayReadsThePlanFromItsLabelOnly`; cli `TestTheLeadWitnessesABareYesRepliedToThePlanPost` (refs naming another event, a gate question in the lead's root, marked or unmarked: `conflicting_thread`; a yes <prefix> in a gate question's thread: `conflicting_thread`; replying to nothing `not_in_gate_thread`; a post another key signed, an altered post, a plan for no recent proposal, no plan digest, a bare no, a yes the attachment signed; the owner's `Yes.` rooted at the post's own thread witnessed; a replay refused), `TestTheAttachmentRoutesABareYesByTheRecordedPlan`; pi "brief 3.1.1-1: amq_team_propose with reply_to sends the plan as the labelled reply"; the prefix form's rows unchanged. First round, superseded: | buzz `TestABareYesRepliedToTheRelayedPlanConfirmsTheTeam` (the confirmer gets the yes and the post's bytes; outside the thread it is a gate answer; to a relay post with no digest, to one naming no proposal, it continues the request; to a body-signed post the relay never posted, nothing; a bare no, nothing), `TestPlanPrefixes`; cli `TestTheLeadWitnessesABareYesRepliedToThePlanPost` (the yes replying elsewhere or to nothing `not_in_gate_thread`, a post another key signed `wrong_pubkey`, an altered post `id_mismatch`, a post naming no proposal `no_proposal`, a bare no, a yes the attachment signed; the owner's `Yes.` witnessed with its event id; a replay refused), `TestTheAttachmentRoutesABareYesByThePostsProposal` (a post naming no proposal `not_a_plan`, no lead call; the plan's post reaches the serving lead, witnessed); the prefix form: `TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes`, `TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified`, `TestTheOwnersSignedYesOverBuzzIsWitnessedByTheLead`, `TestATeamYesIsAConfirmationNotAPhoneRequest`, unchanged but for the confirmer's added parameter |

Tests that pinned the old text were moved to the new one, each on the same
point: gatestore `TestRenderAndValidate`, gateverify `TestThreadReask` and
`TestReview7ReaskPublishedOnceAcrossRetries` (the re-ask), buzz
`TestHandlerGatesAndRouting`, `TestOwnerScriptDryRun` and the rehearsal (the
question's third line in place of "Reply in this thread"), cli
`TestTheStartAcceptsOnlyAWitnessedFreshConfirmation` and
`TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot` (the doors text),
pi's screen row (the identifiers line now sits under the question).

## 5. Revert runs

Each run reverts one change, runs the rows that must catch it, and restores every touched file (docs/v3-spike/311-evidence/311-revert-runs.py, try/finally, refuses a dirty tree). Every revert compiles, so each failure is its row's.

```text
REVERT the 3.0 text (Render writes the identifiers and the typed lines): fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
   --- FAIL: TestNoQuestionAsksForATuple (0.00s)
   --- FAIL: TestRenderAndValidate (0.00s)
REVERT a hash above the rule (a field's hex run shown whole): fails (the row catches it)
   --- FAIL: TestAQuotedTitleIsCutAt80WithAnEllipsis (0.00s)
REVERT the title not cut at 80 characters: fails (the row catches it)
   --- FAIL: TestAQuotedTitleIsCutAt80WithAnEllipsis (0.00s)
REVERT an unrecorded claim (the merge's second line says its checks passed): fails (the row catches it)
   --- FAIL: TestEveryWordOfThePlainLinesTracesToAField (0.01s)
REVERT a sentence over 20 words (every refused rule listed): fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT the gate id above the rule: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT no class in words (a tag named without saying tag): fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT the identifiers below the rule lose the target line: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT the facts dropped at Render: fails (the row catches it)
   --- FAIL: TestAQuotedTitleIsCutAt80WithAnEllipsis (0.00s)
REVERT a 3.1.0 record no longer validates (only the new text): fails (the row catches it)
   --- FAIL: TestTheOldTextStillValidatesAndFactsAreSafe (0.00s)
REVERT SafeFacts keeps a branch that does not validate: fails (the row catches it)
   --- FAIL: TestTheOldTextStillValidatesAndFactsAreSafe (0.00s)
REVERT the external send names its tool and channel id above the rule: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT the credential change names its git setting above the rule: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT a credentials write names its path above the rule: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT the widen names its protected globs above the rule: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT a repository named owner/name above the rule: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT a generic question names the whole target: fails (the row catches it)
   --- FAIL: TestEveryClassTheCatalogRaisesAsksInPlainWords (0.01s)
REVERT the sanitizer reads lowercase hex only: fails (the row catches it)
   --- FAIL: TestATitleCarriesNoHashOrTokenAboveTheRule (0.00s)
REVERT a token in a title reaches the quoted line: fails (the row catches it)
   --- FAIL: TestATitleCarriesNoHashOrTokenAboveTheRule (0.00s)
REVERT the question, its lines and its title not redacted at composition: fails (the row catches it)
   --- FAIL: TestACanaryInATitleIsRedactedAtComposition (0.00s)
REVERT the stored title not redacted, seen by the verifier's row: fails (the row catches it)
   --- FAIL: TestABareYesVerifiesAgainstARedactedQuestion (0.06s)
REVERT the stored title not redacted (the question is): fails (the row catches it)
   --- FAIL: TestACanaryInATitleReachesNoQuestionStatusOrKey (0.05s)
REVERT status does not redact gates[].question: fails (the row catches it)
   --- FAIL: TestACanaryInATitleReachesNoQuestionStatusOrKey (0.05s)
REVERT Verify refuses the bare form: fails (the row catches it)
   --- FAIL: TestABareYesOrNoInTheThreadIsTheAnswer (0.06s)
REVERT the core binds a bare answer to no gate: fails (the row catches it)
   --- FAIL: TestABareYesOrNoInTheThreadIsTheAnswer (0.06s)
REVERT a bare yes outside the thread is not re-asked in the one open gate: fails (the row catches it)
   --- FAIL: TestABareYesOutsideTheThreadGetsThePlainReask (0.06s)
REVERT a bare answer bound to every gate (not the thread it names): fails (the row catches it)
   --- FAIL: TestABareYesInAnotherGatesThreadIsThatGatesNeverThisOnes (0.08s)
REVERT the re-ask asks for the tuple (the 3.0 text): fails (the row catches it)
   --- FAIL: TestABareYesOutsideTheThreadGetsThePlainReask (0.08s)
   --- FAIL: TestNoReaskAsksForATuple (0.12s)
   --- FAIL: TestThreadReask (0.09s)
REVERT the history re-ask asks for the tuple: fails (the row catches it)
   --- FAIL: TestNoReaskAsksForATuple (0.12s)
REVERT the doors text asks for yes <prefix>: fails (the row catches it)
   --- FAIL: TestTheDoorsTextAsksForNoPrefix (0.00s)
REVERT the plan block asks for yes <prefix>: fails (the row catches it)
   not ok 1 - brief 3.1.1-1: the plan block asks for a bare yes to the plan's post, never yes <prefix>
REVERT a bare yes routed to the lead as a phone request: fails (the row catches it)
   --- FAIL: TestABareYesIsAGateAnswerAtTheHandler (0.07s)
REVERT a bare yes goes to the primary squad whatever its thread: fails (the row catches it)
   --- FAIL: TestABareYesGoesToTheSquadWhoseThreadItNames (0.13s)
REVERT the handler's team door ignores a bare yes to the plan: fails (the row catches it)
   --- FAIL: TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam (0.11s)
REVERT any relay post taken as the plan post (its text decides): fails (the row catches it)
   --- FAIL: TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam (0.08s)
REVERT the relay ignores the lead's team-plan label: fails (the row catches it)
   --- FAIL: TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam (0.08s)
   --- FAIL: TestTheRelayReadsThePlanFromItsLabelOnly (0.00s)
REVERT the relay record drops plan_for when the post is published: fails (the row catches it)
   --- FAIL: TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam (0.08s)
REVERT the relay record takes a malformed plan_for: fails (the row catches it)
   --- FAIL: TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam (0.09s)
REVERT the lead takes a plan post another key signed: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.11s)
REVERT the lead takes a yes whose direct reply target is not the plan post: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.11s)
REVERT the lead takes an altered plan post: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.10s)
REVERT the lead takes a bare no as the confirmation: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.11s)
REVERT the lead takes a post without its recorded plan text (Codex re-check): fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.14s)
   --- FAIL: TestTheAttachmentRoutesABareYesByTheRecordedPlan (0.07s)
REVERT the lead records a plan text from any peer: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.04s)
REVERT pi sends the plan reply the lead did not record: fails (the row catches it)
   not ok 2 - brief 3.1.1-1: amq_team_propose with reply_to records the plan with the lead, then sends it labelled
REVERT the attachment routes a plan yes by prefix: fails (the row catches it)
   --- FAIL: TestTheAttachmentRoutesABareYesByTheRecordedPlan (0.05s)
REVERT the attachment hands the lead the yes without the post: fails (the row catches it)
   --- FAIL: TestTheAttachmentRoutesABareYesByTheRecordedPlan (0.05s)
REVERT the lead's process sends the plan reply without its label: fails (the row catches it)
   not ok 2 - brief 3.1.1-1: amq_team_propose with reply_to records the plan with the lead, then sends it labelled
REVERT the lead takes a confirmation in a gate question's thread: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.10s)
REVERT the lead takes a yes naming more than the plan's thread: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.12s)
REVERT the solo shell runs a command string with a team-plan label: fails (the row catches it)
   not ok 1 - the solo lead's shell sees through quoting, expansions and eval
REVERT the solo shell runs an argv form with a team-plan label: fails (the row catches it)
   not ok 3 - brief 3.1.1-1: the solo shell refuses a team-plan label in an argv form
REVERT the guard's second layer runs a team-plan label: fails (the row catches it)
   --- FAIL: TestTheGuardRefusesTheSoloLeadsQuotedTmuxAsASecondLayer (0.00s)
REVERT the lead reads only reply, root and unmarked references: fails (the row catches it)
   --- FAIL: TestTheLeadWitnessesABareYesRepliedToThePlanPost (0.16s)
REVERT the handler's first layer reads only reply, root and unmarked references: fails (the row catches it)
   --- FAIL: TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam (0.16s)
REVERT the release prepare raises without the branch it checked: fails (the row catches it)
   --- FAIL: TestReleaseVerbPassesTheFileAsTheBodyAndNotesAsTheGateNote (0.55s)
REVERT the merge gate without the PR's title and base: fails (the row catches it)
   --- FAIL: TestTheMergeGateAsksInPlainWordsFromWhatTheTrainRead (0.50s)
REVERT gh pr view does not ask for the title: fails (the row catches it)
   --- FAIL: TestTheMergeGateAsksInPlainWordsFromWhatTheTrainRead (0.50s)
REVERT the widen's growth gate without the task's title: fails (the row catches it)
   --- FAIL: TestSeatCommitAndWidenThroughTheGrowthGate (0.46s)
REVERT status gates without the question: fails (the row catches it)
   --- FAIL: TestSeatCommitAndWidenThroughTheGrowthGate (0.47s)
REVERT status gates carry the whole text, not the lines above the rule: fails (the row catches it)
   --- FAIL: TestSeatCommitAndWidenThroughTheGrowthGate (0.46s)
REVERT the gates block shows the identifiers only: fails (the row catches it)
   not ok 1 - brief 3.1.1-1: the gates block and the answer key show the question above the rule verbatim
REVERT the answer key shows the identifiers only: fails (the row catches it)
   not ok 1 - brief 3.1.1-1: the gates block and the answer key show the question above the rule verbatim
REVERT the answer key finds no rule in the text: fails (the row catches it)
   not ok 1 - brief 3.1.1-1: the gates block and the answer key show the question above the rule verbatim

64 of 64 reverts fail their rows
```

## 6. The gates

Run on `f1f6a0d3` (after Codex's re-check on #838; the tree of this note's commit but for this section).

```text
$ go build ./... && go vet ./... && go test -count=1 ./...
go exit 0: 56 packages ok, none failing
$ cd pi && npm ci --offline --ignore-scripts && npm test
ℹ tests 70
ℹ pass 70
ℹ fail 0
$ PATH=<pyvenv>/bin:$PATH make ci
make ci exit 0
Ran 19 tests in 12.850s

OK
context injection: last run 16.8 ms
PERF pre-tool allow in a seat: p50 22.3 ms, p95 23.5 ms
PERF outside a seat: 60 runs, p50 6.0 ms, p95 6.5 ms, max 6.9 ms (budget p95 < 20 ms: met)
```

## 7. The owner-run row: a release gate read on the phone, answered with a bare yes

Not run here (no real Buzz, AMQ or tmux from this worktree). After the
v3.1.0 tag, with this branch merged and installed:

1. `make install` from the merged v3 and check `amq-squad version` names
   the 3.1.1 build. The Buzz attachment runs (`amq-squad remote attach`,
   the enrolled share) and the owner's DM with the body is open on the
   phone.
2. In the repository, raise the release gates with the installed binary:
   `amq-squad release prepare --repository omriariav/amq-squad --tag
   v3.1.1-rc.1 --commit <the v3 tip SHA> --branch v3 --notes-file
   <notes.md> --project . --session <session> --json`.
3. On the phone, read the two questions. Each shows three plain lines
   ("Create a tag for v3.1.1-rc.1 in amq-squad?" / "The prepare
   found its commit on v3." / "Yes creates the tag. No does nothing.";
   the GitHub release's likewise), a rule, and the identifiers. Record the
   text as the phone shows it; no hex run of 8 or more and no gate id above
   the rule.
4. In the lead's pane, `/squad`: the gates block shows the same three lines
   for each open gate, then its identifiers. Record the block.
5. Reply `yes` in the tag question's thread. The result row under the reply
   says `accepted rel-tag-... tag`.
6. Send a bare `yes` in the DM, not in a thread. The result row says
   `refused rel-gh-... not_in_gate_thread`, and the GitHub release
   question's thread gets "Reply yes or no to this message." Reply `Yes.`
   to that re-ask: `accepted rel-gh-... github_release`.
7. `amq-squad gate status --project . --session <session> --json`: both
   gates `accepted`. `amq-squad release publish ...` (same flags) runs the
   tag and the release under the owner's gh.
8. The team door, same phone: ask the solo lead for a team; the lead
   answers with `amq_team_propose` and `reply_to`, so the relayed post is
   recorded as the plan (`plan_for` in the request's relay record); reply
   `yes` to that post directly.
   The result row says `confirmed team`, and the lead's screen shows the
   plan confirmed at the Buzz door. A `yes` sent outside that thread
   confirms nothing.

## 8. Where the brief did not match the code

- **There is no relaunch gate.** The brief lists "a relaunch" among the
  gates to phrase, and the lead's message points at "the widen and relaunch
  question text in internal/cli and internal/schedproc". A relaunch is a
  screen key and a scheduler request; it raises no owner gate and composes
  no question. Nothing was built for it.
- **The merge gate's example claims what the record does not say.** A
  merge gate is raised only when the train refused the PR, so "Its checks
  passed on the train" is false whenever the refused rule is `ci_green`,
  and true otherwise only by inference. The second line names the refused
  rules in words, the record's own field. "Yes merges it" is also more than
  an approval does: it lifts the refusal and the train merges on its run,
  so the line says "Yes lets the train merge it."
- **"From the tip of v3" is not what the prepare checks.** `CheckCommit`
  proves the commit is on the branch, not that it is the tip; the line says
  "The prepare found its commit on v3." The example also joins the tag and
  the release in one question; they are two gates, so two questions.
- **The PR's title was recorded nowhere.** The train now asks `gh pr view`
  for it (internal/mergetrain/gh.go, forge.go, train.go), and the fake gh
  prints only the fields `--json` names, so the row catches its removal.
- **"The relay record maps the post to the proposal."** The relay record
  holds no message text and no proposal. The record proves the post is the
  relay's; the post's own signed bytes name the digest; the lead verifies
  those bytes under the share body's pinned key. The plan's digest has to
  be in the lead's relayed reply, which the doors text now says.
- **"The verifier binds by the thread exactly as today"** holds for
  `Verify`, but the core took the gate id from the tuple. A bare answer
  names none, so the core finds the gate by the thread (`bareGate`), the
  verification change the brief allows for accepting the bare form.
- **Files outside the declared list.** internal/status (schema.go,
  classify.go and the two golden fixtures): the screen is fed by `status
  --json` and nothing else, so the gates block can show the question only
  through an additive `gates[].question`. internal/cli/lead_start.go,
  team_confirm.go and team.go: the team door's seams that 31-3's note
  named. docs/v3-spike/briefs/: the brief copied, as the lead asked.
- **"Never merge."** The brief forbids it; the lead's message allows `git
  merge origin/v3` when the tip moves. It moved once (#836) and was merged.

- **The plan post is the lead's to prove.** After Codex's re-check the
  relay's `plan_for` only routes; the lead witnesses a post only when the
  share body signed it and its content carries a plan text the lead's own
  pi recorded before sending. A shell that copies the exact plan text
  under the label would post the real plan, which the owner reads before
  answering; the shell label is refused anyway.
- **Residuals of the token rule.** A branch or tag name with a slash
  (`release/3.1`) would show above the rule; the samples and this
  repository's branches have none. An email address is a destination as the
  tool names it and stays above the rule. A quoted PR or task title is the
  author's text and is exempt from the token check, as from the sentence
  count.

## 9. For the 3.1.1 release notes

Gate questions on the phone are now three plain lines (what is asked, why
now, what yes and no do) with the identifiers below a rule; answer with a
bare `yes` or `no` replied in the question's thread, and confirm a proposed
team by replying `yes` to the lead's post of the plan. The 3.0 typed line
still verifies; nothing asks for it.
