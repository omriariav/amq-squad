package statecontract

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// S2-3 (3.3.0): the launch family. A failed launch's code reaches a task
// through scheduler.(LaunchFailure).Code: the pool's tasks wait on it and,
// when the launch fails the same way again, block on it. The codes the launch
// path wrote as string literals are constants now, each on the block and wait
// lists, and LaunchReasons names the family for pi/test/words.test.ts.

// s23LaunchLiterals is each S2-3 constant beside the literal it replaced.
var s23LaunchLiterals = []struct{ constant, literal string }{
	{"piruntime.ReasonLedgerUnreadable", "ledger_unreadable"},
	{"piruntime.ReasonPiResumeContextLost", "pi_resume_context_lost"},
	{"piruntime.ReasonPiSessionIDInvalid", "pi_session_id_invalid"},
	{"piruntime.ReasonPiSessionIDMissing", "pi_session_id_missing"},
	{"piruntime.ReasonPiSessionUnreadable", "pi_session_unreadable"},
	{"scheduler.ReasonLaunchError", "launch_error"},
	{"scheduler.ReasonNotStarted", "not_started"},
	{"seatexec.ReasonArgvMissing", "argv_missing"},
	{"seatexec.ReasonCodexApprovalMissing", "codex_approval_missing"},
	{"seatexec.ReasonCodexSandboxMissing", "codex_sandbox_missing"},
	{"seatexec.ReasonDirWritableByOthers", "dir_writable_by_others"},
	{"seatexec.ReasonEnvNeedsInvalid", "env_needs_invalid"},
	{"seatexec.ReasonIdentityMismatch", "identity_mismatch"},
	{"seatexec.ReasonIdentityProofFailed", "identity_proof_failed"},
	{"seatexec.ReasonIdentityProofTimeout", "identity_proof_timeout"},
	{"seatexec.ReasonMailboxMissing", "mailbox_missing"},
	{"seatexec.ReasonMkdirFailed", "mkdir_failed"},
	{"seatexec.ReasonPathInvalid", "path_invalid"},
	{"seatexec.ReasonProofExpired", "proof_expired"},
	{"seatexec.ReasonProofRefused", "proof_refused"},
	{"seatexec.ReasonProofTicketConsumed", "proof_ticket_consumed"},
	{"seatexec.ReasonProofTicketMissing", "proof_ticket_missing"},
	{"seatexec.ReasonProofUnacknowledged", "proof_unacknowledged"},
	{"seatexec.ReasonProviderMissing", "provider_missing"},
	{"seatexec.ReasonRecordInvalid", "record_invalid"},
	{"seatexec.ReasonRecordMissing", "record_missing"},
	{"seatexec.ReasonRoleMismatch", "role_mismatch"},
	{"seatexec.ReasonRoleMissing", "role_missing"},
	{"seatexec.ReasonRootInvalid", "root_invalid"},
	{"seatexec.ReasonRootUnreadable", "root_unreadable"},
	{"seatexec.ReasonRulesMismatch", "rules_mismatch"},
	{"seatexec.ReasonRulesMissing", "rules_missing"},
	{"seatexec.ReasonRuntimeMismatch", "runtime_mismatch"},
	{"seatexec.ReasonSeatInvalid", "seat_invalid"},
	{"seatexec.ReasonStatFailed", "stat_failed"},
	{"seatexec.ReasonSymlinkInPath", "symlink_in_path"},
	{"seatexec.ReasonWriteFailed", "write_failed"},
	{"seatexec.ReasonWrongOwner", "wrong_owner"},
	{"seatlaunch.ReasonApplyAmbiguous", "apply_ambiguous"},
	{"seatlaunch.ReasonApplyFailed", "apply_failed"},
	{"seatlaunch.ReasonDecisionsPending", "decisions_pending"},
	{"seatlaunch.ReasonIdentityForged", "identity_forged"},
	{"seatlaunch.ReasonNoProof", "no_proof"},
	{"seatlaunch.ReasonNotObserved", "not_observed"},
	{"seatlaunch.ReasonPrepareRefused", "prepare_refused"},
	{"seatlaunch.ReasonProofLedgerFailed", "proof_ledger_failed"},
	{"seatlaunch.ReasonProofMalformed", "proof_malformed"},
	{"seatlaunch.ReasonProofMarkerFailed", "proof_marker_failed"},
	{"seatlaunch.ReasonProofOriginUnknown", "proof_origin_unknown"},
	{"seatlaunch.ReasonProofTokenInvalid", "proof_token_invalid"},
	{"seatlaunch.ReasonProofUnknown", "proof_unknown"},
	{"seatlaunch.ReasonRefused", "refused"},
	{"seatlaunch.ReasonResourcePresent", "resource_present"},
	{"seatlaunch.ReasonSeatNotUp", "seat_not_up"},
}

// s23ListedBefore are the launch family's constants that were on the lists
// before S2-3.
var s23ListedBefore = []string{
	"claudeseat.TokenInsecure",
	"scheduler.AMQBindingPresent",
	"scheduler.ReasonResourcePresent",
	"seatexec.ClaudeHooksMissing",
	"seatexec.ClaudePermissionMismatch",
	"seatexec.IdentityIncomplete",
	"seatexec.PiNotEmbedded",
	"seatexec.SandboxPinsMissing",
}

// launchAddedAfterS23 are the launch family's constants added after S2-3.
var launchAddedAfterS23 = []string{
	"seatexec.ClaudeProjectMCP", // 3.3.1 item 5, D5 (c)
}

// s23NotLaunch are the entries of s2NotTaskReasons that are no launch code;
// every other entry is on the launch family's Outside list, so pi words it.
var s23NotLaunch = map[string]string{
	"claudeseat.TokenVar":             "an environment variable's name",
	"routing.SkipInputOver100k":       "a router skip reason",
	"routing.SkipInputOver32k":        "a router skip reason",
	"routing.SkipNotInRegistry":       "a router skip reason",
	"routing.SkipProbation":           "a router skip reason",
	"routing.SkipRuntimeNotPermitted": "a router skip reason",
	"routing.SkipUnverified":          "a router skip reason",
	"routing.SkipUsedBefore":          "a router skip reason",
	"schedproc.ReasonLeadRunning":     "the lead lock's refusal",
	"seatlaunch.ClaudePermissionMode": "a permission mode",
	"seatlaunch.PiWakeAuditReason":    "an audit sentence",
}

// s23OutsideNotExcluded are the Outside constants of no reason family, so on
// no s2NotTaskReasons entry.
var s23OutsideNotExcluded = []string{"piruntime.RuntimeMismatchReason"}

// s23NotDoorParams are the helper parameters a family or outside constant
// is passed to that are no launch door, each with why (s23-door). A
// parameter of fmt's formatting functions is no door either (s23FormatParam).
var s23NotDoorParams = map[string]string{
	"param cli.refuse#0": "amq-squad up's refusal of the pi runtime (cli.(*upRun).checkPiBuild) with the outside code piruntime.ReasonRejected, which never reaches a task",
}

// s23FormatParam: a parameter of fmt's formatting functions, which format
// text and are no door (fmt's scanning functions write into their operands
// and are not skipped).
func s23FormatParam(door string) bool {
	for _, function := range []string{"Errorf", "Fprintf", "Printf", "Sprintf", "Fprint", "Print", "Sprint", "Fprintln", "Println", "Sprintln"} {
		if strings.HasPrefix(door, "param fmt."+function+"#") {
			return true
		}
	}
	return false
}

// s23Record reads the launch family as S1-2b recorded it: the values of
// sites.md 5a and the guarded words of 5c.
func s23Record(t *testing.T) (values, guarded []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "v3-spike", "330-s1-2b-reasons", "sites.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	section := func(from, to string) string {
		i, j := strings.Index(text, from), strings.Index(text, to)
		if i < 0 || j < i {
			t.Fatalf("sites.md: no section from %q to %q", from, to)
		}
		return text[i:j]
	}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|").FindAllStringSubmatch(section("### 5a.", "### 5b."), -1) {
		values = append(values, m[1])
	}
	for _, m := range regexp.MustCompile("(?m)^- `([a-z_]+)`: ").FindAllStringSubmatch(section("### 5c.", "## 6."), -1) {
		guarded = append(guarded, m[1])
	}
	return values, guarded
}

// TestLaunchFamily: the count row (54 S2-3 constants, 8 listed before, 1
// added after, 4 kept words), each S2-3 constant byte-equal to its literal on both lists,
// the family as S1-2b recorded it, its doors on both lists, each kept word
// with its guard, every constant used at a family door in the family, and no
// string literal at a family door but a kept word.
func TestLaunchFamily(t *testing.T) {
	family := LaunchReasons()
	block, wait := BlockReasons(), WaitReasons()
	codes := map[string]map[string]string{"block": {}, "wait": {}}
	for name, list := range map[string]ReasonList{"block": block, "wait": wait} {
		for _, entry := range list.Entries {
			codes[name][entry.Constant] = entry.Code
		}
	}

	// the count row
	if len(s23LaunchLiterals) != 54 || len(s23ListedBefore) != 8 || len(launchAddedAfterS23) != 1 || len(family.Constants) != 54+8+1 || len(family.Kept) != 4 {
		t.Errorf("the launch family holds %d constants (S2-3 %d, before %d, after %d) and %d kept words; the count is 54 + 8 + 1 and 4", len(family.Constants), len(s23LaunchLiterals), len(s23ListedBefore), len(launchAddedAfterS23), len(family.Kept))
	}
	want := map[string]bool{}
	for _, row := range s23LaunchLiterals {
		want[row.constant] = true
	}
	for _, name := range s23ListedBefore {
		want[name] = true
	}
	for _, name := range launchAddedAfterS23 {
		want[name] = true
	}
	seen := map[string]bool{}
	for _, name := range family.Constants {
		if seen[name] {
			t.Errorf("the launch family names %s twice", name)
		}
		seen[name] = true
		if !want[name] {
			t.Errorf("the launch family names %s, which is neither an S2-3 constant, listed before, nor added after", name)
		}
		if codes["block"][name] == "" && codes["wait"][name] == "" {
			t.Errorf("the launch family names %s, which is on neither list", name)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("the launch family does not name %s", name)
		}
	}

	// each S2-3 constant, byte for byte the literal it replaced, on both lists
	literals := map[string]bool{}
	for _, row := range s23LaunchLiterals {
		literals[row.literal] = true
		for _, list := range []string{"block", "wait"} {
			if got := codes[list][row.constant]; got != row.literal {
				t.Errorf("%s list: %s is %q; its literal was %q", list, row.constant, got, row.literal)
			}
		}
	}
	if len(literals) != len(s23LaunchLiterals) {
		t.Errorf("two S2-3 constants share a literal")
	}

	// the family as S1-2b recorded it: 5a's values are the literals and the
	// kept words, and 5c's guarded words are the kept words
	values, guarded := s23Record(t)
	kept := map[string]bool{}
	var keptWords []string
	for _, word := range family.Kept {
		kept[word.Word] = true
		keptWords = append(keptWords, word.Word)
	}
	var recorded []string
	for _, value := range values {
		if !kept[value] {
			recorded = append(recorded, value)
			if !literals[value] {
				t.Errorf("sites.md 5a records %q, which is no S2-3 constant's literal and no kept word", value)
			}
		}
	}
	if len(values) != 58 || len(recorded) != len(s23LaunchLiterals) {
		t.Errorf("sites.md 5a records %d values, %d of them no kept word; S2-3 has %d constants", len(values), len(recorded), len(s23LaunchLiterals))
	}
	sort.Strings(guarded)
	sort.Strings(keptWords)
	if strings.Join(guarded, " ") != strings.Join(keptWords, " ") {
		t.Errorf("sites.md 5c guards %v; the kept words are %v", guarded, keptWords)
	}

	survey := cachedReasonSurvey(t)
	if survey.functions[family.Door] == nil {
		t.Errorf("the launch family's door %s is no declared function", family.Door)
	}
	doors := map[string]bool{}
	for _, door := range family.Doors {
		doors[door] = true
		for name, list := range map[string]ReasonList{"block": block, "wait": wait} {
			found := false
			for _, declared := range list.Doors {
				found = found || declared == door
			}
			if !found {
				t.Errorf("the launch door %s is no door of the %s list", door, name)
			}
		}
	}
	for _, word := range family.Kept {
		if survey.functions[word.Guard] == nil {
			t.Errorf("the kept word %q is guarded at %s, and no such function is declared", word.Word, word.Guard)
		}
		if word.Why == "" {
			t.Errorf("the kept word %q says no why", word.Word)
		}
		for list := range codes {
			for constant, code := range codes[list] {
				if code == word.Word {
					t.Errorf("the kept word %q is the %s code of %s", word.Word, list, constant)
				}
			}
		}
	}

	// the uses at the launch doors: every constant is in the family, every
	// S2-3 constant has one, and a literal is a kept word or empty
	used := map[string]bool{}
	keptWritten := map[string]bool{}
	for _, use := range survey.uses {
		if !doors[use.door] {
			continue
		}
		if use.literal {
			if use.value != "" && !kept[use.value] {
				t.Errorf("%s: the string literal %q reaches the launch door %s in %s; a launch code is a listed constant", use.site, use.value, use.door, use.function)
			}
			keptWritten[use.value] = true
			continue
		}
		used[use.constant] = true
		if !seen[use.constant] {
			t.Errorf("%s: %s is used at the launch door %s in %s and is not in the launch family", use.site, use.constant, use.door, use.function)
		}
	}
	for _, row := range s23LaunchLiterals {
		if !used[row.constant] {
			t.Errorf("%s is used at no launch door", row.constant)
		}
	}
	for _, word := range family.Kept {
		if !keptWritten[word.Word] {
			t.Errorf("the kept word %q is written at no launch door", word.Word)
		}
	}
	// a folded literal (a concatenation, parentheses, a conversion, one
	// local) at a family door is a kept word or empty, and none reaches an
	// outside door
	outsideDoors := map[string]bool{}
	for _, door := range family.OutsideDoors {
		outsideDoors[door] = true
		if doors[door] {
			t.Errorf("the outside door %s is a family door", door)
		}
	}
	for _, expression := range s2ReadModule(t).expressions {
		if strings.HasPrefix(expression.site, "spike/") {
			continue
		}
		if doors[expression.door] && expression.value != "" && !kept[expression.value] {
			t.Errorf("%s: the string expression %q reaches the launch door %s; a launch code is a listed constant", expression.site, expression.value, expression.door)
		}
		if outsideDoors[expression.door] {
			t.Errorf("%s: the string expression %q reaches the outside door %s; it is an Outside constant", expression.site, expression.value, expression.door)
		}
	}

	// Outside: on no list, and every s2NotTaskReasons entry but s23NotLaunch
	outside := map[string]bool{}
	for _, entry := range family.Outside {
		if outside[entry.Constant] {
			t.Errorf("Outside names %s twice", entry.Constant)
		}
		outside[entry.Constant] = true
		if entry.Mark != "" || len(entry.Writers) != 0 {
			t.Errorf("Outside: %s carries a mark or a writer", entry.Constant)
		}
		for list := range codes {
			for constant, code := range codes[list] {
				if code == entry.Code {
					t.Errorf("Outside: %s has the %s code of %s", entry.Constant, list, constant)
				}
			}
		}
		if kept[entry.Code] {
			t.Errorf("Outside: %s is the kept word %q", entry.Constant, entry.Code)
		}
	}
	wantOutside := map[string]bool{}
	for name := range s2NotTaskReasons {
		if _, ok := s23NotLaunch[name]; !ok {
			wantOutside[name] = true
		}
	}
	for name := range s23NotLaunch {
		if _, ok := s2NotTaskReasons[name]; !ok {
			t.Errorf("s23NotLaunch is stale: %s is not on s2NotTaskReasons", name)
		}
	}
	for _, name := range s23OutsideNotExcluded {
		wantOutside[name] = true
	}
	for name := range wantOutside {
		if !outside[name] {
			t.Errorf("Outside does not name %s, an entry of s2NotTaskReasons that is no s23NotLaunch entry", name)
		}
	}
	for name := range outside {
		if !wantOutside[name] {
			t.Errorf("Outside names %s, which is no s2NotTaskReasons entry and not on s23OutsideNotExcluded", name)
		}
	}
	if len(family.Outside) != 13 {
		t.Errorf("Outside holds %d constants; the count is 13", len(family.Outside))
	}
	outsideUsed := map[string]bool{}
	for _, use := range survey.uses {
		if !outsideDoors[use.door] {
			continue
		}
		if use.literal {
			t.Errorf("%s: the string literal %q reaches the outside door %s", use.site, use.value, use.door)
			continue
		}
		outsideUsed[use.constant] = true
		if !outside[use.constant] && !seen[use.constant] {
			t.Errorf("%s: %s is used at the outside door %s and is in neither the family nor Outside", use.site, use.constant, use.door)
		}
	}
	for _, door := range family.OutsideDoors {
		if survey.functions[strings.TrimPrefix(door[:strings.LastIndex(door, "#")], "param ")] == nil {
			t.Errorf("the outside door %s is no declared function's parameter", door)
		}
	}

	// s23-door: a pass-through helper's parameter is a launch door in its
	// launch callers (CallerDoors): there a constant is in the family and a
	// literal or folded expression is empty, a kept word or a listed
	// non-code; and a family or outside constant passed to any other helper
	// parameter is at a declared door, or that parameter is classified
	callers := map[string]map[string]bool{}  // door -> function
	notCodes := map[string]map[string]bool{} // function -> word
	for _, group := range family.CallerDoors {
		if len(group.Doors) == 0 || len(group.Functions) == 0 {
			t.Errorf("a caller door names no door or no function: %+v", group)
		}
		for _, door := range group.Doors {
			if callers[door] != nil {
				t.Errorf("the caller door %s is declared twice", door)
			}
			callers[door] = map[string]bool{}
			if doors[door] || outsideDoors[door] {
				t.Errorf("the caller door %s is also a family or outside door", door)
			}
			if function, index, ok := reasonParameterDoor(door); !ok || survey.functions[function] == nil || index >= survey.functions[function].Params().Len() {
				t.Errorf("the caller door %s is no declared function's parameter", door)
			}
			for _, function := range group.Functions {
				callers[door][function] = true
			}
		}
		for _, function := range group.Functions {
			if survey.functions[function] == nil {
				t.Errorf("a caller door names %s, and no such function is declared", function)
			}
		}
		for _, word := range group.NotCodes {
			if notCodes[word.Function] == nil {
				notCodes[word.Function] = map[string]bool{}
			}
			notCodes[word.Function][word.Word] = true
			if word.Why == "" || word.Word == "" || kept[word.Word] || !slices.Contains(group.Functions, word.Function) {
				t.Errorf("a caller door's non-code %q says no why, is empty, is a kept word or names a function the door does not: %+v", word.Word, word)
			}
			for list := range codes {
				for constant, code := range codes[list] {
					if code == word.Word {
						t.Errorf("a caller door's non-code %q is the %s code of %s", word.Word, list, constant)
					}
				}
			}
		}
	}
	atCaller := func(door, function string) bool { return callers[door][function] }
	allowed := func(value, function string) bool { return value == "" || kept[value] || notCodes[function][value] }
	callerUsed, wordsWritten, notDoorSeen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, use := range survey.uses {
		if atCaller(use.door, use.function) {
			if use.literal {
				if !allowed(use.value, use.function) {
					t.Errorf("%s: the string literal %q reaches the launch caller door %s in %s; a launch code is a listed constant", use.site, use.value, use.door, use.function)
				}
				wordsWritten[use.function+"\x00"+use.value] = true
				continue
			}
			callerUsed[use.function] = true
			if !seen[use.constant] {
				t.Errorf("%s: %s is used at the launch caller door %s in %s and is not in the launch family", use.site, use.constant, use.door, use.function)
			}
			continue
		}
		if use.literal || (!seen[use.constant] && !outside[use.constant]) || !strings.HasPrefix(use.door, "param ") || doors[use.door] || outsideDoors[use.door] {
			continue
		}
		if s23FormatParam(use.door) {
			continue
		}
		if _, ok := s23NotDoorParams[use.door]; ok {
			notDoorSeen[use.door] = true
			continue
		}
		t.Errorf("%s: %s is passed to %s in %s, which is no launch door, no caller door in that function and not on s23NotDoorParams", use.site, use.constant, use.door, use.function)
	}
	for _, expression := range s2ReadModule(t).expressions {
		if strings.HasPrefix(expression.site, "spike/") || !atCaller(expression.door, expression.function) {
			continue
		}
		wordsWritten[expression.function+"\x00"+expression.value] = true
		if !allowed(expression.value, expression.function) {
			t.Errorf("%s: the string expression %q reaches the launch caller door %s in %s; a launch code is a listed constant", expression.site, expression.value, expression.door, expression.function)
		}
	}
	for _, group := range family.CallerDoors {
		used := false
		for _, function := range group.Functions {
			used = used || callerUsed[function]
		}
		if !used {
			t.Errorf("no function of the caller door %v passes it a family constant", group.Doors)
		}
		for _, word := range group.NotCodes {
			if !wordsWritten[word.Function+"\x00"+word.Word] {
				t.Errorf("a caller door's non-code %q is passed to it by %s no more", word.Word, word.Function)
			}
		}
	}
	for _, door := range sortedKeys(s23NotDoorParams) {
		if !notDoorSeen[door] {
			t.Errorf("s23NotDoorParams is stale: no family or outside constant is passed to %s", door)
		}
	}

	t.Logf("launch family: %d constants (%d S2-3, %d before), %d doors, %d kept words, %d outside at %d doors, %d caller doors", len(family.Constants), len(s23LaunchLiterals), len(s23ListedBefore), len(family.Doors), len(family.Kept), len(family.Outside), len(family.OutsideDoors), len(family.CallerDoors))
}
