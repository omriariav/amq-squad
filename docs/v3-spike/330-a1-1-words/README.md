# A1-1: the words

A1-1 puts the decided bare-answer list in `internal/gatestore/words.go:3-21` behind `BareVerb`; `ParseBare` in `internal/gatestore/plain.go:555-568` and the relay's `answerForm` in `internal/buzz/relay.go:766-809` ask that shared reader, and the parser removes at most one trailing punctuation mark. The original cut's statements that "the list is the only parser" and "No channel change" were corrected after measurement. The dispatch TIP was `2d52b79dc133d070e5e45c91eff7371f999055bc`, which was also the worktree's HEAD at the precheck; the commit that holds this note is 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf, merged into v3 as 8c915ba47b7d90a2de38af0bd0a53436c43e22bc. The seven Go files were written by the seat of task t186 from the brief whose sha256 is 9da9fdd704c67628bd71903cfe69904915d8413edaf5b0979c9207ab3aa7ede3; that task blocked on a whole run of `internal/cli`, which cannot pass in a seat, and the files are carried here unchanged, by blob hash.

```text
8087b52e39817bd3a735bf24b65148d6245b4b08  internal/buzz/relay.go
ef151cf3977ffd695ef87070b656205cc814b641  internal/buzz/words330_test.go
1ad7c22579edf12539150b584fcca9b8ad6c80df  internal/cli/words330_test.go
872e77d4765fa9236bcae4cc2820cc8acbd0d22d  internal/gatestore/plain.go
c19e3d1bbb16e67f0be7bb60aeb68dff1c4e1cca  internal/gatestore/plain311_test.go
4b93ca88a41d0e32a573dd7165ae84a42b3c08ae  internal/gatestore/words.go
8eaf661b6d8208ef58b7474cf66cea4b3eff19d6  internal/gatestore/words_test.go
```

## The list

```go
var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן"}
var noWords = []string{"no", "deny", "n", "stop", "לא"}
```

`כן` is U+05DB U+05DF and `לא` is U+05DC U+05D0. The shared reader's signature is `func BareVerb(word string) (verb string, ok bool)` (`internal/gatestore/words.go:8-21`).

## The rule as built

`ParseBare` trims with `strings.TrimSpace`. A mark is any rune for which `unicode.IsPunct` is true: this includes `. ! ? , ; : … - ) ( " ' * _ / \ # % & @`, full-width `！ ？ 。 、`, Hebrew `׃ ־ ׳ ״`, and `؟ ¡ » ’ ” — ·`, but excludes `+ $ ~ ^ | = < >`, the backquote, `✓`, and emoji. It removes one trailing mark, but it does not remove a second mark. Whitespace before the mark is trimmed after the mark is removed. Trimmable space includes space, tab, LF, CR, VT, U+0085, U+00A0, U+2028, and U+3000; it excludes U+200B, U+200D, U+200E, U+200F, U+FEFF, and NUL. It then folds case with `strings.ToLower`, and the rest must exactly equal one list word. A CR or LF remaining inside the line binds nothing. A yes word yields `approve`, and a no word yields `deny` (`internal/gatestore/plain.go:555-568`).

| Case | Result | Test |
|---|---|---|
| `ok?` | binds `approve` | `TestParseBareBindsEachWordAloneInAnyCaseWithOneMark` |
| `ok??` and `ok?!` | bind nothing | `TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine` |
| `oK` (`ok` with U+212A Kelvin sign) | binds `approve` | `TestParseBareBindsEachWordAloneInAnyCaseWithOneMark` |
| Hebrew word with niqqud | binds nothing | `TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine` |
| `go ahead` with exactly one U+0020 | binds; two spaces, tab, and U+00A0 bind nothing | `TestParseBareBindsEachWordAloneInAnyCaseWithOneMark`, `TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine` |
| emoji alone or after a word | binds nothing | `TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine` |
| word beside U+200F | binds nothing | `TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine` |

## The plan's ruling and the A0 note on "ok?"

The plan's ruling 4 lists `"ok?"` among the lines that bind nothing, while the A0 note's D3 says `"ok?" followed by more words`; the A0 note's text is the ruled one, and `ok?` binds.

## The relay

`answerVerbs` is gone, and `answerForm` asks `BareVerb` for the whole normalized line, its first field, and its first two fields (`internal/buzz/relay.go:783-793`):

```go
if _, ok := gatestore.BareVerb(strings.Join(w, " ")); ok {
	return true
}
wordFields := 0
if _, ok := gatestore.BareVerb(w[0]); ok {
	wordFields = 1
} else if len(w) >= 2 {
	if _, ok := gatestore.BareVerb(strings.Join(w[:2], " ")); ok {
		wordFields = 2
	}
}
```

The relay's refusal is wider than the parser because it removes every punctuation mark and symbol from both ends of each field. A lead reply containing a line that is only `Ok.`, `- stop`, or `**Go ahead**` is therefore refused. For every word in the list, the refusal is at least as wide as it was for the original four words (`internal/buzz/words330_test.go:13-46`).

## The handler and the team door

a bare `stop` or `ok` sent to the squad outside a request's thread is a gate answer from A1-1 on; with no gate open it is refused and reaches no lead. In a gate's thread, every word in the list is that gate's answer through `internal/buzz/inbound.go:557-559`, `internal/buzz/inbound.go:642-653`, `internal/gateverify/core.go:172-183`, and `internal/gateverify/verify.go:149-158`; `TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer` asserts this. `ok`, `y`, `go ahead`, or `כן` replied to the lead's plan post confirms the team as `yes` does, while a no word confirms nothing through `internal/buzz/inbound.go:249-259` and the bare-yes case of the witness's switch, `internal/cli/lead_start.go:493-519`; `TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord` and `TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost` assert it. The witness test runs `witnessBuzz` but does not run the attachment that hands the bytes to the lead over its socket with a real pi as the peer (`internal/cli/words330_test.go:34-62`).

## Known differences

One of the four earlier words with two or more trailing marks, `yes!!`, gave its verb before this change and binds nothing now (the one-mark rule; `TestParseBare` asserts it).

the relay's refusal of a decorated line of a word D3 adds was ruled on its shape; no relay record existed on the lead's host to count from.

Added by the lead: run by the lead on 2026-11-12, on its own host: a search of the home directory for any directory named `extensions/amq-squad/relay` (`find <home> -type d -path '*extensions/amq-squad/relay' -not -path '*/Library/*' -not -path '*/node_modules/*'`) printed nothing; the same search under the squad's state folders to depth 8 printed nothing; in the project's twelve session folders no `relay` directory exists and no message file names a `cockpit/q-` thread. So no lead had had a phone request relayed from a root on that host, or none was kept.

A format character (Cf) beside a word binds nothing, until a measurement of the owner's client says one is sent.

Niqqud binds nothing at the parser while the relay, which normalises, refuses such a lead line.

The texts the squad sends still say "yes or no".

A line that binds nothing is not re-asked by this task.

Statement 12 of the A0 note is covered by a control only (C12).

The guard test does not see a list of only `ok`, `y`, `n`, `stop`, `approve`, `deny`, a regular expression, or a file that is not Go.

No test asserts that a channel yet to be written reads the list.

two helper types of the tests carry no comment: `decidedWordCase`, `bareWordCase330`.

the guard's allowed place is a map entry with its reason in the test's comment, not in the list.

## For the 3.3.0 notes

a bare `stop` or `ok` sent to the squad outside a request's thread is a gate answer from A1-1 on; with no gate open it is refused and reaches no lead.

No 3.3.0 notes file exists on this tree. `ls docs | grep -i -E '3\.3|330'` printed nothing and exited 1.

## The tests

- `TestParseBare` preserves the earlier parser contract and now asserts that `yes!!` binds nothing.
- `TestBareWordsAreTheDecidedList` fixes the eleven words, their order, and their normalized form.
- `TestParseBareBindsEachWordAloneInAnyCaseWithOneMark` exercises every word across case, surrounding space, and one-mark forms.
- `TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine` fixes the two-mark boundary and the decided exclusions.
- `TestNoOtherFileSpellsAYesNoList` scans production Go for a second bare-answer list.
- `TestRelayRefusesGateShapedLines` preserves the relay's established answer-shape refusal behavior.
- `TestRelayRefusesALeadLineOfEachWordOfTheList` proves the relay reads every decided word and preserves the wider refusal.
- `TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead` proves each bare word is handled before phone routing.
- `TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer` proves each yes and no word binds to the question it replies to.
- `TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord` proves plan-post confirmation follows only the list's approve verb.
- `TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost` proves the independent lead witness accepts yes words and rejects no words and out-of-rule lines.

## Rows

### R0

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R0 ===
edit: none (your files)
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 0
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 0):
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 0
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 5):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.12s)
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.04s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R1

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R1 ===
$ python3 rows_edit.py R1
edit: internal/gatestore/words.go:5
  before: var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן"}
  after:  var yesWords = []string{"yes", "approve", "y", "go ahead", "כן"}
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- FAIL: TestBareWordsAreTheDecidedList
--- FAIL: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 28):
    words_test.go:22: bare words: yes []string{"yes", "approve", "y", "go ahead", "כן"} no []string{"no", "deny", "n", "stop", "לא"}, want yes []string{"yes", "approve", "y", "ok", "go ahead", "כן"} no []string{"no", "deny", "n", "stop", "לא"}
    words_test.go:47: ParseBare("ok") = "" false, want "approve"
    words_test.go:47: ParseBare("OK") = "" false, want "approve"
    words_test.go:47: ParseBare("Ok") = "" false, want "approve"
    words_test.go:47: ParseBare(" ok") = "" false, want "approve"
    words_test.go:47: ParseBare("ok ") = "" false, want "approve"
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- FAIL: TestRelayRefusesALeadLineOfEachWordOfTheList
--- FAIL: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- FAIL: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 1 fail 4 skip 0
messages (the texts are your tests'; the first 6 lines of 13):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.12s)
    words330_test.go:29: "ok": relayed [{RequestID:q-0a4cdf4babeb ReplyID:r-016 Kind:answer Outcome:posted Reason: Events:[c17250b6f39a3838d0541cc550afef63bb1d44d2e39e8dfd4e6854d60045ba8f] Error:}], posts 1 want 0
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 12):
    words330_test.go:20: "ok": {Outcome:refused Digest: Refusal:event_unverified Detail:event 49b9361f12b3 is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:5b830567caf57d30a68f11957ded88dd39f0ecefff1da521791a48d56
    words330_test.go:20: "OK.": {Outcome:refused Digest: Refusal:event_unverified Detail:event d562e086151d is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:5f4c55616472a5eb42b683308989963a0716a2bfa253a945262df5c0
    --- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.05s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R2

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R2 ===
$ python3 rows_edit.py R2
edit: internal/gatestore/words.go:5
  before: var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן"}
  after:  var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן", "sure"}
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- FAIL: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- FAIL: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 2):
    words_test.go:22: bare words: yes []string{"yes", "approve", "y", "ok", "go ahead", "כן", "sure"} no []string{"no", "deny", "n", "stop", "לא"}, want yes []string{"yes", "approve", "y", "ok", "go ahead", "כן"} no []string{"no", "deny", "n", "stop", "לא"}
    words_test.go:84: ParseBare("sure") = "approve", want no binding
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- FAIL: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 4 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 6):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.15s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.11s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.11s)
    words330_test.go:43: "sure": relayed [{RequestID:q-f3645075fdcb ReplyID:r-067 Kind:answer Outcome:refused Reason:relay_refused_gate_shape Events:[] Error:}], posts 5 want 6
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.04s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R3

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R3 ===
$ python3 rows_edit.py R3
edit: internal/gatestore/words.go:6
  before: var noWords = []string{"no", "deny", "n", "stop", "לא"}
  after:  var noWords = []string{"no", "deny", "n", "לא"}
edit: internal/gatestore/words.go:5
  before: var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן"}
  after:  var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן", "stop"}
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- FAIL: TestBareWordsAreTheDecidedList
--- FAIL: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 25):
    words_test.go:22: bare words: yes []string{"yes", "approve", "y", "ok", "go ahead", "כן", "stop"} no []string{"no", "deny", "n", "לא"}, want yes []string{"yes", "approve", "y", "ok", "go ahead", "כן"} no []string{"no", "deny", "n", "stop", "לא"}
    words_test.go:47: ParseBare("stop") = "approve" true, want "deny"
    words_test.go:47: ParseBare("STOP") = "approve" true, want "deny"
    words_test.go:47: ParseBare("Stop") = "approve" true, want "deny"
    words_test.go:47: ParseBare(" stop") = "approve" true, want "deny"
    words_test.go:47: ParseBare("stop ") = "approve" true, want "deny"
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- FAIL: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 7):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.11s)
    words330_test.go:81: "stop": reply "accepted rel-list tag" (1), handled [{MessageID:2026-10-28T12-00-01.000Z_pid1_00000001 EventID:475ba706df245ea7654b2683ba9526d922e4ce908c93d6a453aae67add295984 RequestID: Kind:gate Gate:0x264e569e700 Delivery:<nil> Status:[verifying event 475ba706df24] Reply:a
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 11):
    words330_test.go:28: "stop": {Outcome:witnessed Digest:sha256:5bfe026f77ba6f7d51453e304550b9f531e46af550900ed86034b19a3b5f067d@absent Refusal: Detail:witnessed by the lead (pid 48261); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.05s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R4

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R4 ===
$ python3 rows_edit.py R4
edit: internal/gatestore/plain.go:559
  before: func ParseBare(content string) (string, bool) {
  after:  func parseBareA11Row(content string) (string, bool) {
new file: internal/gatestore/zz_a11_row.go, its ParseBare:
  func ParseBare(content string) (string, bool) {
      if strings.TrimSpace(content) == "sure" {
          return "approve", true
      }
      return parseBareA11Row(content)
  }
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- FAIL: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 4 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 1):
    words_test.go:84: ParseBare("sure") = "approve", want no binding
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 0
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 5):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.10s)
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.04s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R5

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R5 ===
$ python3 rows_edit.py R5
new file: internal/buzz/zz_a11_row.go:
  var a11RowWords = []string{"yes", "no"}
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- FAIL: TestNoOtherFileSpellsAYesNoList
pass 4 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 1):
    words_test.go:145: bare-answer spellings outside words.go: got map[string]map[string]int{"internal/buzz/zz_a11_row.go":map[string]int{"no":1, "yes":1}, "internal/cli/worktree.go":map[string]int{"yes":3}}, want map[string]map[string]int{"internal/cli/worktree.go":map[string]int{"yes":3}}
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 0
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 5):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.12s)
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.03s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R6

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R6 ===
$ python3 rows_edit.py R6
edit: internal/gatestore/plain.go:559
  before: func ParseBare(content string) (string, bool) {
  after:  func parseBareA11Row(content string) (string, bool) {
new file: internal/gatestore/zz_a11_row.go, its ParseBare:
  func ParseBare(content string) (string, bool) {
      return parseBareA11Row(strings.TrimRightFunc(strings.TrimSpace(content), unicode.IsPunct))
  }
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- FAIL: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- FAIL: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 122):
    plain311_test.go:484: ParseBare("yes!!") = "approve"
    words_test.go:73: ParseBare("yes!!") = "approve", want no binding
    words_test.go:73: ParseBare("yes??") = "approve", want no binding
    words_test.go:73: ParseBare("yes..") = "approve", want no binding
    words_test.go:73: ParseBare("yes...") = "approve", want no binding
    words_test.go:73: ParseBare("yes?!") = "approve", want no binding
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- FAIL: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 7):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.12s)
    words330_test.go:66: "ok!!": reply "refused - not_in_gate_thread" (1), handled [{MessageID:2026-10-28T12-00-01.000Z_pid1_00000001 EventID:654dc25566f3203415ed8ee7d44f1025fabad4dd3d70664e6933349e8ac64244 RequestID: Kind:gate Gate:0x435ef5591ce0 Delivery:<nil> Status:[verifying event 654dc25566f3]
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 11):
    words330_test.go:28: "ok!!": {Outcome:witnessed Digest:sha256:f4c4b8dfac473e1268e10cbd110683efde80b2b3b0629bdfa4f937a49a7837c0@absent Refusal: Detail:witnessed by the lead (pid 48670); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.05s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R7

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R7 ===
$ python3 rows_edit.py R7
edit: internal/gatestore/plain.go:559
  before: func ParseBare(content string) (string, bool) {
  after:  func parseBareA11Row(content string) (string, bool) {
new file: internal/gatestore/zz_a11_row.go, its ParseBare:
  func ParseBare(content string) (string, bool) {
      if head, _, ok := strings.Cut(content, ", "); ok {
          content = head
      }
      return parseBareA11Row(content)
  }
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- FAIL: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 4 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 22):
    words_test.go:73: ParseBare("yes, but") = "approve", want no binding
    words_test.go:73: ParseBare("yes, but…") = "approve", want no binding
    words_test.go:73: ParseBare("approve, but") = "approve", want no binding
    words_test.go:73: ParseBare("approve, but…") = "approve", want no binding
    words_test.go:73: ParseBare("y, but") = "approve", want no binding
    words_test.go:73: ParseBare("y, but…") = "approve", want no binding
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- FAIL: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 7):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.11s)
    words330_test.go:66: "stop, the build is red": reply "refused - not_in_gate_thread" (1), handled [{MessageID:2026-10-28T12-00-01.000Z_pid1_00000001 EventID:506f129b6a81a1396846d859ee1c738f1afd7af471e6145c63659f4ff63c6343 RequestID: Kind:gate Gate:0x72816da1ebd0 Delivery:<nil> Status:[verifying e
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 11):
    words330_test.go:28: "ok, but": {Outcome:witnessed Digest:sha256:e7d1476b5f6c41e8f8bb379fd224c17034d668d4536049157b48e902bc43cf54@absent Refusal: Detail:witnessed by the lead (pid 48864); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.06s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R8

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R8 ===
$ python3 rows_edit.py R8
edit: internal/gatestore/plain.go:559
  before: func ParseBare(content string) (string, bool) {
  after:  func parseBareA11Row(content string) (string, bool) {
new file: internal/gatestore/zz_a11_row.go, its ParseBare:
  func ParseBare(content string) (string, bool) {
      if content != strings.ToLower(content) {
          return "", false
      }
      return parseBareA11Row(content)
  }
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- FAIL: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- FAIL: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 50):
    plain311_test.go:479: ParseBare(" YES!\n") = "" false, want "approve"
    plain311_test.go:479: ParseBare("No.") = "" false, want "deny"
    plain311_test.go:479: ParseBare("Yes.") = "" false, want "approve"
    words_test.go:47: ParseBare("YES") = "" false, want "approve"
    words_test.go:47: ParseBare("Yes") = "" false, want "approve"
    words_test.go:47: ParseBare("YES!") = "" false, want "approve"
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 0
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 5):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.12s)
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 11):
    words330_test.go:20: "OK.": {Outcome:refused Digest: Refusal:event_unverified Detail:event a0b4e8dea3ca is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:0c206392623407f395df159c5cc9c732339b3bd9ac063a862980cdb2
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.05s)
    --- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.03s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R9

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R9 ===
$ python3 rows_edit.py R9
edit: internal/gatestore/plain.go:559
  before: func ParseBare(content string) (string, bool) {
  after:  func parseBareA11Row(content string) (string, bool) {
new file: internal/gatestore/zz_a11_row.go, its ParseBare:
  func ParseBare(content string) (string, bool) {
      if content != strings.TrimSpace(content) {
          return "", false
      }
      return parseBareA11Row(content)
  }
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- FAIL: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- FAIL: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 89):
    plain311_test.go:479: ParseBare(" YES!\n") = "" false, want "approve"
    words_test.go:47: ParseBare(" yes") = "" false, want "approve"
    words_test.go:47: ParseBare("yes ") = "" false, want "approve"
    words_test.go:47: ParseBare("\tyes\t") = "" false, want "approve"
    words_test.go:47: ParseBare("yes\n") = "" false, want "approve"
    words_test.go:47: ParseBare("\r\nyes\r\n") = "" false, want "approve"
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 0
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 5):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.12s)
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.04s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R10

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R10 ===
$ python3 rows_edit.py R10
edit: internal/gatestore/plain.go:559
  before: func ParseBare(content string) (string, bool) {
  after:  func parseBareA11Row(content string) (string, bool) {
new file: internal/gatestore/zz_a11_row.go, its ParseBare:
  func ParseBare(content string) (string, bool) {
      if verb, ok := parseBareA11Row(content); ok {
          return verb, true
      }
      if f := strings.Fields(content); len(f) > 1 {
          return parseBareA11Row(f[0])
      }
      return "", false
  }
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- FAIL: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- FAIL: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 158):
    plain311_test.go:484: ParseBare("yes please") = "approve"
    plain311_test.go:484: ParseBare("yes\nno") = "approve"
    plain311_test.go:484: ParseBare("yes aaaaaaaaaaaaaaaa") = "approve"
    words_test.go:73: ParseBare("yes! !") = "approve", want no binding
    words_test.go:73: ParseBare("yes. .") = "approve", want no binding
    words_test.go:73: ParseBare("yes !!") = "approve", want no binding
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- FAIL: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 7):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.11s)
    words330_test.go:66: "stop, the build is red": reply "refused - not_in_gate_thread" (1), handled [{MessageID:2026-10-28T12-00-01.000Z_pid1_00000001 EventID:0dda8c017ac122e3848f58fe9bc121554139ab297cf2adc9496a63eaa37f8f83 RequestID: Kind:gate Gate:0x2f0fbdd8000 Delivery:<nil> Status:[verifying ev
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 11):
    words330_test.go:28: "ok, but": {Outcome:witnessed Digest:sha256:f0734833deeb7ec33433912117556f634ec225a6011916050cc6a05802853762@absent Refusal: Detail:witnessed by the lead (pid 49298); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.04s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R11

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R11 ===
edit: internal/gatestore/plain.go is the TIP's (7542de96b745bb23f0465539e76f0de1f6cfe396), whole: ParseBare as it was
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- FAIL: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- FAIL: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- FAIL: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- FAIL: TestNoOtherFileSpellsAYesNoList
pass 1 fail 4 skip 0
messages (the texts are your tests'; the first 6 lines of 209):
    plain311_test.go:484: ParseBare("yes!!") = "approve"
    words_test.go:47: ParseBare("y") = "" false, want "approve"
    words_test.go:47: ParseBare("Y") = "" false, want "approve"
    words_test.go:47: ParseBare("Y") = "" false, want "approve"
    words_test.go:47: ParseBare(" y") = "" false, want "approve"
    words_test.go:47: ParseBare("y ") = "" false, want "approve"
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- FAIL: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- FAIL: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 2 fail 3 skip 0
messages (the texts are your tests'; the first 6 lines of 23):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.09s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.11s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.10s)
    words330_test.go:57: "y": reply "accepted q-1f5bfbc3269c routed lead-a" (1), handled [{MessageID:2026-10-28T12-00-01.000Z_pid1_00000001 EventID:1f5bfbc3269c7124a12f0aa32e0c5466ddd481aa041baef4c611e07351b6f90d RequestID:q-1f5bfbc3269c Kind:route Gate:<nil> Delivery:0x31a375a5c510 Status:[verifyin
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 15):
    words330_test.go:20: "ok": {Outcome:refused Digest: Refusal:event_unverified Detail:event 2f82265625ed is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:be019580e5b397f20f14a7894305d32b9f11c1860e5b1b59c46701730
    words330_test.go:20: "y": {Outcome:refused Digest: Refusal:event_unverified Detail:event 4dc4451ec069 is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:679015a7eab728c24277df4bb7a314829fb04faf74ffc8d9281ea0e836
    words330_test.go:20: "go ahead": {Outcome:refused Digest: Refusal:event_unverified Detail:event 318a7904250e is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:615968358556dcfc7931e67e1c7f9522b703d246975eee0cd93
    words330_test.go:20: "כן": {Outcome:refused Digest: Refusal:event_unverified Detail:event 0998423d70ed is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:3f95bce031aae3b9da755e0c333a911907250c7861b3729b269ebee5a
    words330_test.go:20: "OK.": {Outcome:refused Digest: Refusal:event_unverified Detail:event bb3f2410a832 is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:cc4b0f6585d72248d0d17f0cc42997da8d5515026d892c5f7b0fc163
    --- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.05s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R12

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R12 ===
$ python3 rows_edit.py R12
edit: internal/gatestore/plain.go:559
  before: func ParseBare(content string) (string, bool) {
  after:  func parseBareA11Row(content string) (string, bool) {
new file: internal/gatestore/zz_a11_row.go, its ParseBare:
  func ParseBare(content string) (string, bool) {
      if r, _ := utf8.DecodeLastRuneInString(strings.TrimSpace(content)); unicode.IsPunct(r) {
          return "", false
      }
      return parseBareA11Row(content)
  }
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- FAIL: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- FAIL: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 170):
    plain311_test.go:479: ParseBare("no ?") = "" false, want "deny"
    plain311_test.go:479: ParseBare("Yes.") = "" false, want "approve"
    plain311_test.go:479: ParseBare(" YES!\n") = "" false, want "approve"
    plain311_test.go:479: ParseBare("No.") = "" false, want "deny"
    words_test.go:47: ParseBare("YES!") = "" false, want "approve"
    words_test.go:47: ParseBare("Yes.") = "" false, want "approve"
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 0
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 5):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.11s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.11s)
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 11):
    words330_test.go:20: "OK.": {Outcome:refused Digest: Refusal:event_unverified Detail:event abef12153c53 is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:7736d9946a335314f42feefc5e716d409796827199d23a278e823398
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.06s)
    --- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.03s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R13

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R13 ===
$ python3 rows_edit.py R13
edit: internal/gatestore/words.go:5
  before: var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן"}
  after:  var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "לא"}
edit: internal/gatestore/words.go:6
  before: var noWords = []string{"no", "deny", "n", "stop", "לא"}
  after:  var noWords = []string{"no", "deny", "n", "stop", "כן"}
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- FAIL: TestBareWordsAreTheDecidedList
--- FAIL: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 49):
    words_test.go:22: bare words: yes []string{"yes", "approve", "y", "ok", "go ahead", "לא"} no []string{"no", "deny", "n", "stop", "כן"}, want yes []string{"yes", "approve", "y", "ok", "go ahead", "כן"} no []string{"no", "deny", "n", "stop", "לא"}
    words_test.go:47: ParseBare("כן") = "deny" true, want "approve"
    words_test.go:47: ParseBare("כן") = "deny" true, want "approve"
    words_test.go:47: ParseBare("כן") = "deny" true, want "approve"
    words_test.go:47: ParseBare(" כן") = "deny" true, want "approve"
    words_test.go:47: ParseBare("כן ") = "deny" true, want "approve"
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- FAIL: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 9):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.11s)
    words330_test.go:81: "כן": reply "accepted rel-list tag" (1), handled [{MessageID:2026-10-28T12-00-01.000Z_pid1_00000001 EventID:6853df64aa1e9161a03fa3393dbfe64a7763fc27a90eae16c3c013089b159909 RequestID: Kind:gate Gate:0x320db6908540 Delivery:<nil> Status:[verifying event 6853df64aa1e] Reply:ac
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 12):
    words330_test.go:20: "כן": {Outcome:refused Digest: Refusal:event_unverified Detail:event 5adf18253d8b is not a bare yes replied to the plan post (or yes <16 hex>) Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want witnessed sha256:a6c63642b59a58b09a6f318d90cbff78ca792c12b46e088e94765535a
    words330_test.go:28: "לא": {Outcome:witnessed Digest:sha256:5addc1d2b0f0241a33947efe03726ea9c5e9dc449524d4d0a43adde94ba78965@absent Refusal: Detail:witnessed by the lead (pid 50043); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.04s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.04s)
    --- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.03s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R14

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R14 ===
edit: internal/buzz/relay.go is the TIP's (7542de96b745bb23f0465539e76f0de1f6cfe396), whole: the relay keeps its own four words
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 1
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- FAIL: TestNoOtherFileSpellsAYesNoList
pass 4 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 1):
    words_test.go:145: bare-answer spellings outside words.go: got map[string]map[string]int{"internal/buzz/relay.go":map[string]int{"no":1, "yes":1}, "internal/cli/worktree.go":map[string]int{"yes":3}}, want map[string]map[string]int{"internal/cli/worktree.go":map[string]int{"yes":3}}
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- FAIL: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 4 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 43):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.12s)
    words330_test.go:29: "y": relayed [{RequestID:q-1c990f7204d9 ReplyID:r-011 Kind:answer Outcome:posted Reason: Events:[8cf11694a98a227eab7a1345b2d3704b0857528b9389c0ba4aad971ce6a9db4d] Error:}], posts 1 want 0
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.03s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R15

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R15 ===
$ python3 rows_edit.py R15
edit: internal/buzz/inbound.go:648
  before: if _, ok := gatestore.ParseBare(content); ok {
  after:  if verb, ok := gatestore.ParseBare(content); ok && verb == "" {
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 0
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 0):
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- FAIL: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- FAIL: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 3 fail 2 skip 0
messages (the texts are your tests'; the first 6 lines of 23):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.13s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.10s)
    words330_test.go:57: "yes": reply "accepted q-dfbfe9153859 routed lead-a" (1), handled [{MessageID:2026-10-28T12-00-01.000Z_pid1_00000001 EventID:dfbfe91538598a6306501f3fd5243d6362d694998489f34210b618f4a3bb8e7f RequestID:q-dfbfe9153859 Kind:route Gate:<nil> Delivery:0x7611937fc1b0 Status:[verify
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.04s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R16

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R16 ===
$ python3 rows_edit.py R16
edit: internal/buzz/inbound.go:253
  before: if verb, ok := gatestore.ParseBare(content); ok && verb == "approve" && h.Confirm != nil {
  after:  if verb, ok := gatestore.ParseBare(content); ok && verb != "" && h.Confirm != nil {
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 0
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 0):
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 1
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- FAIL: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 4 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.14s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.08s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.11s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.10s)
    words330_test.go:116: "no" confirmed the team: reply "confirmed team" calls []string{"no"}
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 0
--- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 1 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 10):
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-3 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-4 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/no-0 (0.04s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### R17

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row R17 ===
$ python3 rows_edit.py R17
edit: internal/cli/lead_start.go:493
  before: case bare && verb == "approve" && req.Post != nil:
  after:  case bare && verb != "" && req.Post != nil:
$ go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'
exit: 0
--- PASS: TestParseBare
--- PASS: TestBareWordsAreTheDecidedList
--- PASS: TestParseBareBindsEachWordAloneInAnyCaseWithOneMark
--- PASS: TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine
--- PASS: TestNoOtherFileSpellsAYesNoList
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 0):
$ go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'
exit: 0
--- PASS: TestRelayRefusesGateShapedLines
--- PASS: TestRelayRefusesALeadLineOfEachWordOfTheList
--- PASS: TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead
--- PASS: TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer
--- PASS: TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord
pass 5 fail 0 skip 0
messages (the texts are your tests'; the first 6 lines of 5):
    --- PASS: TestRelayRefusesGateShapedLines/whole-line_answer_forms (0.15s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_id (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/an_open_gate's_question_text (0.07s)
    --- PASS: TestRelayRefusesGateShapedLines/sentences_with_the_word_post_unchanged (0.12s)
    --- PASS: TestRelayRefusesGateShapedLines/never_into_a_gate_question's_thread (0.11s)
$ go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'
exit: 1
--- FAIL: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost
pass 0 fail 1 skip 0
messages (the texts are your tests'; the first 6 lines of 13):
    words330_test.go:28: "stop": {Outcome:witnessed Digest:sha256:dbd310fc88f26c4564b575fc188178980d3df3eea1415e57c0a82390b0391616@absent Refusal: Detail:witnessed by the lead (pid 50761); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    words330_test.go:28: "n": {Outcome:witnessed Digest:sha256:4f1da00190f38a8cfe814e86868239d6c38c24598abc83c55f5ef617af928991@absent Refusal: Detail:witnessed by the lead (pid 50761); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    words330_test.go:28: "לא": {Outcome:witnessed Digest:sha256:88b3eb825e11d98647395b60a8443f5ca02e49fc73835cc434cdfafc6afc8a23@absent Refusal: Detail:witnessed by the lead (pid 50761); it stands for 30m0s Steps:[] Roster:[] OwnerSteps:[] Team:<nil> Proof:}, want event_unverified
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-0 (0.06s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-1 (0.05s)
    --- PASS: TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost/yes-2 (0.05s)
restore: internal/gatestore/words.go cmp exit 0
restore: internal/gatestore/plain.go cmp exit 0
restore: internal/buzz/relay.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: internal/cli/lead_start.go cmp exit 0
restore: added files left: 0
restore: git status as before the row: cmp exit 0
```

### C1b

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row C1b ===
edit: none (your tree); an existing test of internal/gateverify
$ go test ./internal/gateverify/ -count=1 -timeout 240s -v -run '^TestABareYesOrNoInTheThreadIsTheAnswer$'
exit: 0
--- PASS: TestABareYesOrNoInTheThreadIsTheAnswer
pass 1 fail 0 skip 0
```

### C12

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all`

```text
=== row C12 ===
edit: none (your tree); an existing test of internal/gateverify
$ go test ./internal/gateverify/ -count=1 -timeout 240s -v -run '^TestFourClassesThroughTheCore$'
exit: 0
--- PASS: TestFourClassesThroughTheCore
pass 1 fail 0 skip 0
keep directory removed: yes
```


## What ran

`git status --porcelain --untracked-files=all` exited 0 and printed nothing before the carry.

`git rev-parse HEAD` exited 0 and printed:

```text
2d52b79dc133d070e5e45c91eff7371f999055bc
```

`git diff --stat 2d52b79dc133d070e5e45c91eff7371f999055bc HEAD -- internal/gatestore internal/gateverify/core.go internal/gateverify/verify.go internal/gateverify/bare311_test.go internal/gateverify/core_test.go internal/buzz/relay.go internal/buzz/inbound.go internal/buzz/seen.go internal/buzz/phone_test.go internal/buzz/plain311_test.go internal/buzz/serve_test.go internal/buzz/words330_test.go internal/cli/lead_start.go internal/cli/plain311_test.go internal/cli/words330_test.go internal/cli/worktree.go docs/v3-spike/330-a0-sender-auth docs/v3-spike/330-a1-1-words` exited 0 and printed nothing.

`git apply - < ../../../../amq-squad-lead/T/a11/inputs-carry/carry/t186.patch` exited 0 and printed nothing.

`git hash-object internal/buzz/relay.go internal/buzz/words330_test.go internal/cli/words330_test.go internal/gatestore/plain.go internal/gatestore/plain311_test.go internal/gatestore/words.go internal/gatestore/words_test.go` exited 0 and initially printed, in the same order as `blobs-after.txt`:

```text
8087b52e39817bd3a735bf24b65148d6245b4b08
ef151cf3977ffd695ef87070b656205cc814b641
1ad7c22579edf12539150b584fcca9b8ad6c80df
872e77d4765fa9236bcae4cc2820cc8acbd0d22d
c19e3d1bbb16e67f0be7bb60aeb68dff1c4e1cca
4b93ca88a41d0e32a573dd7165ae84a42b3c08ae
8eaf661b6d8208ef58b7474cf66cea4b3eff19d6
```

`go build ./...` was run twice: the first run exited 1 (the sandbox refused the host Go build cache); the second exited 0 and printed nothing.

`go vet ./internal/gatestore/ ./internal/buzz/ ./internal/cli/` was run twice: the first run exited 1 (the sandbox refused the host Go build cache); the second exited 0 and printed nothing.

`gofmt -l internal/gatestore internal/buzz internal/cli` exited 0 and printed nothing.

`go test ./internal/gatestore/ -count=1 -timeout 120s -v -run '^(TestParseBare|TestBareWordsAreTheDecidedList|TestParseBareBindsEachWordAloneInAnyCaseWithOneMark|TestParseBareBindsNothingForTwoMarksOrAnythingElseOnTheLine|TestNoOtherFileSpellsAYesNoList)$'` exited 0; its last line was `ok  	github.com/omriariav/amq-squad/v3/internal/gatestore	0.613s`.

`go test ./internal/buzz/ -count=1 -timeout 240s -v -run '^(TestRelayRefusesGateShapedLines|TestRelayRefusesALeadLineOfEachWordOfTheList|TestABareWordOfTheListWithNoGateOpenIsRefusedAndReachesNoLead|TestABareWordOfTheListInAGatesThreadIsThatGatesAnswer|TestTheTeamDoorTakesAYesWordOfTheListAndNoNoWord)$'` exited 0; its last line was `ok  	github.com/omriariav/amq-squad/v3/internal/buzz	2.976s`.

`go test ./internal/cli/ -count=1 -timeout 240s -v -run '^(TestTheLeadWitnessesAYesWordOfTheListRepliedToThePlanPost)$'` exited 0; its last line was `ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.885s`.

`go test ./internal/gateverify/ -count=1 -timeout 240s -v -run '^TestABareYesOrNoInTheThreadIsTheAnswer$'` exited 0; its last line was `ok  	github.com/omriariav/amq-squad/v3/internal/gateverify	1.070s`.

`go test ./internal/gateverify/ -count=1 -timeout 240s -v -run '^TestFourClassesThroughTheCore$'` exited 0; its last line was `ok  	github.com/omriariav/amq-squad/v3/internal/gateverify	0.871s`.

`grep -cE '"(yes|no)"' internal/gatestore/plain.go internal/buzz/relay.go` printed `0` for each file and exited 1.

`grep -c 'answerVerbs' internal/buzz/relay.go` printed `0` and exited 1.

`git diff --stat -- internal/buzz/inbound.go internal/buzz/seen.go internal/cli/lead_start.go` exited 0 and printed nothing.

`ls docs | grep -i -E '3\.3|330'` exited 1 and printed nothing.

`bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all` was run twice and exited 0 both times, with the 20 rows and `keep directory removed: yes`. The rows of this note are the whole output of the second run, `bash -c 'bash ../../../../amq-squad-lead/T/a11/inputs-carry/rows-a11.sh all' > <tmp>/t188-rows-all.log`. The path of that file was replaced by `<tmp>` by the lead on 2026-11-13.

`go run ./scripts/redact-evidence -check docs/v3-spike/330-a1-1-words/` exited 0 and printed nothing.

`go run ./scripts/redact-evidence -check docs/v3-spike/330-a0-sender-auth/` exited 0 and printed nothing.

`git diff --check` exited 0 and printed nothing.

`git diff --no-index --check /dev/null internal/gatestore/words.go` printed nothing and exited 1.

`git diff --no-index --check /dev/null internal/gatestore/words_test.go` printed nothing and exited 1.

`git diff --no-index --check /dev/null internal/buzz/words330_test.go` printed nothing and exited 1.

`git diff --no-index --check /dev/null internal/cli/words330_test.go` printed nothing and exited 1.

`git diff --no-index --check /dev/null docs/v3-spike/330-a1-1-words/README.md` printed nothing and exited 1.

`git diff --numstat -- docs/v3-spike/330-a0-sender-auth/README.md` printed 13 added lines, 0 removed lines, and the path.

`git diff -U0 -- docs/v3-spike/330-a0-sender-auth/README.md | grep -c '^@@'` printed `1` and exited 0.

`grep -c '/Us''ers/' docs/v3-spike/330-a1-1-words/README.md docs/v3-spike/330-a0-sender-auth/README.md` printed `0` for each file and exited 1.

`grep -cE '[[:space:]]+$' docs/v3-spike/330-a1-1-words/README.md docs/v3-spike/330-a0-sender-auth/README.md` printed `0` for each file and exited 1.

`git status --porcelain --untracked-files=all` printed the nine declared paths and exited 0.

`git diff --stat -- internal/gatestore/plain.go internal/gatestore/plain311_test.go internal/buzz/relay.go docs/v3-spike/330-a0-sender-auth/README.md` printed only those four paths and exited 0.

No package was run whole by the seat: the whole runs are the lead's, on the PR head. The final command was the seven-file `git hash-object` command above, and it printed the same seven hashes again.

### Added by the lead on 2026-11-13 (runs on the merged commits)

Every line of this sub-section is the lead's, run in a worktree of v3 at `f9251f8cead3c42315203892e8f93990badc2e24`. `2d52b79dc133d070e5e45c91eff7371f999055bc` is the dispatch TIP and `188e04f7ffb6c23f1100410f3ce76cf7e9137bbf` the commit that holds this note.

The entry above for `git diff --numstat` gives no exit code. On the two commits:

`git diff --numstat 2d52b79dc133d070e5e45c91eff7371f999055bc 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf -- docs/v3-spike/330-a0-sender-auth/README.md`

```text
13	0	docs/v3-spike/330-a0-sender-auth/README.md
```

It exited 0.

The last command of the list, the seven-file `git hash-object`, is not printed a second time above. The seven blobs of the commit that holds this note, in the order of `blobs-after.txt`:

`git rev-parse 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf:internal/buzz/relay.go 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf:internal/buzz/words330_test.go 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf:internal/cli/words330_test.go 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf:internal/gatestore/plain.go 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf:internal/gatestore/plain311_test.go 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf:internal/gatestore/words.go 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf:internal/gatestore/words_test.go`

```text
8087b52e39817bd3a735bf24b65148d6245b4b08
ef151cf3977ffd695ef87070b656205cc814b641
1ad7c22579edf12539150b584fcca9b8ad6c80df
872e77d4765fa9236bcae4cc2820cc8acbd0d22d
c19e3d1bbb16e67f0be7bb60aeb68dff1c4e1cca
4b93ca88a41d0e32a573dd7165ae84a42b3c08ae
8eaf661b6d8208ef58b7474cf66cea4b3eff19d6
```

It exited 0.

The entry above for the last `git status` says "the nine declared paths" without printing them. The nine paths of the commit:

`git diff --name-status 2d52b79dc133d070e5e45c91eff7371f999055bc 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf`

```text
M	docs/v3-spike/330-a0-sender-auth/README.md
A	docs/v3-spike/330-a1-1-words/README.md
M	internal/buzz/relay.go
A	internal/buzz/words330_test.go
A	internal/cli/words330_test.go
M	internal/gatestore/plain.go
M	internal/gatestore/plain311_test.go
A	internal/gatestore/words.go
A	internal/gatestore/words_test.go
```

It exited 0.

The entry above for the last `git diff --stat` says "only those four paths" without printing them. On the two commits:

`git diff --stat 2d52b79dc133d070e5e45c91eff7371f999055bc 188e04f7ffb6c23f1100410f3ce76cf7e9137bbf -- internal/gatestore/plain.go internal/gatestore/plain311_test.go internal/buzz/relay.go docs/v3-spike/330-a0-sender-auth/README.md`

```text
 docs/v3-spike/330-a0-sender-auth/README.md | 13 +++++++++++++
 internal/buzz/relay.go                     | 25 +++++++++++++++++++------
 internal/gatestore/plain.go                | 19 +++++++------------
 internal/gatestore/plain311_test.go        |  6 +++---
 4 files changed, 42 insertions(+), 21 deletions(-)
```

It exited 0.
