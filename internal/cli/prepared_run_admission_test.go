package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #573: `team resume` previewed prepared and staged actors as will-launch and emitted a
// command `agent up` then REFUSED, because admission required a spawn reservation the preview
// knew nothing about. The condition was inlined at five sites and the planner was not one of
// them.
//
// This table asserts the VERDICTS. The structural test below asserts that both surfaces
// actually consult this predicate rather than agreeing by coincidence. Neither alone is
// sufficient: correct verdicts in a predicate nobody calls fixes nothing, and a shared call
// whose verdicts are wrong is worse than none.
func TestPreparedRunActorAdmissionClassifiesEveryState(t *testing.T) {
	// GoalNamespace and GoalDigest are REQUIRED here, not decoration: #579 round 3 F1 makes
	// Bindable depend on the record's token AGREEING with the accepted generation, and
	// preparedRunTokenFromSnapshot derives that generation from these fields. Omitting them made
	// the fixture unable to model agreement at all -- every bindable row failed, and it would have
	// been easy to misread that as the F1 fix being wrong rather than the fixture being incomplete.
	manifest := preparedRunManifest{
		Generation:    "g-7",
		Project:       "/repo",
		Profile:       "squad",
		Session:       "v2-25-0",
		GoalNamespace: "squad/v2-25-0",
		GoalDigest:    "gd-7",
		StagedRoster:  []string{"qa"},
	}
	// A record the managed path can ACTUALLY bind: complete generation AND the reserved launch
	// attempt. #579 finding 1 -- the attempt was missing from this fixture and from the
	// predicate, so a record like this previewed bindable and managed resume then refused it.
	bound := &launch.Record{
		PreparedRunGeneration:    "g-7",
		PreparedRunDigest:        "d-7",
		PreparedRunGoalNamespace: "squad/v2-25-0",
		PreparedRunGoalDigest:    "gd-7",
		PreparedRunLaunchAttempt: "a-1",
	}
	unbound := &launch.Record{}

	for _, tc := range []struct {
		name         string
		prepared     bool
		role         string
		rec          *launch.Record
		wantRequired bool
		wantStaged   bool
		wantBindable bool
		wantRecovery string
	}{
		{
			name: "not a prepared session at all", prepared: false, role: "qa", rec: nil,
			wantRequired: false,
		},
		{
			name: "staged actor with no record", prepared: true, role: "qa", rec: nil,
			wantRequired: true, wantStaged: true, wantRecovery: "--staged-spawn",
		},
		{
			name: "prepared non-staged actor with no record", prepared: true, role: "cto", rec: nil,
			wantRequired: true, wantStaged: false, wantRecovery: "run start",
		},
		{
			name: "staged actor whose record carries no token", prepared: true, role: "qa", rec: unbound,
			wantRequired: true, wantStaged: true, wantRecovery: "--staged-spawn",
		},
		{
			// The row that makes the predicate record-aware. --exec CAN bind this through the
			// managed restore path, so blocking it would refuse something that works.
			name: "staged actor whose record carries a complete claim-bound token", prepared: true, role: "qa", rec: bound,
			wantRequired: true, wantStaged: true, wantBindable: true, wantRecovery: "agent resume",
		},
		{
			name: "prepared actor whose record carries a complete token", prepared: true, role: "cto", rec: bound,
			wantRequired: true, wantStaged: false, wantBindable: true, wantRecovery: "agent resume",
		},
		// #579 finding 6: the rows below were all missing, and every one of them is a state the
		// predicate can actually see. The table was six happy-ish rows.
		{
			// finding 1: complete generation, NO reserved attempt. Previewed will-launch and
			// managed resume then refused it.
			name: "complete generation but no reserved attempt is NOT bindable", prepared: true, role: "qa",
			rec:          &launch.Record{PreparedRunGeneration: "g-7", PreparedRunDigest: "d-7", PreparedRunGoalNamespace: "squad/v2-25-0", PreparedRunGoalDigest: "gd-7"},
			wantRequired: true, wantStaged: true, wantBindable: false, wantRecovery: "--staged-spawn",
		},
		{
			name: "partial token is not bindable", prepared: true, role: "qa",
			rec:          &launch.Record{PreparedRunGeneration: "g-7", PreparedRunLaunchAttempt: "a-1"},
			wantRequired: true, wantStaged: true, wantBindable: false, wantRecovery: "--staged-spawn",
		},
		{
			// finding 2: contradictory evidence -- a token-bearing record in a session with no
			// accepted generation. Must be governed, never a plain launch.
			name: "token-bearing record in an UNPREPARED session is governed", prepared: false, role: "qa", rec: bound,
			wantRequired: true, wantStaged: false, wantBindable: false, wantRecovery: "run start",
		},
		// #579 round 3. Each row below is the FALSIFYING INPUT for a claim my round-2 commit
		// message made and my round-2 checks could not disprove, because those checks confirmed a
		// line existed rather than testing what would make the sentence false.
		{
			// F1's falsifying input: a COMPLETE, claim-bound token from a SUPERSEDED generation.
			// Shape-perfect, agreement-absent. Round 2 called this bindable and offered
			// `agent resume <role>`; exec-side validation refuses it.
			name: "stale-generation token is NOT bindable", prepared: true, role: "qa",
			rec: &launch.Record{
				PreparedRunGeneration: "g-OLD", PreparedRunDigest: "d-OLD",
				PreparedRunGoalNamespace: "squad/v2-25-0", PreparedRunGoalDigest: "gd-OLD",
				PreparedRunLaunchAttempt: "a-1",
			},
			wantRequired: true, wantStaged: true, wantBindable: false, wantRecovery: "--staged-spawn",
		},
		{
			// F2's falsifying input: ONLY an orphaned launch attempt. token.empty() is TRUE here --
			// it checks only the four generation fields -- so round 2 let this fall through as an
			// ordinary actor in an unprepared session.
			name: "attempt-only record in an UNPREPARED session is governed", prepared: false, role: "qa",
			rec:          &launch.Record{PreparedRunLaunchAttempt: "a-orphan"},
			wantRequired: true, wantStaged: false, wantBindable: false, wantRecovery: "run start",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adm := preparedRunActorAdmission(manifest, "d-7", tc.prepared, tc.role, "handle-"+tc.role, tc.rec)

			if adm.required() != tc.wantRequired {
				t.Errorf("required() = %v, want %v", adm.required(), tc.wantRequired)
			}
			if adm.Staged != tc.wantStaged {
				t.Errorf("Staged = %v, want %v", adm.Staged, tc.wantStaged)
			}
			if adm.Bindable != tc.wantBindable {
				t.Errorf("Bindable = %v, want %v", adm.Bindable, tc.wantBindable)
			}
			if !tc.wantRequired {
				// A non-prepared session must not produce refusal text at all; an unused
				// Reason string is how a caller ends up rendering a refusal for a fine actor.
				if adm.Reason != "" || adm.Recovery != "" {
					t.Errorf("a non-required verdict must carry no Reason/Recovery; got %q / %q", adm.Reason, adm.Recovery)
				}
				return
			}
			// The reason must name the ACTOR, so an operator with several blocked members can
			// tell which row is which.
			if !strings.Contains(adm.Reason, tc.role) {
				t.Errorf("Reason must name the role %q: %s", tc.role, adm.Reason)
			}
			if tc.wantStaged && !strings.Contains(adm.Reason, manifest.Generation) {
				t.Errorf("a staged refusal must name the generation whose reservation is required: %s", adm.Reason)
			}
			if !strings.Contains(adm.Recovery, tc.wantRecovery) {
				t.Errorf("Recovery must offer %q; got %q", tc.wantRecovery, adm.Recovery)
			}
			// The recovery command must be runnable, not a description of one.
			if !strings.HasPrefix(adm.Recovery, "amq-squad ") {
				t.Errorf("Recovery must be an exact command starting with amq-squad; got %q", adm.Recovery)
			}
			// #579 round 3 fold-in: a BLANK field must render as a visible placeholder, never as
			// '' -- an empty-quoted flag looks filled and fails at runtime.
			if strings.Contains(adm.Recovery, "''") {
				t.Errorf("Recovery contains an empty-quoted argument, which looks filled and is not: %q", adm.Recovery)
			}
		})
	}
}

// #579 finding 5: the previous version of this test asserted only that a CALL EXPRESSION
// exists, so it passed while admission called the predicate purely for its Reason string and
// computed its own verdict. A call is not consumption. That is the same shape-not-proof
// mistake this milestone has now produced five times, and it is why the half-consolidation
// shipped with a green test.
//
// WHAT THIS PROVES, stated exactly (#579 round 4 R4-3): required() sits in the CONDITION of an
// if at each site, so the predicate governs a branch rather than being evaluated for a field.
//
// WHAT IT DOES NOT PROVE, declared rather than chased: that the guarded body actually refuses.
// A body of `return nil` satisfies this check. Proving "the body provably refuses" in the AST is
// a ladder with no top rung -- I climbed four rungs on this one test across rounds 2, 3 and 4,
// each time closing the mutation I had just been shown and leaving the adjacent one. Ruled to
// stop here: refusal semantics are reviewed AT THE SITE, and the verdict table plus the removal
// proofs cover the behaviour directly.
//
// The honest description of what a check proves is worth more than an implied stronger claim,
// because the implied version is what a future reader relies on.
func TestBothSurfacesConsumeThePredicateVerdict(t *testing.T) {
	for _, tc := range []struct {
		file   string
		symbol string
	}{
		{"launch.go", "preparedRunActorAdmission"},
		{"team_resume.go", "preparedRunAdmissionForMember"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, filepath.Join(".", tc.file), nil, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// Find the identifier the call result is assigned to, then require that identifier
			// to appear in a call to required() somewhere in the same file.
			var assigned []string
			ast.Inspect(parsed, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, rhs := range as.Rhs {
					call, isCall := rhs.(*ast.CallExpr)
					if !isCall {
						continue
					}
					id, isID := call.Fun.(*ast.Ident)
					if !isID || id.Name != tc.symbol {
						continue
					}
					if len(as.Lhs) > 0 {
						if target, ok := as.Lhs[0].(*ast.Ident); ok {
							assigned = append(assigned, target.Name)
						}
					}
				}
				return true
			})
			if len(assigned) == 0 {
				t.Fatalf("%s never assigns the result of %s; #573 requires the shared predicate to "+
					"decide, and a result that is not bound cannot be consumed", tc.file, tc.symbol)
			}

			// #579 round 3 F3: the previous version accepted ANY same-named required() receiver
			// anywhere in the file, so a dead `_ = adm.required()` sitting beside an independent
			// verdict computer passed. Consumption is not control.
			//
			// The verdict must appear in the CONDITION of an if, so the predicate governs a
			// conditional rather than being evaluated and discarded. Refusal semantics are reviewed
			// at the site, not proven here -- see this test's header for the declared bound. This is
			// the seventh shape-versus-proof item in this milestone and the review reopened for
			// safety anyway, so the polarity part was fixed rather than declared.
			consumed := false
			ast.Inspect(parsed, func(n ast.Node) bool {
				ifs, ok := n.(*ast.IfStmt)
				if !ok || ifs.Cond == nil {
					return true
				}
				// Does this if's CONDITION call required() on one of the bound identifiers?
				governed := false
				ast.Inspect(ifs.Cond, func(c ast.Node) bool {
					call, isCall := c.(*ast.CallExpr)
					if !isCall {
						return true
					}
					sel, isSel := call.Fun.(*ast.SelectorExpr)
					if !isSel || sel.Sel == nil || sel.Sel.Name != "required" {
						return true
					}
					recv, isID := sel.X.(*ast.Ident)
					if !isID {
						return true
					}
					for _, name := range assigned {
						if recv.Name == name {
							governed = true
						}
					}
					return true
				})
				if !governed {
					return true
				}
				// And does that if's body return a value or set the blocked action? This is a
				// SHAPE check, not proof of refusal semantics: `return nil` satisfies it. Refusal
				// semantics are reviewed at the site, per the ruling.
				ast.Inspect(ifs.Body, func(b ast.Node) bool {
					switch node := b.(type) {
					case *ast.ReturnStmt:
						if len(node.Results) > 0 {
							consumed = true
						}
					case *ast.Ident:
						if node.Name == "resumeBlocked" {
							consumed = true
						}
					}
					return true
				})
				return true
			})
			if !consumed {
				t.Errorf("%s calls %s but its required() verdict never appears in the CONDITION of an if -- "+
					"a dead `_ = x.required()` beside an independent verdict computer would look identical. "+
					"The predicate must govern a conditional, or a predicate change moves one surface and "+
					"not the other, which is the disagreement #573 exists to end. Refusal semantics "+
					"themselves are reviewed at the site, not proven here.",
					tc.file, tc.symbol)
			}
		})
	}
}

// The locked requirement for #573 was a SHARED predicate, not a parallel reimplementation, so
// that preview and admission cannot disagree. This asserts the sharing structurally: both the
// admission path and the resume planner must reference the predicate.
//
// A behavioural end-to-end test would be satisfiable by two independent implementations that
// happen to agree today, which is exactly how #573 arose -- the condition was inlined five
// times and stayed consistent until one copy was missing.
//
// DECLARED BOUND: this proves both surfaces CALL the predicate. It does not execute a full
// resume against an on-disk prepared session, so it cannot prove the planner's blocked row
// renders end-to-end; the verdict table above covers the classification and the planner's
// field-setting is reviewed by inspection at the call site.
func TestPreviewAndAdmissionShareOnePredicate(t *testing.T) {
	const predicate = "preparedRunActorAdmission"
	const loader = "preparedRunAdmissionForMember"

	want := map[string]string{
		// admission -- the authority whose refusal the preview must not contradict
		"launch.go": predicate,
		// the resume planner, via the manifest-loading wrapper
		"team_resume.go": loader,
	}

	for file, symbol := range want {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, filepath.Join(".", file), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		found := false
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, isID := call.Fun.(*ast.Ident); isID && id.Name == symbol {
				found = true
			}
			return true
		})
		if !found {
			t.Errorf("%s does not call %s. #573 requires preview and admission to consult ONE "+
				"predicate; a second copy of the condition is how they came to disagree.", file, symbol)
		}
	}

	// Anti-vacuity: the loader must itself route through the predicate, or the planner would be
	// calling a wrapper that answers independently and the "shared" claim would be hollow.
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(".", "prepared_run_admission.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse predicate file: %v", err)
	}
	routed := false
	ast.Inspect(parsed, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != loader {
			return true
		}
		ast.Inspect(fn, func(x ast.Node) bool {
			call, isCall := x.(*ast.CallExpr)
			if !isCall {
				return true
			}
			if id, isID := call.Fun.(*ast.Ident); isID && id.Name == predicate {
				routed = true
			}
			return true
		})
		return true
	})
	if !routed {
		t.Errorf("%s must delegate to %s; otherwise the planner and admission answer independently "+
			"while appearing to share a definition", loader, predicate)
	}
}

// #579 round 3 F4's falsifying input. The round-2 check asserted the command prefix and the
// --staged-spawn flag, both of which pass with the binary missing entirely or unquoted -- I
// checked the FLAGS and never the interpolated value, then claimed every emitted form was
// checked against the real command surface.
//
// This uses a binary path that BREAKS an unquoted command: a space plus a metacharacter.
func TestStagedRecoveryQuotesTheInterpolatedBinary(t *testing.T) {
	manifest := preparedRunManifest{
		Generation: "g-7", Project: "/repo", Profile: "squad", Session: "v2-25-0",
		GoalNamespace: "squad/v2-25-0", GoalDigest: "gd-7",
		StagedRoster: []string{"qa"},
	}
	rec := &launch.Record{Binary: "/opt/my tools/codex;rm -rf x"}

	adm := preparedRunActorAdmission(manifest, "d-7", true, "qa", "qa", rec)

	if !strings.Contains(adm.Recovery, shellQuote(rec.Binary)) {
		t.Errorf("the interpolated binary must be shell-quoted, or a path with spaces malforms the\n"+
			"command and a metacharacter becomes SYNTAX when the operator copies it.\ngot: %s", adm.Recovery)
	}
	// The raw form must NOT appear unquoted: asserting the quoted form alone would also pass if
	// both were present.
	if strings.Contains(adm.Recovery, "codex;rm -rf x") && !strings.Contains(adm.Recovery, shellQuote(rec.Binary)) {
		t.Errorf("recovery carries the raw unquoted binary: %s", adm.Recovery)
	}
}

// #579 round 4 R4-1: a LOADER-LEVEL falsifier. The round-3 stale-generation row calls the
// predicate directly with a literal digest, so it proves the predicate and says nothing about
// the CALL-SITE THREADING -- a loader passing a wrong or record-derived digest stayed green.
// My falsifier proved the function; the claim was about the system.
//
// This drives preparedRunAdmissionForMember against REAL on-disk state where the record's token
// matches a SUPERSEDED generation.
//
// SCOPE, per the ruling (#579 r5 R5-3): falsifier-proven AT THE LOADER. It says nothing about
// the admission site, whose digest is INERT by construction (rec = nil) and pinned separately by
// TestAdmissionPassesNoRecordSoTheDigestIsProvablyInert. The earlier wording -- "fails if either
// call site threads the wrong digest" -- was written before that ruling and is false under it.
//
// DECLARED BOUND (#579 r5 R5-1): this row bites a loader threading a WRONG-VALUED digest,
// because the before-assertion requires Bindable to be true on the current generation. It does
// NOT bite a loader threading rec.PreparedRunDigest: the before-check passes (the values are
// equal by fixture construction) and the after-check still rejects, because republishing rotates
// the GENERATION ID and samePreparedRunGeneration compares that independently.
//
// The fixture that would bite it -- same generation id, different manifest digest -- CANNOT BE
// BUILT through the sanctioned writer: publishPreparedRunGeneration installs the generation
// manifest with durableCreateExclusive, which uses os.Link and therefore fails with EEXIST
// rather than replacing. One generation id publishes exactly once, by design. Hand-writing the
// artifacts would fabricate a state the system cannot produce, which is worse evidence than an
// honest bound. So the record-derived mutation is closed STRUCTURALLY instead, by
// TestLoaderThreadsTheProjectionDigestNotTheRecords.
func TestLoaderRefusesBindableForASupersededGeneration(t *testing.T) {
	dir, manifest, token := preparedRunStateFixture(t)
	attempt, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token)
	if err != nil {
		t.Fatalf("reserve launch: %v", err)
	}
	token.LaunchAttempt = attempt

	// Anti-vacuity: the record's token must be bindable-shaped BEFORE the generation moves, or
	// this test would pass for the wrong reason -- an unbindable token is refused anyway.
	preRec := &launch.Record{
		PreparedRunGeneration:    token.Generation,
		PreparedRunDigest:        token.ManifestDigest,
		PreparedRunGoalNamespace: token.GoalNamespace,
		PreparedRunGoalDigest:    token.GoalDigest,
		PreparedRunLaunchAttempt: token.LaunchAttempt,
	}
	before, err := preparedRunAdmissionForMember(dir, team.DefaultProfile, "prepared", manifest.Lead, manifest.Lead, preRec)
	if err != nil {
		t.Fatalf("loader on the current generation: %v", err)
	}
	if !before.Bindable {
		t.Fatalf("fixture is not bindable on the CURRENT generation, so the supersede case below "+
			"would pass for the wrong reason: %+v", before)
	}

	// Now publish a NEW generation. The record still carries the OLD one.
	republishPreparedRunManifestForTest(t, manifest)

	after, err := preparedRunAdmissionForMember(dir, team.DefaultProfile, "prepared", manifest.Lead, manifest.Lead, preRec)
	if err != nil {
		t.Fatalf("loader on the superseded generation: %v", err)
	}
	if after.Bindable {
		t.Error("a record whose token names a SUPERSEDED generation must not be Bindable: preview would " +
			"offer `agent resume` and exec-side validation would refuse it -- the allow-in-preview/" +
			"refuse-on-exec defect this predicate exists to kill")
	}
	if !after.required() {
		t.Error("a superseded-generation actor is still governed and must be blocked, not planned fresh")
	}
}

// #579 round 4 R4-2: the ” assertion never received a BLANK field. Every table row reuses the
// populated manifest, so reverting any helper to shellQuote("") stayed green -- an anti-vacuity
// check that was itself vacuous, which is the sharpest version of this milestone's recurring
// mistake.
//
// The zero manifest is the falsifying input: it is exactly the state the contradictory-evidence
// branch sees, because that branch fires when NO accepted manifest exists.
func TestUnpreparedRecoveryRendersPlaceholdersNotEmptyQuotes(t *testing.T) {
	// Zero manifest: no Project, Profile or Session at all.
	adm := preparedRunActorAdmission(preparedRunManifest{}, "", false, "qa", "qa-handle",
		&launch.Record{PreparedRunLaunchAttempt: "a-orphan"})

	if !adm.required() {
		t.Fatal("a token-bearing record in an unprepared session must be governed")
	}
	if strings.Contains(adm.Recovery, "''") {
		t.Errorf("recovery contains an EMPTY-QUOTED argument, which looks filled and fails at "+
			"runtime -- the executable-not-plausible rule this PR asserts:\n%s", adm.Recovery)
	}
	for _, want := range []string{"<project>", "<profile>", "<session>"} {
		if !strings.Contains(adm.Recovery, want) {
			t.Errorf("a blank field must render as the visible placeholder %s:\n%s", want, adm.Recovery)
		}
	}
}

// #579 round 4 R4-4: Contains(shellQuote(binary)) is satisfied even when an ADDITIONAL unquoted
// copy sits beside the quoted one, and the second Contains I wrote to catch that had the same
// hole. Contains cannot prove absence.
//
// Exact equality is the only assertion that cannot be fooled: it pins the WHOLE emitted command,
// so any extra, missing or reordered token fails.
func TestStagedRecoveryIsExactlyTheExpectedCommand(t *testing.T) {
	manifest := preparedRunManifest{
		Generation: "g-7", Project: "/repo", Profile: "squad", Session: "v2-25-0",
		GoalNamespace: "squad/v2-25-0", GoalDigest: "gd-7",
		StagedRoster: []string{"qa"},
	}
	rec := &launch.Record{Binary: "/opt/my tools/codex"}

	adm := preparedRunActorAdmission(manifest, "d-7", true, "qa", "qa-handle", rec)

	want := "amq-squad agent up " + shellQuote(rec.Binary) + " --role " + shellQuote("qa") +
		" --staged-spawn --staged-claim <exact active claim ID from: amq-squad status --json>"
	if adm.Recovery != want {
		t.Errorf("staged recovery must match EXACTLY -- Contains would accept an extra unquoted copy\n got: %s\nwant: %s", adm.Recovery, want)
	}
}

// #579 round 4, ruled pin. The admission site passes rec = nil, so Bindable is false there
// regardless of the digest and the digest argument is INERT at that site. That is correct --
// admission decides about an IN-PROCESS token, not a persisted one -- but it means the claim
// "the digest is threaded at both call sites" is weaker than it sounds, and mutating the
// admission digest leaves every test green.
//
// A comment saying so is how declarations die: four of today's findings were a comment
// outliving its code. This pins the inertness STRUCTURALLY, which gives it a falsifying input:
// the moment anyone passes a persisted record at admission, this fails and forces a ruling --
// which is exactly the moment the digest would stop being inert and the threading would start
// to matter.
func TestAdmissionPassesNoRecordSoTheDigestIsProvablyInert(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(".", "launch.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse launch.go: %v", err)
	}

	calls := 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "preparedRunActorAdmission" {
			return true
		}
		calls++
		if len(call.Args) == 0 {
			t.Errorf("%s: call has no arguments", fset.Position(call.Pos()))
			return true
		}
		last := call.Args[len(call.Args)-1]
		lit, isIdent := last.(*ast.Ident)
		if !isIdent || lit.Name != "nil" {
			t.Errorf("%s: admission must pass the NIL LITERAL for the launch record.\n"+
				"It passes %s instead, which means a persisted record now reaches admission -- so the "+
				"digest argument is no longer inert there and the threading needs its own falsifier. "+
				"Get a ruling rather than deleting this check.",
				fset.Position(last.Pos()), typeName(last))
		}
		return true
	})

	// Anti-vacuity: if the call disappears or is renamed, this test must fail rather than pin
	// nothing. Exactly one admission call site is expected.
	if calls != 1 {
		t.Fatalf("expected exactly 1 preparedRunActorAdmission call in launch.go, found %d; the pin is "+
			"either blind or the call site moved", calls)
	}
}

// typeName renders an expression's kind for the failure message above, so the reader learns
// WHAT was passed instead of just that something was.
func typeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return "identifier " + v.Name
	case *ast.CallExpr:
		return "a call expression"
	case *ast.UnaryExpr:
		return "a unary expression (likely &record)"
	default:
		return "a non-nil expression"
	}
}

// #579 r5 R5-1, structural closure of the mutation no behavioural fixture can reach.
//
// The loader must pass THE PROJECTION'S digest to the predicate -- the value returned by
// preparedRunManifestForProjection -- and not one derived from the launch record. A
// record-derived digest makes the comparison self-referential: the token is checked against a
// snapshot built from its own field, so it always agrees and Bindable is true for a superseded
// generation.
//
// Pinned structurally because the behavioural falsifier is impossible: the fixture would need
// one generation id published twice with different content, and publishPreparedRunGeneration
// installs generation artifacts with os.Link, which fails EEXIST rather than replacing. Same
// reasoning as the admission inertness pin -- when a property cannot be given a falsifying
// INPUT, give it a falsifying EDIT.
func TestLoaderThreadsTheProjectionDigestNotTheRecords(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(".", "prepared_run_admission.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	checked := false
	ast.Inspect(parsed, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "preparedRunAdmissionForMember" || fn.Body == nil {
			return true
		}
		// The digest identifier the projection binds, taken from the assignment rather than
		// assumed to be called "digest" -- a rename must not silently disable this pin.
		projectionDigest := ""
		ast.Inspect(fn.Body, func(x ast.Node) bool {
			as, isAssign := x.(*ast.AssignStmt)
			if !isAssign || len(as.Rhs) != 1 || len(as.Lhs) < 2 {
				return true
			}
			call, isCall := as.Rhs[0].(*ast.CallExpr)
			if !isCall {
				return true
			}
			id, isID := call.Fun.(*ast.Ident)
			if !isID || id.Name != "preparedRunManifestForProjection" {
				return true
			}
			if ident, ok := as.Lhs[1].(*ast.Ident); ok {
				projectionDigest = ident.Name
			}
			return true
		})
		if projectionDigest == "" || projectionDigest == "_" {
			t.Fatalf("the loader does not bind the projection's digest at all, so it cannot be passing it")
		}

		ast.Inspect(fn.Body, func(x ast.Node) bool {
			call, isCall := x.(*ast.CallExpr)
			if !isCall {
				return true
			}
			id, isID := call.Fun.(*ast.Ident)
			if !isID || id.Name != "preparedRunActorAdmission" || len(call.Args) < 2 {
				return true
			}
			checked = true
			arg, isIdent := call.Args[1].(*ast.Ident)
			if !isIdent || arg.Name != projectionDigest {
				t.Errorf("%s: the loader must pass the PROJECTION's digest (%s) as the digest argument.\n"+
					"A record-derived digest makes the generation comparison self-referential -- the token is "+
					"checked against a snapshot built from its own field, always agrees, and a SUPERSEDED "+
					"generation reads as Bindable. No behavioural fixture can catch that, because one "+
					"generation id cannot be published twice (os.Link EEXIST), which is why this is pinned.",
					fset.Position(call.Args[1].Pos()), projectionDigest)
			}
			return true
		})
		return true
	})

	if !checked {
		t.Fatal("found no preparedRunActorAdmission call inside preparedRunAdmissionForMember; the pin is blind")
	}
}
