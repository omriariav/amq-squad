package cli

import (
	"fmt"
	"strings"
	"testing"
)

// flagValue is extraction as OUT-OF-BAND state: the value, plus whether one was present at all.
//
// #577 round 8: the previous version marked a dangling flag with an in-band sentinel STRING. I
// had rejected the empty string precisely to avoid collision and then chose another value from
// the SAME DOMAIN, so the decider could not distinguish a real dangling flag from the legitimate
// (if weird) invocation -F "<dangling flag with no value>", which real tmux accepts. An in-band
// sentinel collides with its domain by construction; no better magic string fixes that.
//
// hasValue answers a question the value cannot: "was there a value?" is not a fact about the
// value, so it does not belong in the value.
type flagValue struct {
	Value    string
	HasValue bool
}

// listPanesArgv is the structured result of ONE option-aware walk of a list-panes invocation.
//
// Both -F and -t use the SAME representation, flagValue{Value, HasValue}: a value-less flag is
// still recorded, so it counts toward multiplicity, and its absence of a value is carried out of
// band rather than by any in-domain marker. Without counting it, "exactly one" would be a claim
// about value-bearing pairs rather than about flags (r7 F2).
//
// #577 round 9 (second pass): the previous design had one shared extractor but THREE independent
// CALLS, each walking argv knowing only its own flag. Shared code is not shared parsing. The
// falsifying inputs, all of which real tmux reads differently than independent scans do:
//
//	[-s -F -t -t =issue-96]   -F consumes "-t", so format="-t" and target="=issue-96".
//	                          An independent -t scan re-reads -F's consumed value and reports
//	                          target="-t", skipping the real target entirely.
//	[-F -s -t =issue-96]      -F consumes "-s", so scope is ABSENT. An independent hasFlag sees
//	                          the consumed token and falsely reports scope present.
//	[-s -t -F -F #{pane_id}]  -t consumes "-F", so target="-F" and format="#{pane_id}".
//
// ROLE IS A PROPERTY OF THE WHOLE ARGV, not of a token or of one flag's view of it. That is why
// no number of per-flag scans can be made correct: each is missing the information that decides
// the question. One walk that knows every option's arity is the only shape that can answer it.
//
// This is the fourth level of the same collision in this PR: representation (r8), iteration within
// one flag (r9 first pass), and now iteration ACROSS flags. Each fix addressed the level it was
// shown and left the one above.
type listPanesArgv struct {
	Scope   bool        // -s, boolean
	Targets []flagValue // -t, consumes one token
	Formats []flagValue // -F, consumes one token
	Unknown []string    // tokens that are neither options nor consumed values
}

// parseListPanesArgv walks the argv ONCE, honouring each option's arity and skipping every
// consumed value, and returns the single structured result every guard consumes.
func parseListPanesArgv(args []string) listPanesArgv {
	var parsed listPanesArgv
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-s":
			parsed.Scope = true
		case "-t", "-F":
			flag := args[i]
			if i+1 >= len(args) {
				// Value-less: counted, absence carried OUT OF BAND (r8) so no value can
				// imitate it.
				if flag == "-t" {
					parsed.Targets = append(parsed.Targets, flagValue{})
				} else {
					parsed.Formats = append(parsed.Formats, flagValue{})
				}
				continue
			}
			value := flagValue{Value: args[i+1], HasValue: true}
			if flag == "-t" {
				parsed.Targets = append(parsed.Targets, value)
			} else {
				parsed.Formats = append(parsed.Formats, value)
			}
			// CONSUMED. Skipping is what stops a value that spells another option from being
			// read as one -- by ANY guard, because there is only one walk.
			i++
		default:
			parsed.Unknown = append(parsed.Unknown, args[i])
		}
	}
	return parsed
}

// validateListPanesInvocation is the SINGLE decider: it consumes one parse and answers every
// question the fixture asks of a list-panes invocation -- scope, target, format -- returning the
// requested format when the invocation is one real tmux would answer as this fixture models it.
//
// #577 round 9 GATE REJECT (dev-2), and the reject was right twice over:
//
//  1. BUILD. My rewrite deleted paneListFormatArg while four call sites still called it. gofmt is
//     clean on a tree that cannot compile, which is now the fourth time in this milestone that
//     formatting has been mistaken for correctness. Only a compiler decides that.
//  2. EVIDENCE. TestListPanesFakeRejectsScopeThatIsOnlyAConsumedValue called parseListPanesArgv
//     and asserted parsed.Scope. It never invoked the fake, so deleting the fake's `if
//     !parsed.Scope` guard left the test that NAMES that rejection green. It was also a verbatim
//     duplicate of row two of the parser table -- same argv, same assertion -- so it added no
//     coverage while reading as though it added the decisive coverage. That is the r7
//     plumbing-vs-decision failure returning at the level above the one I fixed.
//
// The structural fix, not the instance fix: there is ONE parse and ONE decision function over its
// result, and the fake and the named rejection tests all call THIS. A guard the tests do not call
// is a guard nothing protects, so the only way to keep that property is to leave no second place
// where the decision could live.
//
// wantTarget is a parameter because the pinned exact-match target is a property of the FIXTURE's
// modeled world, not of tmux. Passing it keeps one decider instead of forking the function.
func validateListPanesInvocation(args []string, wantTarget string) (string, error) {
	parsed := parseListPanesArgv(args)

	// -s SCOPE (r7 F1). Real tmux without -s enumerates only ONE window, so an existing role
	// window could be missing from the before-snapshot and then attributed to this create and
	// destroyed. Decided on the walked result, so -s appearing only as another option's consumed
	// value is correctly ABSENT.
	if !parsed.Scope {
		return "", fmt.Errorf("list-panes must pass -s to enumerate the whole session; without it tmux returns one window only")
	}

	// Three independent properties: exactly ONE -t, it HAS a value, and that value is the
	// exact-match target. Presence is out of band, so no target or format string can imitate an
	// absent one.
	switch {
	case len(parsed.Targets) != 1:
		return "", fmt.Errorf("list-panes must pass exactly one -t; got %d (%v)", len(parsed.Targets), parsed.Targets)
	case !parsed.Targets[0].HasValue:
		return "", fmt.Errorf("list-panes passed -t with no value")
	case parsed.Targets[0].Value != wantTarget:
		return "", fmt.Errorf("list-panes must target the exact-match form %q; got %q", wantTarget, parsed.Targets[0].Value)
	}

	switch {
	case len(parsed.Formats) != 1:
		return "", fmt.Errorf("list-panes must pass exactly one -F; got %d (%v)", len(parsed.Formats), parsed.Formats)
	case !parsed.Formats[0].HasValue:
		return "", fmt.Errorf("list-panes passed -F with no value")
	}
	return parsed.Formats[0].Value, nil
}

// This test must falsify the DECISION, not the plumbing -- the r7 lesson, re-applied one level up
// after r9's first submission failed it again.
//
// It calls validateListPanesInvocation, the SAME function the list-panes fake calls, so deleting
// any clause inside it fails a row here BY NAME. Asserting on parseListPanesArgv instead would
// prove only that a struct field was populated, which is what the rejected version did.
//
// EVERY ROW IS WELL-FORMED EXCEPT IN THE ONE DIMENSION UNDER TEST. This matters more than it
// looks: the rejected version's "duplicate -F" row passed no -s and no -t at all, so under a
// combined decider it would refuse on SCOPE and still satisfy an "exactly one" assertion only by
// accident of message wording. A row that can be rejected for a reason other than the one it
// names is evidence about nothing. Traced clause by clause, not assumed.
func TestListPanesInvocationDeciderRejectsEveryMalformedDimension(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			// The case that slipped a count-only guard: ONE flag, no value.
			name: "lone dangling -F", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F"},
			wantErr: "-F with no value",
		},
		{
			name: "duplicate -F", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F", "#{pane_id}", "-F", "#{window_id}"},
			wantErr: "exactly one -F",
		},
		{
			name: "no -F at all", args: []string{"list-panes", "-s", "-t", "=issue-96"},
			wantErr: "exactly one -F",
		},
		{
			// r7 F1, now falsifiable: delete the scope clause and this row goes green.
			name: "no -s at all", args: []string{"list-panes", "-t", "=issue-96", "-F", "#{pane_id}"},
			wantErr: "must pass -s",
		},
		{
			// The row dev-2 named. -F consumes "-s", so scope is ABSENT however much the token
			// appears in argv. This replaces TestListPanesFakeRejectsScopeThatIsOnlyAConsumedValue,
			// which asserted the same argv against the PARSER and therefore duplicated row two of
			// the parser table while proving nothing about the rejection it was named for.
			name: "scope present only as a consumed value", args: []string{"list-panes", "-F", "-s", "-t", "=issue-96"},
			wantErr: "must pass -s",
		},
		{
			name: "dangling -t", args: []string{"list-panes", "-s", "-F", "#{pane_id}", "-t"},
			wantErr: "-t with no value",
		},
		{
			name: "duplicate -t", args: []string{"list-panes", "-s", "-t", "=issue-96", "-t", "=issue-96", "-F", "#{pane_id}"},
			wantErr: "exactly one -t",
		},
		{
			// Not the exact-match form: tmux resolves a bare name by prefix and then by glob, so
			// an unanchored target can answer for a DIFFERENT session.
			name: "target is not the exact-match form", args: []string{"list-panes", "-s", "-t", "issue-96", "-F", "#{pane_id}"},
			wantErr: "exact-match form",
		},
		{
			// CROSS-FLAG, rejection side. -t consumes the second "-t", so the target VALUE is the
			// literal "-t" -- extraction succeeds and POLICY refuses, because "-t" is not the
			// exact-match target. Recorded as a rejection rather than an acceptance precisely
			// because extraction succeeding is not the contract being tested here; a bare
			// ["-t","-t"] is an extraction fact, and it lives at the parser level.
			name:    "target value spelling its own flag is extracted and then REFUSED",
			args:    []string{"list-panes", "-s", "-t", "-t", "-F", "#{pane_id}"},
			wantErr: "exact-match form",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateListPanesInvocation(tc.args, "=issue-96")
			if err == nil {
				t.Fatalf("must be rejected; got format %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("refusal must name the dimension that failed (%q), or the row proves only "+
					"that SOMETHING refused: %v", tc.wantErr, err)
			}
		})
	}

	// ANTI-VACUITY: well-formed invocations are ACCEPTED and their format returned unchanged.
	// Without these, a decider that refused everything would pass every row above.
	for _, tc := range []struct {
		name       string
		args       []string
		wantFormat string
	}{
		{
			name: "canonical invocation", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F", "#{pane_id}"},
			wantFormat: "#{pane_id}",
		},
		{
			// CROSS-FLAG, acceptance side. -F consumes the second "-F", so the FORMAT is the
			// literal "-F" and the invocation is otherwise canonical. Real tmux reads it exactly
			// this way, so the decider must accept it: the cross-flag walk must not turn a
			// legitimate-if-odd value into a refusal. Note this needs a real -s and a real -t in
			// the argv -- a bare ["-F","-F"] would refuse on scope and prove nothing about format.
			name: "format value spelling its own flag is accepted", args: []string{"list-panes", "-s", "-t", "=issue-96", "-F", "-F"},
			wantFormat: "-F",
		},
	} {
		t.Run("accepts/"+tc.name, func(t *testing.T) {
			got, err := validateListPanesInvocation(tc.args, "=issue-96")
			if err != nil {
				t.Fatalf("a well-formed invocation must be accepted: %v", err)
			}
			if got != tc.wantFormat {
				t.Errorf("format must be returned unchanged; got %q, want %q", got, tc.wantFormat)
			}
		})
	}
}

// #577 round 8: the in-band sentinel made this argv indistinguishable from a dangling flag. Real
// tmux accepts it as a literal (if useless) format value, so the decider must too -- an in-band
// marker cannot tell a domain value from an absence, which is why presence moved out of band.
//
// WHAT THIS ROW PROVES, narrowly: it falsifies REINTRODUCTION OF THE OLD SENTINEL specifically.
// It cannot prevent every future magic string, because it names one. The general property is
// carried by the representation itself -- flagValue keeps presence out of the value domain -- and
// a new in-band marker would need its own row. Stated so the row is not mistaken for a guard
// against the whole class.
func TestListPanesInvocationAcceptsAFormatEqualToTheOldSentinel(t *testing.T) {
	const looksLikeTheOldSentinel = "<dangling flag with no value>"

	got, err := validateListPanesInvocation([]string{"list-panes", "-s", "-t", "=issue-96", "-F", looksLikeTheOldSentinel}, "=issue-96")
	if err != nil {
		t.Fatalf("a literal format value must be accepted even when it spells the old sentinel: %v", err)
	}
	if got != looksLikeTheOldSentinel {
		t.Errorf("format must pass through unchanged; got %q", got)
	}
}

// EXTRACTION FACTS ONLY -- NOT POLICY. This table asserts what the argv SAID: which token became
// which flag's value once each option's arity is honoured. It deliberately makes no claim about
// whether an invocation is acceptable; every acceptance and rejection decision belongs to
// validateListPanesInvocation and is asserted against that function.
//
// The layer split is the r9 gate lesson made structural: a parser-direct assertion that NAMES a
// rejection proves a struct field was populated and nothing more, which is how a test survived
// deletion of the guard it was named for. Rows here are named for the fact, never for a verdict.
//
// #577 round 9 (second pass): ROLE across flags. Each row is an argv real tmux reads one way and
// independent per-flag scans read another, so each falsifies the independent-scan design rather
// than a detail of it.
func TestParseListPanesArgvAssignsRolesByPosition(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantScope   bool
		wantTarget  string
		wantFormat  string
		wantTargets int
		wantFormats int
	}{
		{
			// -F consumes "-t"; the REAL target follows. An independent -t scan reports
			// target="-t" and never sees =issue-96.
			name:      "format value spelling -t does not become a target",
			args:      []string{"list-panes", "-s", "-F", "-t", "-t", "=issue-96"},
			wantScope: true, wantFormat: "-t", wantTarget: "=issue-96",
			wantTargets: 1, wantFormats: 1,
		},
		{
			// -F consumes "-s", so scope is ABSENT. An independent hasFlag reports it present.
			name:      "scope flag appearing only as a consumed value is NOT scope",
			args:      []string{"list-panes", "-F", "-s", "-t", "=issue-96"},
			wantScope: false, wantFormat: "-s", wantTarget: "=issue-96",
			wantTargets: 1, wantFormats: 1,
		},
		{
			// The symmetric case: -t consumes "-F".
			name:      "target value spelling -F does not become a format",
			args:      []string{"list-panes", "-s", "-t", "-F", "-F", "#{pane_id}"},
			wantScope: true, wantTarget: "-F", wantFormat: "#{pane_id}",
			wantTargets: 1, wantFormats: 1,
		},
		{
			// Same-flag case from the first pass, kept: one option whose value spells itself.
			name:       "one option whose value spells its own flag",
			args:       []string{"-F", "-F"},
			wantFormat: "-F", wantFormats: 1,
		},
		{
			// The -t twin, kept HERE and only here. Extraction yields target "-t"; whether that
			// is acceptable is policy, and the decider refuses it (wantTarget equality). Asserting
			// this argv as an acceptance anywhere would be wrong.
			name:       "the -t twin: one target whose value spells its own flag",
			args:       []string{"-t", "-t"},
			wantTarget: "-t", wantTargets: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseListPanesArgv(tc.args)
			if got.Scope != tc.wantScope {
				t.Errorf("Scope = %v, want %v -- a consumed value must not be read as an option", got.Scope, tc.wantScope)
			}
			if len(got.Targets) != tc.wantTargets {
				t.Errorf("got %d targets (%v), want %d", len(got.Targets), got.Targets, tc.wantTargets)
			} else if tc.wantTarget != "" && (!got.Targets[0].HasValue || got.Targets[0].Value != tc.wantTarget) {
				t.Errorf("target = %+v, want %q", got.Targets[0], tc.wantTarget)
			}
			if len(got.Formats) != tc.wantFormats {
				t.Errorf("got %d formats (%v), want %d", len(got.Formats), got.Formats, tc.wantFormats)
			} else if tc.wantFormat != "" && (!got.Formats[0].HasValue || got.Formats[0].Value != tc.wantFormat) {
				t.Errorf("format = %+v, want %q", got.Formats[0], tc.wantFormat)
			}
		})
	}
}

// DELETED: TestListPanesFakeRejectsScopeThatIsOnlyAConsumedValue.
//
// It named a rejection by the fake and never called the fake or any decider -- it re-asserted row
// two of the parser table against parseListPanesArgv. Two defects in one test: it was a verbatim
// duplicate of coverage that already existed, and its NAME claimed the coverage that did not.
// Deleting the fake's scope guard left it green.
//
// Its property now lives as the "scope present only as a consumed value" row of
// TestListPanesInvocationDeciderRejectsEveryMalformedDimension, where it runs against the same
// function the fake calls and therefore fails when that clause is removed.
