# 3.3.0 A0: who may answer a gate, per channel

Line numbers as of `8d66f50e58a5d8c0635cd0d44d7dc21cdf54b488`.
This note records what the repository does today beside the decisions already
made for 3.3.0. It does not change those decisions.

## Decided

    D1 = A. The Grok bot carries questions and the owner's text; its yes never
    binds a gate. Conversation stays free; only the binding answer is the
    owner's signed reply in Buzz.
    D2 = A. The lead's chat shows every open question in plain words and says
    where to answer (Buzz). Nothing typed in the lead's chat binds a gate,
    beyond /confirm for a team plan as today. Reason: anything that can type
    into the pane types as the owner.
    D3 = A. The words: yes, no, approve, deny, plus y, ok, go ahead (yes) and
    n, stop (no), plus כן (yes) and לא (no). Matching is case-insensitive and
    trimmed, with one trailing punctuation mark allowed; anything else on the
    line ("yes, but...", "ok?" followed by more words) binds nothing. One
    list, in Go, used by every channel. The model never classifies an answer.
    D4 = A. An unthreaded bare answer binds only when exactly one gate is open
    AND that gate's question is the latest question the squad has shown the
    owner in that channel; if any lead message ending in a question came after
    it, the answer is re-asked in the gate's thread, as today. With two or
    more open, the harness (not the lead) asks which, by letter, and binds the
    letters. A yes never binds a gate raised after it.
    The window: when a gate's window lapses, the gate is asked again in the
    same Buzz thread with a new window; an answer created before the re-ask
    does not approve the re-asked question.
    Who may answer: the owner only, by his signed Buzz reply, checked against
    his pinned key. No model output is an input of the binding decision.

A1 closes the words-list and which-gate gaps, including D4. A2 closes the
window re-ask gap. A3 makes the lead's chat show questions without adding a
binding verb. A4 keeps which-gate and the re-ask in Buzz. A5 adds the Grok bot
channel under D1.

## Per channel

### Buzz: the owner's DM and gate threads

| Cell | Today | Decided |
| --- | --- | --- |
| Who | The attachment fetches the event named by id and routes a gate-shaped owner DM to the core. The core accepts a decision only after the owner checks below and a successful commit (`internal/buzz/inbound.go:224-265`, `internal/gateverify/core.go:167-246`). | The owner, through the owner's signed Buzz reply. No model output counts. |
| Proof | The fetched event's id must recompute; its public key must equal the pinned owner key; its signature must verify; it must be kind 9; and its `h` tag must be exactly the pinned channel (`internal/buzz/inbound.go:282-300`). `Verify` first validates the gate record, its squad, the pin and the record's owner (`internal/gateverify/verify.go:115-129`), then repeats those checks on the event, then checks the tuple or bare word, digest, window, question and thread (`internal/gateverify/verify.go:115-212`). | Keep the fetch by id and all five owner-event checks. The pinned Buzz signature, not carrier or model provenance, is the sender proof. |
| Replay | Before verification the core recovers a half-written decision, then refuses a consumed gate or event. The store checks both durable marks (`internal/gateverify/core.go:191-207`, `internal/gatestore/store.go:484-501`). | A binding event is single-use and cannot move to another gate or a new window. |
| Window | The answer's `created_at` must be inside the record's original window and presentation must be no later than `not_after` (`internal/gateverify/verify.go:169-178`). A re-ask is made only while that same record is open; an expired record gets no new window (`internal/gateverify/core.go:298-305`). | A lapsed gate is asked again in the same thread with a new window. An answer created before that re-ask cannot approve it. A2 supplies the new-window record and comparison. |
| Words | `ParseBare` trims space, rejects embedded CR/LF, lowercases, strips **all** trailing Unicode punctuation, and accepts only `yes`, `no`, `approve`, `deny`. Therefore `yes!!` parses as approve today (`internal/gatestore/plain.go:555-572`). | D3's one Go list, including the short and Hebrew forms, and no more than one trailing mark. The model does not classify. A1 closes the gap. |
| Ambiguity | A threaded bare answer names exactly one gate by question or re-ask. An unthreaded bare answer selects the sole open gate but is then refused as outside the thread and re-asked there. With zero or multiple open gates it names none; no letter choice is offered (`internal/gateverify/core.go:249-295`). Extra words fail `ParseBare`. | One open gate may bind an unthreaded answer only under D4's latest-question rule. With two or more, the harness asks which by letter in Buzz. A1 and A4 close the gap. |

### The lead's chat (the pi pane)

| Cell | Today | Decided |
| --- | --- | --- |
| Who | No typed-gate answer path is registered in the pane. `/answer` shows or posts the gate question and never writes an answer (`pi/extensions/lib/keys.ts:21-28`). `/confirm` is a different door: it confirms a team plan (`pi/extensions/lib/team.ts:1-21`). | The chat shows every open question and says to answer in Buzz. Text typed there does not bind a gate. `/confirm` keeps its existing team-plan meaning. A3 carries this. |
| Proof | Pi hands `/confirm` to the extension, which shows a confirm dialog and sends the selected digest only on its yes. The lead accepts the request only from the pi process it spawned, with the recorded pid and start stamp (`pi/extensions/lib/team.ts:538-574`, `internal/cli/lead_start.go:427-445`). This proves the team-plan command came through that pi instance; it is not proof of a signed Buzz gate answer. | No chat proof is promoted to typed-gate sender proof. |
| Replay | The lead witnesses the selected team-plan digest in memory. This replay behavior belongs to team start, not the gate store. No chat event can be consumed as a gate answer. | Gate replay remains a Buzz/core property. |
| Window | `/confirm` offers live team plans younger than 30 minutes. It has no typed-gate window (`pi/extensions/lib/team.ts:538-574`). | The chat displays open gate questions; it does not own or renew their windows. |
| Words | `/confirm` is a slash command and a dialog, not a free-text yes parser. The removed shell door is refused because one uid cannot distinguish the owner's shell from a tree the model built (`internal/cli/lead_start.go:301-308`). | Nothing typed as chat text binds. D3's words are interpreted only on the binding Buzz path. |
| Ambiguity | When several team plans are live, `/confirm` asks the owner to select one before the dialog. There is no analogous binding typed-gate selection in the pane (`pi/extensions/lib/team.ts:545-564`): `/answer` asks which open gate to show, and the choice selects what is displayed or posted, never an answer (`pi/extensions/lib/keys.ts:295-328`). | The pane may display multiple gates, but A4 keeps the binding which-gate choice in Buzz. |

### The Grok bot

| Cell | Today | Decided |
| --- | --- | --- |
| Who | No Grok bot face exists in this repository today; the repository's follow-up says so (`docs/v3-followups.md:615-623`). The non-doc, non-test search below found no `grok bot` or `grokbot` match. | The bot carries questions and the owner's text, but its yes does not bind. A5 adds the channel under D1. |
| Proof | There is no channel implementation here and therefore no repository check of an AMQ bridge signature for this channel. A future bridge signature may prove that AMQ carried a message; it does not prove the pinned owner signed a Buzz event. | Only the owner's signed Buzz reply, checked against the pinned key, is binding. |
| Replay | No Grok gate-answer replay seam exists today. | Relayed conversation stays non-binding; the eventual binding Buzz event uses the core's event-and-gate replay marks. |
| Window | No Grok gate window exists today. | Questions may be carried, but the binding window and any re-ask remain in Buzz. |
| Words | No Grok answer parser exists today. | The shared D3 list may recognize conversation text, but only the signed Buzz reply can bind. The model never classifies. |
| Ambiguity | No Grok which-gate path exists today. | The harness asks which in Buzz; the bot's text never selects a binding gate. |

The exact searches used for the negative path review were:

```text
rg -n 'ParseBare\(' --glob '*.go' --glob '!**/*_test.go' .
7 matches
rg -n 'HandleEvent(ID)?\(|ownerEvent\(|FetchEvent\(' --glob '*.go' --glob '!**/*_test.go' .
13 matches
rg -n -i 'grok bot|grokbot' --glob '!docs/**' --glob '!README.html' --glob '!**/*_test.go' --glob '!**/*.test.ts' .
0 matches
rg -n '\bVerify\(' --glob '*.go' --glob '!**/*_test.go' internal/gateverify
3 matches
rg -n 'gateverify\.Verify\(' --glob '*.go' --glob '!**/*_test.go' .
0 matches
```

All seven `ParseBare` sites and all thirteen binding-entry/fetch sites were
read. No path was found by which a model-written lead relay, seat message or
tool result is accepted as the owner. The lead relay refuses any whole line
with an answer shape and never posts into a gate question thread
(`internal/buzz/relay.go:34-45`, `internal/buzz/relay.go:646-683`). If text
nevertheless reaches the core, the repeated pinned-owner checks refuse the
body key, a seat key, a bad signature, the wrong kind or the wrong channel
before a decision. A continuation shaped like an answer remains conversation
text and leaves the gate open (`internal/buzz/phone_test.go:444-472`). A seat
message is refused as the wrong public key (`internal/gateverify/core_test.go:238-243`).

The first two searches do not reach every caller of `Verify`. The fourth and
fifth do: of the three matches one is the definition
(`internal/gateverify/verify.go:115-212`) and two are callers, both inside the
package. The core's call is the path read above
(`internal/gateverify/core.go:207`). The other is `VerifyAnswerFile`
(`internal/gateverify/answer.go:197`), reached through `Verifier.Accepted`
(`internal/gateverify/growth.go:25-41`): it verifies again the signed event
stored with an answer the core already accepted. It is a re-verification of
the same signed owner event, not a second way in, and it was not read for the
paragraph above.

## Rows every A and B brief must carry

"Today" uses the three requested values; row 12's is narrowed in its cell,
and row 8's test covers half of its statement. `holds` means both a seam and
an existing test assert the statement; `does not hold` names the current
contrary behavior; `not determinable` means the channel or assertion has no
seam and test in this repository.

| # | Statement | Today | Today's seam | Nearest existing test | Brief |
| --- | --- | --- | --- | --- | --- |
| 1 | A model-written yes in a relay binds nothing. | holds | Relay answer-shape refusal, then pinned-owner verification (`internal/buzz/relay.go:646-683`, `internal/buzz/inbound.go:282-300`). | `internal/buzz/phone_test.go:250-300` asserts answer-shaped lead lines are refused and not posted. | A3, A5, B3 |
| 2 | An answer with a condition or extra words ("yes but wait for CI", "ok? and then") binds nothing. | holds | Whole-line `ParseBare` (`internal/gatestore/plain.go:555-572`). | `internal/gatestore/plain311_test.go:474-487` asserts `yes please` is not parsed. | A1, A4 |
| 3 | Each word of the list binds, trimmed, in any case, with at most one trailing punctuation mark; two marks bind nothing. | does not hold | Today has four words and strips every trailing punctuation rune (`internal/gatestore/plain.go:555-572`). | `internal/gatestore/plain311_test.go:474-487` covers the four words and one-mark examples; it has no two-mark refusal row. | A1 |
| 4 | With two gates open, the harness asks which; a letter binds its gate; a letter that names no question binds nothing. | does not hold | Multiple open gates return `RejectThread`; there is no letter parser (`internal/gateverify/core.go:249-295`). | `internal/gateverify/bare311_test.go:85-92` asserts refusal with no re-ask. | A1, A4 |
| 5 | An unthreaded answer binds only when one gate is open and its question is the latest the squad showed the owner in that channel. | does not hold | One open gate is selected, then the answer is re-asked solely because it is unthreaded; there is no latest-question comparison (`internal/gateverify/core.go:249-295`). | `internal/gateverify/bare311_test.go:61-92` asserts the one-gate re-ask and multi-gate refusal. | A1, A4 |
| 6 | A yes never binds a gate raised after it. | does not hold | The verifier permits `created_at` as early as `issued_at - 60 s`; it has no strict answer-after-raise comparison (`internal/gatestore/record.go:38-46`, `internal/gateverify/verify.go:169-178`). | `internal/gateverify/core_test.go:155-166` asserts the skew/window edges, not the D4 ordering rule. | A1, B3 |
| 7 | An answer created before a re-ask does not approve the re-asked question. | does not hold | A valid re-ask id is added to the allowed thread ids, but `Verify` compares time only with the original record window (`internal/gateverify/verify.go:169-204`). | `internal/gateverify/core_test.go:221-228` asserts a reply to the re-ask is accepted; it has no pre-re-ask timestamp refusal. | A2, A4 |
| 8 | One decision per event id survives the re-ask. | holds | Gate and event consumption marks are checked before verification (`internal/gatestore/store.go:484-501`). | `internal/gateverify/core_test.go:71-76` asserts the same event and a later decision are replay refusals, with no re-ask in the test. The re-ask test accepts one reply and replays nothing (`internal/gateverify/core_test.go:187-228`). No existing test replays an event after a re-ask. | A2, B3 |
| 9 | Nothing typed in the lead's chat binds a gate (beyond /confirm for a team plan). | holds | `/answer` writes no answer; `/confirm` is a registered command accepted only from the spawned pi (`pi/extensions/lib/keys.ts:21-28`, `internal/cli/lead_start.go:427-445`). | `pi/test/keys.test.ts:317-340` asserts the answer key on each of the four typed gate kinds runs gate status only and writes no answer. `internal/cli/lead_start_test.go:514-563` asserts a model-shell child cannot use the TUI door and the spawned pi can. | A3 |
| 10 | A yes relayed by the Grok bot binds nothing. | not determinable | No Grok bot face or gate seam exists (`docs/v3-followups.md:615-623`); the scoped non-test code search above had 0 matches. | none found; same 0-match search. | A5 |
| 11 | Every binding answer is the owner's signed Buzz reply, fetched by id and checked against the pinned key. | holds | Fetch and owner-event checks (`internal/buzz/inbound.go:224-265`, `internal/buzz/inbound.go:282-300`). | `internal/buzz/handler_test.go:172-247` asserts the handler acts only on the event fetched by id, never on the delivered text. `internal/gateverify/core_test.go:23-76` asserts owner success and wrong key, body key, forged signature and replay refusals on the raw event it is given. | A1, A2, A4, B3 |
| 12 | No model output is an input of the binding decision. | holds, narrowed: model output is never accepted as the owner's answer (`internal/buzz/relay.go:646-683`, `internal/gateverify/verify.go:130-148`); the gate's target and its question text are written by the caller that raised it (`internal/cli/gate.go:138-139`, `internal/cli/gate.go:172`; `internal/cli/gate_store.go:99`; `internal/gatestore/record.go:171-181`) and `Verify` consumes them | Relay output is held back by shape; the core independently checks the signed owner event (`internal/buzz/relay.go:646-683`, `internal/gateverify/verify.go:130-148`). The question must carry the record's text (`internal/gateverify/verify.go:219-237`). | `internal/buzz/phone_test.go:250-300` and `internal/gateverify/core_test.go:34-49` assert the two layers. | A3, A5, B3 |

No proposed row: D1 through D4's binding properties are represented by rows
1 through 12. The free-conversation half of D1 is channel behavior, not an
additional binding invariant.

## The three legacy `clear-*` records

This section uses only the lead-supplied inventory of the live store. No live
record, key, signature or event id was read.

| Gate record | Size | Written |
| --- | ---: | --- |
| `gates/clear-a93731e99c650d475b3a19ed6854f9a6.json` | 1576 bytes | 2026-10-30 |
| `gates/clear-a48ee38fac1fd3503e010e1c66554a58.json` | 1588 bytes | 2026-10-30 |
| `gates/clear-0a227ca8d88dc97c65c540a4d0fa18a7.json` | 1580 bytes | 2026-10-31 |

For each, the lead reported schema `amq-squad-gate/1`, class
`quarantine_clear`, `raised_by` `owner`, and the fields `schema`, `gate_id`,
`class`, `gate_kind`, `typed_class`, `target`, `digest`, `text`,
`text_sha256`, `issued_at`, `not_before`, `not_after`, `raised_by`,
`owner_pubkey`, `issuer_key_id`, and `squad`. Each also has a
`.question.json`, a `.question.published`, and one entry under `answers/`.
This note records no value from an answer entry. Rc.2 reports each as
`gate clear-<id> is a legacy quarantine_clear gate (a kind this build no
longer carries); skipped`.

### Writer and removal

`git log -S'quarantine_clear' -- internal` lists ten commits on this base. The
writer was introduced by
`a724b3db0d93e95d9f150b21543383b5b768e2ab`: its `raiseClear` built a
`gatestore.Spec` with action `quarantine_clear`, raised or created the record,
then wrote the clear request
(`a724b3db0d93e95d9f150b21543383b5b768e2ab:internal/cli/seat_clear_gate.go:211-240`).
The same historical code described the gate as the request half of a signed
seat clear
(`a724b3db0d93e95d9f150b21543383b5b768e2ab:internal/cli/seat_clear_gate.go:27-35`).

Commit `85bef34e76e3f6b159df03113202227959d68a36` deleted
`internal/cli/seat_clear_gate.go`, `internal/gatestore/clear.go` and the clear
verifier. Commit `fdfa28ff1a8949f75beed304c9f73dcb8f745174` removed
`quarantine_clear` from `gatetarget.classOf`, `Actions` and the target helpers.
That catalog change is what changed validation for stored records.

On this base, `Record.Validate` calls `gatetarget.Validate(r.Class, r.Target)`
as part of its first malformed-record case (`internal/gatestore/record.go:205-228`).
`gatetarget.Validate` rejects an action whose `Class` is empty, and today's
catalog contains only the six current actions (`internal/gatetarget/gatetarget.go:45-83`).
Thus a `quarantine_clear` record reaches the malformed-record result before a
later check. The separate verifier would also refuse a pre-squad record, but
that is not the first `Validate` result.

### Today's legacy handling and readers

`LegacyKind` defines a non-empty class absent from the current typed-gate
catalog as legacy. `Store.Legacy` reads just `gate_id` and `class`, and
`LegacyLine` prints the stable skip sentence (`internal/gatestore/legacy.go:11-41`).

The exhaustive non-test Go search was:

```text
rg -n '(\.Legacy\(|LegacyKind\()' --glob '*.go' --glob '!**/*_test.go' .
6 matches
```

Two matches are the definition and its internal call. The four readers are:

1. Merge-train drift skips the record, adds `LegacyLine` to notes and records
   that the path was reached (`internal/mergetrain/drift.go:341-355`).
2. `status` lists each legacy record as a `gate store legacy:` warning
   (`internal/cli/status.go:87-108`).
3. Hook gate collection skips a legacy class; status owns the line
   (`internal/cli/hook_store.go:63-77`).
4. `gate status`, including `--gate`, prints `gate status: ` plus
   `LegacyLine` and continues (`internal/cli/gate_store.go:331-346`).

The three records are fully explained by the lead-supplied description: the
deleted writer created a `quarantine_clear` gate; the normal gate path wrote
its question, publication marker and signed-answer sidecar; the catalog later
dropped the kind. Their listed record fields match the historical gate shape.
The supplied inventory exposes no field or adjacent file that needs a second
writer. This is an explanation of the inventory, not a fresh validation of
the live files.

### Rules later A briefs may quote

1. **Retire a kind with its stored records accounted for.** Before removing a
   kind from `gatetarget`, land either continued validation, a migration, or an
   explicit legacy reader. Removing the class makes `Record.Validate` reject
   every stored record of that kind (`internal/gatestore/record.go:205-228`,
   `internal/gatetarget/gatetarget.go:45-83`).
2. **Do not turn a known retired kind into a store failure.** Every reader must
   list or skip it through `LegacyKind`, `Store.Legacy` and `LegacyLine`
   (`internal/gatestore/legacy.go:11-41`).
3. **Keep the immutable record and its evidence sidecars.** A removed writer
   does not justify deleting a stored question, answer or consumption proof;
   readers can make the record non-blocking without erasing its history
   (`internal/gatestore/record.go:1-15`).
4. **Use the same catalog at write and read time.** A new writer must create
   only an action that `gatetarget.Validate` accepts, and its removal plan must
   cover records already written (`internal/gatetarget/gatetarget.go:45-83`).

No record is migrated, deleted or proposed for removal here.

## Citations

Every source citation above is measured on the base SHA unless its row names a
past commit.

| Citation | First cited line, verbatim (trimmed) |
| --- | --- |
| `internal/buzz/inbound.go:224-265` | `raw, err := gateverify.FetchEvent(vctx, relay, id)` |
| `internal/buzz/inbound.go:282-300` | `// ownerEvent checks the fetched event is the owner's DM to the body: the id` |
| `internal/gateverify/core.go:167-246` | `func (c *Core) handleEvent(ctx context.Context, raw []byte) Result {` |
| `internal/gateverify/core.go:191-207` | `// repair a half-marked decision before the replay check reads the marks` |
| `internal/gateverify/core.go:249-295` | `// bareGate is the gate a bare yes or no is for (brief 3.1.1-1): the one gate` |
| `internal/gateverify/core.go:286-295` | `if len(refs) == 0 {` |
| `internal/gateverify/core.go:298-305` | `// reask posts the gate's one "reply here to confirm" in the gate thread. The` |
| `internal/gateverify/verify.go:115-212` | `func Verify(raw []byte, rec gatestore.Record, q *gatestore.Question, reask *gatestore.Reask, pin Pin, now time.Time) (Outcome, error) {` |
| `internal/gateverify/verify.go:130-148` | `e, err := nostr.ParseEvent(raw)` |
| `internal/gateverify/verify.go:169-178` | `_, nb, na, _ := rec.Window()` |
| `internal/gateverify/verify.go:169-204` | `_, nb, na, _ := rec.Window()` |
| `internal/gatestore/plain.go:555-572` | `// ParseBare reads a bare answer (brief 3.1.1-1): yes, no, approve or deny` |
| `internal/gatestore/store.go:484-501` | `// Replayed reports whether a gate already has a consumed answer or an event` |
| `internal/gatestore/record.go:1-15` | `// Package gatestore is the gate store the Go core writes (plan 7.3, R32):` |
| `internal/gatestore/record.go:38-46` | `const (` |
| `internal/gatestore/record.go:205-228` | `// Validate checks that a record is self-consistent: the digest is the target's,` |
| `internal/gatetarget/gatetarget.go:45-83` | `// classOf maps each typed-gate action to its plan class. Merge is not a typed` |
| `internal/buzz/relay.go:34-45` | `// The lead reply relay (brief 31-4; plan 3.1 section 3.2; R33's one added` |
| `internal/buzz/relay.go:646-683` | `// refuse runs the gate-shape check over every line of a reply and returns` |
| `pi/extensions/lib/keys.ts:21-28` | `// The answer key is Q7's fallback (the execution note): the terminal door` |
| `pi/extensions/lib/team.ts:1-21` | `// The solo lead opens a team from the conversation (brief 31-3, PR 1):` |
| `pi/extensions/lib/team.ts:538-574` | `// confirm is the TUI door with nothing to type (brief 3.1.1-3 item 5):` |
| `pi/extensions/lib/team.ts:545-564` | `async confirm(ctx: ExtensionContext): Promise<KeyOutcome> {` |
| `internal/cli/lead_start.go:301-308` | `case req.Kind == confirmTUIKind:` |
| `internal/cli/lead_start.go:427-445` | `// witnessTUI is the TUI door: accepted only from the pi process the lead` |
| `docs/v3-followups.md:615-623` | `**Direction for 3.1 (owner, 2026-10-30, in his words):** "we want to copy` |
| `internal/buzz/phone_test.go:250-300` | `// Row 3: the gate-shaped refusal, one sub-row per form; the sentences` |
| `internal/buzz/phone_test.go:444-472` | `// Row 8: the conversation. Three owner DMs, the first fresh and the next` |
| `internal/gatestore/plain311_test.go:474-487` | `// ParseBare reads yes, no, approve and deny alone, case and trailing` |
| `internal/gateverify/bare311_test.go:61-92` | `func TestABareYesOutsideTheThreadGetsThePlainReask(t *testing.T) {` |
| `internal/gateverify/bare311_test.go:85-92` | `// with two open gates, a bare yes outside every thread names neither` |
| `internal/gateverify/core_test.go:23-76` | `// Acceptance 2: each of the four classes through the core, on synthetic` |
| `internal/gateverify/core_test.go:34-49` | `// wrong digest prefix, a tuple for another gate, wrong key, forged signature` |
| `internal/gateverify/core_test.go:71-76` | `// replay: the same event, and a later deny` |
| `internal/gateverify/core_test.go:155-166` | `// the window edges: created_at - 60 s <= now <= not_after` |
| `internal/gateverify/verify.go:115-129` | `func Verify(raw []byte, rec gatestore.Record, q *gatestore.Question, reask *gatestore.Reask, pin Pin, now time.Time) (Outcome, error) {` |
| `internal/gateverify/verify.go:219-237` | `func checkQuestion(q *gatestore.Question, rec gatestore.Record, pin Pin) error {` |
| `internal/gateverify/core.go:207` | `o, err := Verify(raw, rec, q, reask, c.Pin, now)` |
| `internal/gateverify/answer.go:197` | `o, err := Verify(raw, rec, q, reask, pin, verifiedAt)` |
| `internal/gateverify/growth.go:25-41` | `func (v Verifier) Accepted(store gatestore.Store, rec gatestore.Record) error {` |
| `internal/cli/gate.go:138-139` | `target, note := *targetFlag, *noteFlag` |
| `internal/cli/gate.go:172` | `return storeGateRaise(ctx, gate, capability.Action, target, *windowFlag, *jsonOut, os.Stdout)` |
| `internal/cli/gate_store.go:99` | `spec := gatestore.Spec{GateID: id, Action: action, Target: target, RaisedBy: sel.Handle, Window: window, Squad: core.Store.Squad()}` |
| `internal/gatestore/record.go:171-181` | `digest := gatetarget.Digest(s.Action, s.Target)` |
| `pi/extensions/lib/keys.ts:295-328` | `async answer(ctx: ExtensionContext, st: ScreenStatus): Promise<KeyOutcome> {` |
| `pi/test/keys.test.ts:317-340` | `// Row 9 (Q7 answered no): each of the four typed gate kinds shows "waiting` |
| `internal/buzz/handler_test.go:172-247` | `// Acceptance 2 and the principle: the attachment never reads the DM` |
| `internal/gateverify/core_test.go:187-228` | `func TestThreadReask(t *testing.T) {` |
| `internal/gateverify/core_test.go:221-228` | `// a reply to the re-ask in the same thread also counts (second gate)` |
| `internal/gateverify/core_test.go:238-243` | `// a seat's plain message naming the gate gets no re-ask either` |
| `internal/cli/lead_start_test.go:514-563` | `// Row 1c of the witness design (the lead's half): the TUI door is accepted` |
| `internal/gatestore/legacy.go:11-41` | `// Legacy gate records (brief 31-1): a record of a kind the typed-gate` |
| `internal/mergetrain/drift.go:341-355` | `for _, id := range ids {` |
| `internal/cli/status.go:87-108` | `const (` |
| `internal/cli/hook_store.go:63-77` | `var gates []hookGate` |
| `internal/cli/gate_store.go:331-346` | `rows := []gateStatusRow{}` |
| `a724b3db0d93e95d9f150b21543383b5b768e2ab:internal/cli/seat_clear_gate.go:27-35` | `// The Buzz-signed seat clear (phase 5, plan 4.3 and 7.3; amendment 28's` |
| `a724b3db0d93e95d9f150b21543383b5b768e2ab:internal/cli/seat_clear_gate.go:211-240` | `// raiseClear writes a new request for the quarantine and raises its gate.` |

## Open questions for the lead

1. What durable data will define "the latest question the squad has shown the
   owner in that channel"? Today `bareGate` reads open records and thread ids,
   while history checking starts from a question or history re-ask; neither
   records the ordering of ordinary lead questions
   (`internal/gateverify/core.go:249-295`).
2. Does A1 replace the current punctuation test or add a two-mark refusal next
   to it? The existing test covers one-mark acceptance, while the function
   strips every trailing punctuation rune
   (`internal/gatestore/plain311_test.go:474-487`,
   `internal/gatestore/plain.go:555-572`).
3. What persisted object maps the temporary letters in the two-or-more-gates
   prompt to gate ids, and when does that mapping expire? Today multiple open
   gates produce only `RejectThread` (`internal/gateverify/core.go:286-295`).
4. Does A2 create a new immutable gate record or extend the existing record
   with a new signed window? Today a re-ask retains the original record and is
   refused once that record is not open (`internal/gateverify/core.go:298-305`).
5. Which timestamp is authoritative for "created before the re-ask": the
   signed event's `created_at`, relay receipt time, or a stored fetch time?
   Today verification has only the signed `created_at` and the original record
   window (`internal/gateverify/verify.go:169-204`).
6. A model-written caller chooses a gate's target and the facts in its
   question, and the owner's yes binds that text. Who may raise a gate and
   what a question may carry is not decided by D1 to D4.

## Corrections

2026-11-11, the lead's docs PR after the read-only Codex pass on #967 (head
`d1b4c18db59a82f23ff80387c8f66bd144ea30a4`). Line numbers below are the
note's as merged in `0c72f069e388ef461e6c0b341a9156c291a4d4a4`. No verdict on
rows 1 to 11 changed.

1. Row 12's Today cell said `holds` alone. That was broader than the code
   establishes: the caller that raises a gate writes its target and facts, and
   those become the record and the question text `Verify` consumes. The cell
   is narrowed; the statement's twelve words are unchanged. Every A and B
   brief that carries row 12 carries the narrowed cell with it.
2. The paragraph at :84-93 said "All seven ... and all thirteen ... were
   read. No path was found". The two searches behind it do not reach the
   second caller of `Verify`, `VerifyAnswerFile`. Two searches and a
   paragraph are added; the second caller re-verifies the same signed event.
3. :45 said `Verify` repeats the event checks and then checks the record. It
   validates the record first.
4. :60 said the pane has no typed-gate selection. `/answer` has one, for
   display; the sentence now says "binding".
5. Row 8's test cell cited a replay test with no re-ask in it. The cell says
   so, and that no existing test replays an event after a re-ask; the seam
   cell is unchanged. A2's brief adds that test row.
6. Row 9's test cell gains the test that asserts `/answer` writes no answer.
7. Row 11's test cell gains the test of the fetch by id; the test it cited
   checks the raw event it is given.
8. Open question 6 is added.
9. The file ended with a blank line; removed.
