# Brief 31-4: the two-way phone, execution note

The brief is [docs/v3-spike/briefs/brief-31-4-two-way-phone.md](../briefs/brief-31-4-two-way-phone.md)
(copied verbatim). Authority: docs/v3.1-plan.md section 3.2, section 4 row 2,
section 5 row 3 under owner decision 1 A (2026-11-01: "I also want to chat
with the mayor in Buzz freely"; the chat partner is the lead), ruling 20
point 1, R33, R40.

Branch `v3-31-4` from origin/v3 at 4852f761 (PR #833). Built by a Claude
agent (Opus 5.5) in a scratch worktree; no real Buzz, AMQ, tmux or owner
state was touched, and no key material exists in any test.

## 1. What changed

- **The receipt.** A phone question now gets `accepted <request id> routed
  <dest>` at once (it was `routed <dest>`). The request id is `q-` and the
  first 12 hex of the owner's signed DM. The routing words after the id are
  today's, kept so `not_forwarded not_a_lead` and `ambiguous_lead` still
  reach the owner.
- **The cockpit thread.** The question goes to the lead on the AMQ thread
  `cockpit/<request id>` (the mailbox copy and the forward both), so each
  request has its own thread. Before, both went on AMQ's default p2p thread.
- **The relay** (internal/buzz/relay.go, `Handler.RelayStep`), called by
  `remote attach` on every tick right after the inbox step. It follows each
  open request's cockpit thread with `amq thread --include-body --json` (a
  read; nothing moves out of any inbox) and posts every new reply from the
  request's lead to the owner's DM, through a `Poster` interface. The
  production poster, `BodyPoster`, dials the verifier's per-use connection
  (ruling 20 point 1: the share body key and the kind 9 grant), signs one
  kind 9 in the DM channel and closes the connection. The body key is in the
  attachment's process only.
- **The conversation.** A DM that replies to a relay post continues that
  post's request: the same request id, the same cockpit thread, to the same
  lead (the record keeps the route the question took). It gets the same
  receipt. A DM that is not a reply opens a new request.
- **The record**, one per request, `<root>/extensions/amq-squad/relay/<request id>.json`
  under the mailbox's AMQ root (mode 0600, written with internal/atomicfile
  after every reply handled). It holds the thread, the lead, the owner's
  first DM, the event posts reply to, the turns, every lead reply handled,
  the posts with their event ids and sha256, the refusals with a sha256 of
  the refused line, the held progress reply's id, and `relayed_at`. It holds
  no message text.
- **The lead side.** `status --json` gains `requests[]` (the newest 20:
  `request_id`, `thread`, `lead`, `turns`, `posts`, `relayed_at`, `refused`
  by name, `closed`, `updated_at`), in the golden fixture, the fleet golden
  and the contract. The `/squad` screen shows a `requests` block under the
  gates: one line per request, `relayed <age> ago` or `not relayed`, and
  `refused: <name>`. No lead tool was added.
- **Docs.** README's Buzz section (ask from the phone, the receipt, `re
  <id>`, what the relay never carries), README.html regenerated,
  docs/status-contract.md, `remote attach --help`.

## 2. The two numbers as built

**Progress rate bound.** A lead reply is progress when the lead marks it on
its send: `--labels progress`, or `--kind status` (the pi lead's
`amq_reply` sets a kind and has no labels parameter). Per cockpit thread, a
progress reply posts at once when no progress post was made in the last 2
minutes; inside the bound it is held, and a newer held progress replaces it
(the replaced one is reported `superseded`). The held one posts when the
bound passes, unless an answer or a result came after it, which supersedes
it. Answers and results (`--labels result` or `--kind decision`) post at
once. Each lead reply is at most one post. A long reply is split into at
most three parts at paragraph boundaries; those parts are one post of one
reply, so "no fan-out" holds per reply. The relay never composes a line
from status.

**Gate-shaped refusal.** Every line of a lead reply is checked; one match
refuses the whole reply and nothing reaches the DM.

1. A line that is, as a whole, an answer form: gateverify's inbound parser
   (`gatestore.ParseAnswer`) accepts it, or, after trimming punctuation and
   case, it is `yes`, `no`, `approve` or `deny` alone or followed by a gate
   id (a known id, or `rel-…`, `gate-…`, `<kind>-<20 hex>`) and the rest of
   the typed tuple (class, hex prefix).
2. A line naming an open gate's id, as a word.
3. A line carrying a line of an open gate's question text.

Open gates come from the gate store of every squad core the attachment
serves. A record that does not read counts its id as open (fail closed); a
store that does not list stops the relay for that tick. A refusal is
`relay_refused_gate_shape` with the sha256 of the trimmed line.

**The thread guard.** A relay post replies to the owner's first DM of its
request. When that DM was itself in a gate question's thread, the request
posts top level instead. The poster path also refuses any post whose thread
is a stored gate question's or re-ask's (`relay_refused_gate_thread`). A DM
whose thread names a gate question is never a continuation, so the gate
path keeps every answer in a gate thread as before.

## 3. Rows

Every row is in `go test ./...` or `npm test`.

| Row | Test |
| --- | --- |
| 1. A lead reply posts to the DM, `re <id>` first, redacted (a canary `redact.Block` replaces is absent); the receipt; the cockpit thread | internal/buzz `TestRelayPostsTheLeadsReplyToTheOwnersDM` |
| 2. Five progress replies in a minute give one post; the newest held one posts after the bound; two answers give two posts; a result closes | internal/buzz `TestRelayProgressRateBound` |
| 2. A state change read from status gives no post | internal/cli `TestRemoteAttachRelaysTheLeadsReply` |
| 3. Each refusal form; the sentences that post; never into a gate thread | internal/buzz `TestRelayRefusesGateShapedLines` (five subtests) |
| 4. The relay takes a poster interface; the lead-side packages import no key path | internal/buzz `TestTheLeadSideHoldsNoKeyPathNorPoster` |
| 5. Restart: nothing posted again, the next reply posted | internal/buzz `TestARestartedAttachmentNeitherRepostsNorSkips` |
| 6. A prepared release's two questions appear once each | internal/buzz `TestAGateQuestionGoesByItsOwnPathOnce` |
| 7. The lead's tool list is the exact thirteen | pi/test/tools.test.ts |
| 8. Three DMs, one request, three posts with the same `re <id>`; a fourth DM opens a second request | internal/buzz `TestThePhoneConversation` |
| An answer in a gate thread that also names a relay post stays on the gate path | internal/buzz `TestAnAnswerInTheGateThreadStaysOnTheGatePath` |
| The split | internal/buzz `TestALongReplyIsSplitIntoAtMostThreeParts` |
| The record decodes strictly as `status.RelayRecord`, mode 0600 | internal/buzz `TestARelayRecordIsAStatusRelayRecord` |
| `amq send --thread` and `amq thread` through the installed amq in a scratch root | internal/buzz `TestAMQThreadsThroughTheCLI` |
| The attach loop relays, signed by the share body over the core's transport | internal/cli `TestRemoteAttachRelaysTheLeadsReply` |
| `status` shows the requests, redacted, nullable `relayed_at`, golden-valid | internal/status `TestStatusShowsThePhoneRequests` |
| The screen's requests block | pi/test/screen.test.ts |

Row 4 lists the dependencies of internal/status, schedproc, task,
scheduler, piruntime and leadsession with `go list -deps` (GOPROXY=off),
and refuses internal/buzz, internal/nostr and internal/gateverify. It also
checks that the pi extension's sources name no key path.

Existing rows changed only where the receipt changed: the `routed <dest>`
expectations in internal/buzz (handler, fixround2, enroll) and internal/cli
(remote) now expect `accepted q-<12 hex> routed <dest>`.

## 4. Revert runs

`python3 docs/v3-spike/31-evidence/31-4-revert-runs.py` from a clean
checkout. Each run reverts one change, runs the rows that must catch it,
and restores the tree.

```text
REVERT the post without redact.Block: fails (the row catches it)
   FAIL	github.com/omriariav/amq-squad/v3/internal/buzz [build failed]
REVERT the post without its re <request id> line: fails (the row catches it)
   --- FAIL: TestRelayPostsTheLeadsReplyToTheOwnersDM (0.03s)
REVERT the receipt is routed, not accepted <request id>: fails (the row catches it)
   --- FAIL: TestRelayPostsTheLeadsReplyToTheOwnersDM (0.02s)
REVERT the question post off the cockpit thread: fails (the row catches it)
   --- FAIL: TestRelayPostsTheLeadsReplyToTheOwnersDM (0.02s)
REVERT the post not in the request's DM conversation: fails (the row catches it)
   --- FAIL: TestRelayPostsTheLeadsReplyToTheOwnersDM (0.04s)
REVERT no progress rate bound: fails (the row catches it)
   --- FAIL: TestRelayProgressRateBound (0.06s)
REVERT the held progress never posts: fails (the row catches it)
   --- FAIL: TestRelayProgressRateBound (0.06s)
REVERT kind status is not progress: fails (the row catches it)
   --- FAIL: TestRelayProgressRateBound (0.06s)
REVERT a result does not close the request: fails (the row catches it)
   --- FAIL: TestRelayProgressRateBound (0.09s)
REVERT no whole-line answer form: fails (the row catches it)
   --- FAIL: TestRelayRefusesGateShapedLines (0.07s)
   --- FAIL: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.07s)
REVERT the refusal not narrowed (any line containing the word): fails (the row catches it)
   --- FAIL: TestRelayRefusesGateShapedLines (0.07s)
   --- FAIL: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.07s)
REVERT no open gate id refusal: fails (the row catches it)
   --- FAIL: TestRelayRefusesGateShapedLines (0.08s)
   --- FAIL: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
REVERT no open gate question text refusal: fails (the row catches it)
   --- FAIL: TestRelayRefusesGateShapedLines (0.08s)
   --- FAIL: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
REVERT the poster's gate-thread guard gone: fails (the row catches it)
   --- FAIL: TestRelayRefusesGateShapedLines (0.09s)
   --- FAIL: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.09s)
REVERT a request opened in a gate thread posts into it: fails (the row catches it)
   --- FAIL: TestRelayRefusesGateShapedLines (0.06s)
   --- FAIL: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.06s)
REVERT a lead-side package imports the key path: fails (the row catches it)
   --- FAIL: TestTheLeadSideHoldsNoKeyPathNorPoster (0.05s)
REVERT handled replies not recorded (a restart posts again): fails (the row catches it)
   --- FAIL: TestARestartedAttachmentNeitherRepostsNorSkips (0.06s)
REVERT the relay carries gate-shaped replies: fails (the row catches it)
   --- FAIL: TestAGateQuestionGoesByItsOwnPathOnce (0.12s)
REVERT a lead relay tool added: fails (the row catches it)
   not ok 7 - the lead's tool list is unchanged by the two-way phone: the exact thirteen
REVERT no continuation (every DM a new request): fails (the row catches it)
   --- FAIL: TestThePhoneConversation (0.08s)
REVERT an answer form in the conversation goes to the gate path: fails (the row catches it)
   --- FAIL: TestThePhoneConversation (0.09s)
REVERT a continuation does not follow the request's lead: fails (the row catches it)
   --- FAIL: TestThePhoneConversation (0.11s)
REVERT a gate-thread answer that names a relay post leaves the gate path: fails (the row catches it)
   --- FAIL: TestAnAnswerInTheGateThreadStaysOnTheGatePath (0.08s)
REVERT no split of a long post: fails (the row catches it)
   --- FAIL: TestALongReplyIsSplitIntoAtMostThreeParts (0.00s)
REVERT amq send without --thread: fails (the row catches it)
   --- FAIL: TestAMQThreadsThroughTheCLI (0.12s)
REVERT remote attach without the relay: fails (the row catches it)
   --- FAIL: TestRemoteAttachRelaysTheLeadsReply (0.05s)
REVERT status without the requests: fails (the row catches it)
   --- FAIL: TestStatusShowsThePhoneRequests (0.00s)
REVERT relayed_at not nullable: fails (the row catches it)
   --- FAIL: TestStatusShowsThePhoneRequests (0.00s)
REVERT request fields not redacted: fails (the row catches it)
   --- FAIL: TestStatusShowsThePhoneRequests (0.00s)
REVERT the screen without the requests block: fails (the row catches it)
   not ok 2 - the requests block marks each phone request relayed or refused, by name
30 of 30 reverts caught
```

## 5. The owner-run row (not run; the owner runs it)

On this repository, with the share enrolled and bound as today, and a lead
seat `cto` in a squad session `<s>`.

1. Build the branch head: `go build -o /tmp/31-4/amq-squad ./cmd/amq-squad`.
2. Attach to the lead: `/tmp/31-4/amq-squad remote attach --lead cto
   --project ~/code/amq-squad --session <s> --json | tee /tmp/31-4/attach.jsonl`.
3. From the phone, DM the body: `what is the state of the train? send
   progress as --kind status replies`. Record the receipt under the DM
   within 60 s: `accepted q-<12 hex> routed cto`.
4. In the lead's pane, see the message on thread `cockpit/q-<id>`. The lead
   replies on that thread. Record the post in the DM that starts with `re
   q-<id>`, and the `relayed` line in attach.jsonl.
5. Have a seat change state (a task claimed or merged) and the lead send a
   progress reply (`amq reply --id <msg> --kind status`). Record one
   progress post. A second progress reply within 2 minutes is held, and the
   newest posts after the bound.
6. Reply in Buzz to the relay post: `and the next task?`. Record the same
   `accepted q-<id>` and the lead's answer as a second `re q-<id>` post.
7. Ask the lead (in its pane) to reply on the thread with the single line
   `yes`. Record that nothing posts, and that `amq-squad status --json
   --project ~/code/amq-squad --session <s> | jq .requests` shows the
   request with `"refused": ["relay_refused_gate_shape"]`. The `/squad`
   screen shows `refused: relay_refused_gate_shape` on the request's line.
8. Stop attach (Ctrl-C), start it again, and record that no post repeats.

## 6. Retirement

The lead reply relay is R33's one added purpose. It retires, with the body
key's use for it, when amq-remote offers a post surface on serve's
connection: the relay then hands the lead's replies to serve and holds no
key. Upstream ask 19 below is that surface.

## 7. Upstream ask 19 (unfiled)

Not filed: the owner has not said go. The text as it would be filed on
avivsinai/agent-message-queue:

> **Conversation mode on the mailbox adapter.** The mailbox adapter posts
> the handle's newest bodied non-status reply as the request's result, once,
> within `reply_timeout` (60 s in amq-squad's share). A lead's turn takes
> minutes, so its answer can never be that reply, and amq-squad holds the
> share's body key in its attachment process to post the answer itself (its
> "lead reply relay"). Ask: a `conversation` mode on the adapter binding in
> which every bodied reply on the request's thread posts (status replies as
> progress rows, rate-bounded by serve), until a reply marked final or the
> conversation timeout; and an owner DM that replies to one of those posts is
> delivered on the same thread. With it, amq-squad drops the body key from
> its relay and posts only through serve's connection.

## 8. Choices the brief left open (the smallest diff each)

- The record lives under the mailbox's AMQ root (the root of the cockpit
  threads), so the lead's `status` reads it. In operator mode that is the
  operator session's root.
- Result marking: `--labels result` or `--kind decision`. Progress:
  `--labels progress` or `--kind status`. The kinds exist so the pi lead's
  `amq_reply` can mark both without a new tool parameter.
- The receipt keeps today's routing words after `accepted <request id>`.
- The question-text form matches any line of an open gate's question (the
  question is multi-line, so a single line can never carry all of it).
- A follow window of 24 hours after a request's newest owner turn; a closed
  request is not read again until a continuation opens it.
- A post that fails is retried next tick. If a later part of a split post
  fails, the parts already posted are recorded and not posted again.

## 9. Where the brief was wrong

- "The handler's reply is `accepted <request id>` and the question goes to
  the lead's cockpit thread (thread cockpit/<channel>)": neither existed.
  The receipt was `routed <dest>`, and the question went on AMQ's default
  p2p thread. `cockpit/<channel>` is serve's thread into the attach inbox.
  This brief builds both (plan 3.2 says the receipt "becomes" `accepted`).
- "A repeated question re-posts the current cockpit state as today":
  nothing does that today. A redelivered DM is still refused as `replayed`.
  Re-posting a state would make the core an author on the owner's phone,
  which the same brief forbids.
- "The exact thirteen": TeamLeadTools in internal/piruntime has eleven. The
  pi lead registers thirteen (the eleven plus `amq_task_claim` and
  `amq_task_done`); row 7 pins those thirteen.
- "An open gate's exact question text anywhere in the line": the question
  is multi-line; see section 8.
- "The gates and requests blocks" of screen.ts: there was no requests
  block; this brief adds it.
- Plan 3.2 says the replies come "from the attach handle"; the brief says
  from the lead's handle. Built as the brief says: the relay posts only
  replies from the lead the question went to.

## 10. Gate

Run on e3993144965fc9a682569bc7b6f72cd24353fd05 (the code head; later commits on the branch are this note only):

```text
go build ./... && go vet ./... && go test -count=1 ./...   exit 0: 56 packages ok, 0 FAIL
cd pi && npm ci --offline --ignore-scripts && npm test    exit 0: tests 60; pass 60; fail 0
PATH=<pyvenv>/bin:$PATH make ci                         exit 0
```

## 11. A line for the 3.1 release notes

- The phone is two-way (brief 31-4): a Buzz question gets `accepted <request
  id>` at once, and the lead's answer, progress and result arrive as DM posts
  starting with `re <request id>`; reply to one to continue the request. The
  relay posts only the lead's own text, redacted and rate-bounded. It never
  carries a gate answer's shape or posts into a gate thread. It retires with
  upstream ask 19.
