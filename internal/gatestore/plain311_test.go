package gatestore

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Brief 3.1.1-1, rows 1, 3 and 5: every gate question the code composes, for
// every class the catalog (internal/operatorauth) can raise, read from the
// catalog so a class missed fails the row.

type sample struct {
	name   string
	target string
	facts  *Facts
}

// samples are the target shapes each raiser composes (internal/gatetarget's
// table): the release flow's tag and release (with the prepare's branch),
// the guard's commands, the merge train's merge outside the grant (with the
// PR's title and base), a task's widen past the grant (with the task's
// title) and the squad's growth past the grant.
var samples = map[string][]sample{
	"tag": {
		{"release flow", gatetarget.ReleaseTag("omriariav/amq-squad", "v3.1.0-rc.1", "4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d"), &Facts{Branch: "v3"}},
		{"release flow, no prepare", gatetarget.ReleaseTag("omriariav/amq-squad", "v3.1.0", "4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d"), nil},
		{"git tag", gatetarget.Command("git tag", "v1.2.0"), nil},
		{"tag push", gatetarget.TagPush("refs/tags/v1.2.0"), nil},
		{"push all tags", gatetarget.TagPush("--tags"), nil},
	},
	"github_release": {
		{"release flow", gatetarget.ReleaseCreate("omriariav/amq-squad", "v3.1.0-rc.1"), &Facts{Branch: "v3"}},
		{"gh release create", gatetarget.Command("gh release create", "v1.2.0"), nil},
		{"gh release upload", gatetarget.Command("gh release upload", "v1.2.0"), nil},
		{"gh release edit", gatetarget.Command("gh release edit", "v1.2.0"), nil},
		{"gh release delete", gatetarget.Command("gh release delete", "v1.2.0"), nil},
	},
	"package_publish": {
		{"npm publish", gatetarget.Command("npm publish", ""), nil},
		{"npm publish spec", gatetarget.Command("npm publish", "@scope/pkg"), nil},
		{"twine upload", gatetarget.Command("twine upload", "dist/x.whl"), nil},
	},
	"external_send": {
		{"mcp send", gatetarget.MCPSend("mcp__slack__send_message", "C1"), nil},
		{"mcp send to a user", gatetarget.MCPSend("mcp__slack__send_message", "U024BE7LH"), nil},
		{"mcp send, another family", gatetarget.MCPSend("mcp__gmail__send_email", "owner@example.com"), nil},
		{"gws send", gatetarget.GWSSend("gmail", "send"), nil},
		{"mail", gatetarget.Command("mail", "owner@example.com"), nil},
		{"sendmail", gatetarget.Command("sendmail", ""), nil},
	},
	"credential_change": {
		{"gh auth login", gatetarget.Command("gh auth login", ""), nil},
		{"git config", gatetarget.GitConfig("credential.helper"), nil},
		{"write", gatetarget.Write("/home/u/.ssh/config"), nil},
		{"write, no known program", gatetarget.Write("/home/u/.netrc"), nil},
		{"git config signing key", gatetarget.GitConfig("user.signingkey"), nil},
		{"gh auth logout", gatetarget.Command("gh auth logout", ""), nil},
		{"gh secret set", gatetarget.Command("gh secret set", "NPM_TOKEN"), nil},
		{"ssh-keygen", gatetarget.Command("ssh-keygen", ""), nil},
		{"keychain item", gatetarget.Command("security add-generic-password", "com.example.deploy"), nil},
	},
	"spawn": {
		{"growth: seats", gatetarget.Grow("/r/s1", 13), nil},
		{"growth: files", gatetarget.GrowLimit("/r/s1", "files", 40), nil},
		{"widen: protected paths", gatetarget.GrowTask("/r/s1", "t3", "protected_paths", `["docs/**","internal/x/**"]`), &Facts{Title: "Ship the plain gate questions"}},
		{"widen: one protected path", gatetarget.GrowTask("/r/s1", "t5", "protected_paths", `[".claude/settings.json"]`), &Facts{Title: "tune settings"}},
		{"widen: size cap", gatetarget.GrowTask("/r/s1", "t4", "size_cap", `{"lines":900,"files":30}`), &Facts{Title: "Refactor the relay"}},
		{"merge outside the grant", gatetarget.MergeBeyond("omriariav/amq-squad", 834, "4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d", []string{"ci_green", "task_scope"}),
			&Facts{Title: "gate: the question in plain words, answered with a bare yes or no replied in its thread, and the team confirmation", Branch: "v3"}},
		{"merge, four rules", gatetarget.MergeBeyond("omriariav/amq-squad", 835, "4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d", []string{"ci_green", "force_push", "size_cap", "task_scope"}), &Facts{Branch: "v3"}},
		{"merge, no facts", gatetarget.MergeBeyond("omriariav/amq-squad", 836, "4852f761aa0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d", []string{"protected_path"}), nil},
	},
}

const sampleSquad = "root-sha256:1a5db0a23fc4555ae8493c0339d945b8ca5da0d73dbfaef44e28ebd270a0c3f8"

func renderSample(t *testing.T, action string, i int, s sample) Record {
	t.Helper()
	rec, err := Render(Spec{GateID: fmt.Sprintf("rel-%s-%d", strings.ReplaceAll(action, "_", "-"), i), Action: action, Target: s.target,
		RaisedBy: "lead-a", Squad: sampleSquad, Facts: s.facts}, testNow, testIdent)
	if err != nil {
		t.Fatalf("%s %s: %v", action, s.name, err)
	}
	return rec
}

var (
	hexRun8 = regexp.MustCompile(`[0-9a-f]{8,}`)
	tupleRe = regexp.MustCompile(`(?i)\b(approve|deny|yes) \S+ (release|send|credential|grow) [0-9a-f]{16}|\byes [0-9a-f]{16}\b`)
)

// checkPlain is the plain-language check of one gate text: three plain lines
// above a blank line and the rule (a quoted title line is exempt from the
// count and the sentence length), no hex run of 8 or more and no gate id or
// identifier above the rule, no sentence over 20 words, the class in words
// from the fixed list, a question mark ending the first line, "Yes" and "No"
// starting the third's sentences; below the rule the gate id, the target
// line and the digest.
func checkPlain(text string, rec Record) error {
	above, below, ok := strings.Cut(text, "\n\n"+Rule+"\n")
	if !ok {
		return fmt.Errorf("no rule")
	}
	if hexRun8.MatchString(above) {
		return fmt.Errorf("a hex run above the rule: %q", hexRun8.FindString(above))
	}
	if strings.Contains(above, rec.GateID) {
		return fmt.Errorf("the gate id above the rule")
	}
	for _, id := range []string{"amq-squad gate", "squad:", "class:", "target:", "digest:", "sha256:", "(" + rec.Class + ")"} {
		if strings.Contains(above, id) {
			return fmt.Errorf("identifier %q above the rule", id)
		}
	}
	var lines []string
	for _, l := range strings.Split(above, "\n") {
		if strings.HasPrefix(l, `"`) && strings.HasSuffix(l, `"`) && len(l) > 1 {
			continue // a quoted title
		}
		lines = append(lines, l)
	}
	if len(lines) != 3 {
		return fmt.Errorf("%d plain lines, want 3: %q", len(lines), lines)
	}
	if !strings.HasSuffix(lines[0], "?") {
		return fmt.Errorf("the first line is no question: %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], "Yes ") || !strings.Contains(lines[2], ". No ") {
		return fmt.Errorf("the third line does not start Yes ... No ...: %q", lines[2])
	}
	for _, l := range lines {
		for _, s := range regexp.MustCompile(`[.?]( |$)`).Split(l, -1) {
			if n := len(strings.Fields(s)); n > 20 {
				return fmt.Errorf("a sentence of %d words: %q", n, s)
			}
		}
	}
	if !slices.ContainsFunc(ClassWords, func(w string) bool { return strings.Contains(strings.ToLower(above), strings.ToLower(w)) }) {
		return fmt.Errorf("no class in words above the rule")
	}
	if tok := tokenAbove(above); tok != "" {
		return fmt.Errorf("a token above the rule: %q", tok)
	}
	for _, want := range []string{"amq-squad gate " + rec.GateID, "target: " + rec.Target, "digest: " + rec.Digest} {
		if !strings.Contains(below, want) {
			return fmt.Errorf("below the rule lacks %q", want)
		}
	}
	return nil
}

var (
	versionWord = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)+([-+][0-9A-Za-z.-]+)?$`)
	emailWord   = regexp.MustCompile(`^[^@\s/]+@[^@\s/]+\.[^@\s/]+$`)
)

// tokenAbove is the first code token among the plain lines (a quoted title
// is the author's text and exempt): a word holding a double underscore, a
// path separator, or a dot inside other than a version (v3.1.0-rc.1), an
// email address or a sentence's end. The check is the row's own, so the
// next class someone adds cannot leak a tool name, a setting or a path
// upward through a looser composer.
func tokenAbove(above string) string {
	for _, l := range strings.Split(above, "\n") {
		if strings.HasPrefix(l, `"`) {
			continue
		}
		for _, w := range strings.Fields(l) {
			w = strings.TrimRight(w, ".?!,:;")
			if strings.Contains(w, "__") || strings.ContainsAny(w, "/\\") ||
				strings.Contains(w, ".") && !versionWord.MatchString(w) && !emailWord.MatchString(w) {
				return w
			}
		}
	}
	return ""
}

// The three texts the plan reviewer read on draft PR #838 fail the row.
func TestTheRowFailsTheReviewersThreeTexts(t *testing.T) {
	for name, above := range map[string]string{
		"external send": "Send an outside message with mcp__slack__send_message to C1?\nAsked by lead-a.\nYes lets it run. No stops it.",
		"credential":    "Make a credential change to git setting credential.helper?\nAsked by lead-a.\nYes lets it run. No stops it.",
		"widen":         "Allow a task widen of t3 onto protected paths?\n\"Ship the plain gate questions\"\nThe grant refused the widen at docs/** and 1 more.\nYes widens the task. No leaves it as it is.",
	} {
		if tok := tokenAbove(above); tok == "" {
			t.Errorf("%s: the old text passes the token check", name)
		}
	}
	if tok := tokenAbove("Create a tag for v3.1.0-rc.1 in amq-squad?\nSend it to owner@example.com.\nYes creates the tag. No does nothing."); tok != "" {
		t.Fatalf("a version, an email address or a sentence end read as a token: %q", tok)
	}
}

func TestEveryClassTheCatalogRaisesAsksInPlainWords(t *testing.T) {
	raisable := 0
	for _, c := range operatorauth.ActionCapabilities() {
		if !c.HumanOnly {
			continue
		}
		if gatetarget.Class(c.Action) == "" {
			// a human-only action the gate store does not raise: Render refuses
			// it, so a class that becomes raisable without a sample fails here
			if _, err := Render(Spec{GateID: "g-x", Action: c.Action, Target: "x", RaisedBy: "lead-a"}, testNow, testIdent); err == nil {
				t.Errorf("%s renders but has no sample", c.Action)
			}
			continue
		}
		raisable++
		ss := samples[c.Action]
		if len(ss) == 0 {
			t.Errorf("the catalog raises %s and no sample composes its question", c.Action)
		}
		for i, s := range ss {
			rec := renderSample(t, c.Action, i, s)
			if err := checkPlain(rec.Text, rec); err != nil {
				t.Errorf("%s %s: %v\n%s", c.Action, s.name, err, rec.Text)
			}
			// the 3.0 text fails the same check
			if err := checkPlain(legacyText(rec.Squad, rec.GateID, rec.TypedClass, rec.Class, rec.Target, rec.Digest), rec); err == nil {
				t.Errorf("%s %s: the old text passes the plain check", c.Action, s.name)
			}
			if err := rec.Validate(); err != nil {
				t.Errorf("%s %s: %v", c.Action, s.name, err)
			}
			if t.Failed() {
				continue
			}
			t.Logf("%s / %s:\n%s", c.Action, s.name, rec.Text)
		}
	}
	if raisable < 6 {
		t.Fatalf("the catalog raises %d typed actions, want the six of plan 7.1", raisable)
	}
}

// Row 3: no composed text contains "with one line" or a tuple.
func TestNoQuestionAsksForATuple(t *testing.T) {
	for action, ss := range samples {
		for i, s := range ss {
			rec := renderSample(t, action, i, s)
			if strings.Contains(rec.Text, "with one line") || tupleRe.MatchString(rec.Text) {
				t.Errorf("%s %s asks for a tuple:\n%s", action, s.name, rec.Text)
			}
		}
	}
}

// glue is the composer's fixed vocabulary: the words of its sentence frames.
// It holds no claim a record does not carry (no reviewer, no passed, no
// green, no verified): such words reach a line only from a field.
var glue = strings.Fields(`a all allow an and as asked by cap change commit create creates credential delete does each edit files for found
	message squad channel user mail which program uses credentials file key signs commits how stores log out write makes let path`)

var glue0 = strings.Fields(`a all allow an and as asked by cap change commit create creates credential delete does each edit files for found
	github grant growth in into is it its keeps at leaves lets limit local make merge message more named no nothing of on onto open outside
	past paths prepare protected publish pull push refused release request run see send setting size squad stops tag task the to train
	upload widen widens with writing yes package gws git grow lines`)

// words are the lowercase words of a text as the check compares them.
func words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '/' || r == '@' || r == '…')
	}) {
		if w = strings.ToLower(strings.Trim(w, ".-")); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// fieldWords are the words of every field the composer reads: the target
// (and the values inside it), the raiser, the facts, and the refused rules
// in words.
func fieldWords(rec Record) map[string]bool {
	m := map[string]bool{}
	add := func(s string) {
		for _, w := range words(plain(s)) {
			m[w] = true
		}
		for _, w := range words(plain(strings.NewReplacer("=", " ", "_", " ", "#", " ", ",", " ", "[", " ", "]", " ", `"`, " ", "{", " ", "}", " ", ":", " ", "/", " ").Replace(s))) {
			m[w] = true
		}
	}
	add(rec.Target)
	add(rec.RaisedBy)
	if rec.Facts != nil {
		add(rec.Facts.Title)
		add(rec.Facts.Branch)
		add(quote(rec.Facts.Title))
	}
	if m := mergeTargetRe.FindStringSubmatch(rec.Target); m != nil {
		for _, r := range strings.Split(m[3], ",") {
			add(RuleWords(r))
		}
	}
	// the fixed tables a field is read through: the send's service, the
	// credential file's program
	add(ServiceOf(rec.Target))
	add(CredentialProgram(strings.TrimPrefix(rec.Target, "write ")))
	return m
}

// traceWords is row 5's check: every word of the plain lines is a glue word,
// a field's word, or a number (a count of a field's values); and the second
// line carries at least one field word (it states a recorded fact).
func traceWords(rec Record, above string) error {
	fields := fieldWords(rec)
	isGlue := func(w string) bool { return slices.Contains(glue, w) || slices.Contains(glue0, w) }
	for _, w := range words(above) {
		if !isGlue(w) && !fields[w] && strings.Trim(w, "0123456789") != "" {
			return fmt.Errorf("the word %q comes from no field", w)
		}
	}
	var lines []string
	for _, l := range strings.Split(above, "\n") {
		if !strings.HasPrefix(l, `"`) {
			lines = append(lines, l)
		}
	}
	if len(lines) == 3 && !slices.ContainsFunc(words(lines[1]), func(w string) bool { return fields[w] && !isGlue(w) || strings.Trim(w, "0123456789") == "" }) {
		return fmt.Errorf("the second line names no recorded fact: %q", lines[1])
	}
	return nil
}

func TestEveryWordOfThePlainLinesTracesToAField(t *testing.T) {
	for action, ss := range samples {
		for i, s := range ss {
			rec := renderSample(t, action, i, s)
			if err := traceWords(rec, Plain(rec.Text)); err != nil {
				t.Errorf("%s %s: %v\n%s", action, s.name, err, rec.Text)
			}
		}
	}
	// a line asserting what nobody recorded fails the row
	rec := renderSample(t, "spawn", 5, samples["spawn"][5])
	lines := PlainLines(rec)
	lines[len(lines)-2] = "Its checks passed on the train."
	if err := traceWords(rec, strings.Join(lines, "\n")); err == nil || !strings.Contains(err.Error(), "passed") {
		t.Fatalf("an unrecorded claim passed the row: %v", err)
	}
	lines[len(lines)-2] = "The reviewer accepted it."
	if err := traceWords(rec, strings.Join(lines, "\n")); err == nil {
		t.Fatal("the reviewer's acceptance passed the row")
	}
}

// Row 5: a quoted PR or task title is exempt from the sentence count and is
// cut at 80 characters with an ellipsis; a hex run in it is cut too.
func TestAQuotedTitleIsCutAt80WithAnEllipsis(t *testing.T) {
	rec := renderSample(t, "spawn", 5, samples["spawn"][5])
	lines := PlainLines(rec)
	if len(lines) != 4 || lines[0] != "Allow a merge of pull request #834 into v3?" {
		t.Fatalf("lines = %q", lines)
	}
	title := rec.Facts.Title
	want := `"` + string([]rune(title)[:TitleMax]) + `…"`
	if lines[1] != want {
		t.Fatalf("title line = %q, want %q", lines[1], want)
	}
	short := Facts{Title: "Fix 4852f761aa0b in the relay"}
	r2, err := Render(Spec{GateID: "w-1", Action: "spawn", Target: gatetarget.GrowTask("/r/s1", "t9", "size_cap", `{"lines":10,"files":2}`), RaisedBy: "lead-a", Facts: &short}, testNow, testIdent)
	if err != nil {
		t.Fatal(err)
	}
	if got := PlainLines(r2)[1]; got != `"Fix 4852f76… in the relay"` {
		t.Fatalf("short title line = %q", got)
	}
}

// A record raised by 3.1.0 (the old text, no facts) still validates and
// still verifies; facts that do not validate never stop a gate (SafeFacts
// leaves them out); a record whose facts were edited no longer validates.
func TestTheOldTextStillValidatesAndFactsAreSafe(t *testing.T) {
	rec := renderSample(t, "tag", 2, samples["tag"][2])
	old := rec
	old.Text = legacyText(rec.Squad, rec.GateID, rec.TypedClass, rec.Class, rec.Target, rec.Digest)
	old.TextSHA256 = sha256Text([]byte(old.Text))
	if err := old.Validate(); err != nil {
		t.Fatalf("a 3.1.0 record no longer validates: %v", err)
	}
	if Plain(old.Text) != "" {
		t.Fatal("a 3.1.0 text has plain lines")
	}
	withFacts := renderSample(t, "github_release", 0, samples["github_release"][0])
	edited := withFacts
	edited.Facts = &Facts{Branch: "main"}
	if err := edited.Validate(); err == nil {
		t.Fatal("a record whose facts were edited validates")
	}
	legacyWithFacts := withFacts
	legacyWithFacts.Text = legacyText(rec.Squad, withFacts.GateID, withFacts.TypedClass, withFacts.Class, withFacts.Target, withFacts.Digest)
	legacyWithFacts.TextSHA256 = sha256Text([]byte(legacyWithFacts.Text))
	if err := legacyWithFacts.Validate(); err == nil {
		t.Fatal("the old text validates on a record with facts")
	}
	if f := SafeFacts("a\x00b", "bad..branch"); f != nil {
		t.Fatalf("SafeFacts kept invalid values: %+v", f)
	}
	if f := SafeFacts("  two   words ", "v3"); f == nil || f.Title != "two words" || f.Branch != "v3" {
		t.Fatalf("SafeFacts = %+v", f)
	}
	raw, _ := json.Marshal(renderSample(t, "tag", 2, samples["tag"][2]))
	if strings.Contains(string(raw), `"facts"`) {
		t.Fatal("a record without facts carries the field")
	}
}

// ParseBare reads yes, no, approve and deny alone, case and trailing
// punctuation ignored, and nothing else.
func TestParseBare(t *testing.T) {
	for in, want := range map[string]string{"yes": "approve", "Yes.": "approve", " YES!\n": "approve", "approve": "approve", "no": "deny", "No.": "deny", "deny": "deny", "no ?": "deny"} {
		if got, ok := ParseBare(in); !ok || got != want {
			t.Errorf("ParseBare(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "yes please", "yes\nno", "y", "nope", "¿yes", "yes " + strings.Repeat("a", 16)} {
		if got, ok := ParseBare(in); ok {
			t.Errorf("ParseBare(%q) = %q", in, got)
		}
	}
}
