package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// PR5 / #498: one ledger owns every recovery action for a pause. The half of that which
// survives as a comment and dies in code is "legacy transitions remain READABLE but must not
// remain an alternate WRITER path". A convention cannot enforce that; this does.
//
// The rule: exactly ONE production function may construct a transition record, and exactly ONE
// may create a reservation file. Every other site reads. A second writer is not a style
// problem -- two writers with two notions of the key is double delivery of an audited
// /goal resume, which is the worst outcome the #498 contract names.
//
// Structural, not textual, because this milestone has now produced five checks that accepted a
// shape where they needed a proof. The enforcement is over the AST: which functions contain a
// call that creates a reservation, not which files mention one.
func TestOnlyOneProductionSiteReservesARecoveryTransition(t *testing.T) {
	// THE SANCTIONED PUBLISHERS: THREE. Ratified, after a round trip worth recording.
	//
	// dev-2 counted FOUR and was describing the TRANSITIONAL state correctly: under a
	// constructor-only wiring, resume_goal.go's reserveResumeGoalTransition keeps its direct
	// publishGoalJSON at :770 and is a genuine fourth publisher. The ruling resolved it the other
	// way -- the wiring DELEGATES, so the three legacy transition functions hand their
	// publication to these three and no direct publishGoalJSON remains inside any transition
	// publisher. The set stays three because the end state has three, and the pin then forces the
	// full migration instead of blessing a half-done one.
	//
	// Enumerated rather than reasoned about. publishGoalJSON has five call sites; exactly three
	// touch a transition path (resume_goal.go:770 reserve, :950 bind, :981 consume). The other
	// two, goal_attempt.go:139 and :227, publish ATTEMPT and CLAIM paths and are not this rule's
	// business -- see the explicit non-transition allowlist below, which exists so that
	// "not our business" is a decision on the record rather than a gap.
	sanctioned := map[string]bool{
		"reserveRecoveryTransition":        true,
		"bindRecoveryTransitionGeneration": true,
		"consumeRecoveryTransition":        true,
	}

	// EXPLICIT NON-TRANSITION PUBLISHERS. Naming them is what lets the detector below be
	// DEFAULT-DENY: every function that publishes must be classified, so a new publisher added
	// tomorrow fails this test until someone decides which list it belongs in. Silence is not a
	// classification.
	// Names read from the tree, not guessed: I had written publishGoalAttempt/publishGoalClaim
	// from memory and both were wrong.
	nonTransition := map[string]bool{
		"createGoalAttempt": true, // goal_attempt.go:115, publishes at :139 -- attempt record
		"claimGoalAttempt":  true, // goal_attempt.go:209, publishes at :227 -- claim record
	}

	// THE PUBLICATION PRIMITIVE IS publishGoalJSON, NOT O_EXCL.
	//
	// The first version of this test looked for os.O_EXCL, citing briefs.go:185. That citation is
	// real and the idiom is real, but it is NOT the idiom this domain uses: publishGoalJSON
	// (goal_attempt.go:152) publishes by writing a same-directory candidate, fsyncing it, and
	// link(2)-ing it into place -- link is the atomic no-replace primitive, and losers see
	// ErrExist. grep confirms O_EXCL appears nowhere in the recovery-transition path.
	//
	// So the original detector would have found ZERO writers and fired its own
	// detection-is-broken guard. That guard working is the only reason this was noisy rather than
	// silent, and it is exactly the shape-not-proof failure again: I wrote the detector from an
	// idiom I assumed instead of from the CAS owner I could have read. An enforcement test keyed
	// on the wrong primitive enforces nothing about the thing it names.
	const publicationPrimitive = "publishGoalJSON"
	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	// DEFAULT-DENY, and this is the second correction to this detector.
	//
	// The previous version classified a function as a writer only if it ALSO mentioned an
	// identifier containing "transitionPath". That has a hole big enough to drive the whole rule
	// through: reserveRecoveryTransition RECEIVES its path as a parameter -- deliberately, so it
	// cannot be a second path owner -- and therefore mentions no such identifier. The single most
	// important writer in the design would have been INVISIBLE to the test guarding it, while the
	// legacy functions that derive their own paths stayed visible. Keying on "derives a path"
	// instead of "publishes" is the same shape-not-proof substitution as keying on O_EXCL.
	//
	// So: EVERY function that calls publishGoalJSON is collected, and every one must be
	// classified as either a sanctioned transition publisher or an explicit non-transition
	// publisher. An unclassified publisher FAILS. Silence is not a classification, and a new
	// publisher added tomorrow cannot slip in by simply not looking like the ones here.
	publishers := map[string]string{} // func name -> file
	found := map[string]bool{}

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || fn.Body == nil {
				continue
			}
			found[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				// The primitive is called bare within package cli; the SelectorExpr arm catches a
				// qualified or aliased reference so a move to another package cannot evade this.
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if fun.Name == publicationPrimitive {
						publishers[fn.Name.Name] = filepath.Base(path)
					}
				case *ast.SelectorExpr:
					if fun.Sel != nil && fun.Sel.Name == publicationPrimitive {
						publishers[fn.Name.Name] = filepath.Base(path)
					}
				}
				return true
			})
		}
	}

	// ANTI-VACUITY, three ways. Each guards a different way this test could quietly stop meaning
	// anything, and the O_EXCL episode is why there are three rather than one: that mistake was
	// caught ONLY because a floor fired.
	if len(publishers) == 0 {
		t.Fatalf("no function calls %s; the detection is broken, not the tree clean", publicationPrimitive)
	}
	for name := range sanctioned {
		if !found[name] {
			t.Fatalf("sanctioned publisher %q does not exist in the tree; this test is enforcing a "+
				"rule about a function that is not there", name)
		}
		if publishers[name] == "" {
			t.Errorf("sanctioned publisher %q exists but never calls %s.\nAfter the ruled delegation "+
				"the CAS layer is the ONLY thing that publishes; a sanctioned publisher that does not "+
				"publish means the delegation did not land.", name, publicationPrimitive)
		}
	}

	for name, file := range publishers {
		if sanctioned[name] || nonTransition[name] {
			continue
		}
		t.Errorf("%s (%s) calls %s but is neither a sanctioned transition publisher nor a declared "+
			"non-transition publisher.\nEvery publisher must be classified deliberately. If this is a "+
			"transition writer, it is an ALTERNATE WRITER: two writers means two notions of the claim "+
			"key, and for /goal resume that is DOUBLE DELIVERY of an audited action (#498). If it is "+
			"not, add it to nonTransition with a reason.", name, file, publicationPrimitive)
	}
}

// The companion: every transition record must be built by one constructor, so no site can
// assemble a record that skips the fields the key depends on.
//
// This is the #572 lesson applied before the fact rather than after. There, two struct
// literals bypassed bootstrapack.NewExpectation and recorded an empty LaunchID; here a literal
// that omits PauseGeneration or PreclaimFingerprint would produce a record the key derivation
// cannot reproduce -- unmatched on read, so treated as absent, so a second delivery.
func TestNoProductionCodeBuildsRecoveryTransitionLiterals(t *testing.T) {
	const recordType = "resumeGoalTransitionRecord"
	const constructor = "newRecoveryTransitionRecord"

	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var offenders []string
	constructorFound := false
	inspected := 0
	// Calls to the constructor from production, NOT counting its own declaration. See the floor
	// below for why the count and not merely the existence.
	constructorCalls := 0

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		inspected++
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			if fn.Name.Name == constructor {
				constructorFound = true
				// The constructor is the one place allowed to build the literal.
				continue
			}
			if fn.Body == nil {
				continue
			}
			// SANCTIONED-INPUT PASS, first: a literal supplied as the Base field of a
			// recoveryTransitionInput is the constructor's INPUT, not a bypass of it.
			//
			// The wiring exposed this and it is worth stating plainly, because the pin was
			// UNSATISFIABLE as first written: the constructor's signature takes
			// `Base resumeGoalTransitionRecord`, so every correct caller must build a populated
			// literal to hand it. A rule forbidding all populated literals therefore forbade the
			// only correct way to call the thing it was protecting. Nothing detected this until
			// real production code had to satisfy both at once -- an enforcement test can be
			// self-consistent and still describe a world no caller can live in.
			sanctionedLit := map[token.Pos]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, isLit := n.(*ast.CompositeLit)
				if !isLit {
					return true
				}
				if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "recoveryTransitionInput" {
					return true
				}
				for _, elt := range lit.Elts {
					kv, isKV := elt.(*ast.KeyValueExpr)
					if !isKV {
						continue
					}
					key, isKey := kv.Key.(*ast.Ident)
					if !isKey || key.Name != "Base" {
						continue
					}
					if inner, ok := kv.Value.(*ast.CompositeLit); ok {
						sanctionedLit[inner.Pos()] = true
					}
					// A Base supplied as a bare identifier (base := ...; Base: base) is the
					// common shape; the assignment's literal is caught by position below only if
					// it is inline, so identifier-valued Base is handled by the assignment scan.
					if ident, ok := kv.Value.(*ast.Ident); ok && ident.Obj != nil {
						if assign, ok := ident.Obj.Decl.(*ast.AssignStmt); ok {
							for _, rhs := range assign.Rhs {
								if inner, ok := rhs.(*ast.CompositeLit); ok {
									sanctionedLit[inner.Pos()] = true
								}
							}
						}
					}
				}
				return true
			})

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, isCall := n.(*ast.CallExpr); isCall {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == constructor {
						constructorCalls++
					}
				}
				lit, isLit := n.(*ast.CompositeLit)
				if !isLit {
					return true
				}
				id, isID := lit.Type.(*ast.Ident)
				if !isID || id.Name != recordType {
					return true
				}
				// An EMPTY literal is a zero value for a read target, not a constructed
				// record; only a populated one bypasses the constructor.
				if len(lit.Elts) == 0 {
					return true
				}
				if sanctionedLit[lit.Pos()] {
					return true
				}
				offenders = append(offenders, fset.Position(lit.Pos()).String())
				return true
			})
		}
	}

	if !constructorFound {
		t.Fatalf("constructor %q does not exist; this test would permit every literal", constructor)
	}
	if inspected < 50 {
		t.Fatalf("parsed only %d non-test files; the walk is broken", inspected)
	}
	// THE FLOOR THAT MATTERS, and the one the first draft was missing. `inspected` proves the
	// WALK ran; it says nothing about whether the rule has any subject. If no production code
	// calls the constructor, then "no literal bypasses the constructor" is true the way it is true
	// of a function nobody uses -- vacuously -- and this test would sit green through exactly the
	// regression it exists to prevent: a wiring reverted, a call replaced by a literal that the
	// literal-walk happened to miss, or a PR5 that never landed its writer at all.
	//
	// Counting FILES was the same shape as counting the string instead of the thing. The subject
	// of the rule is the constructor's use, so that is what the floor counts.
	//
	// NOTE FOR THE INTERIM: until resume_goal.go's redelivery writer is routed through the
	// constructor, this floor FAILS. That is intended and is the point of writing the pin before
	// the wiring -- the pin goes green when the wiring lands, so it cannot be shaped to fit
	// whatever the wiring happened to produce.
	if constructorCalls < 1 {
		t.Fatalf("no production code calls %s; the no-literal rule has no subject and this test is "+
			"vacuously green. Either the wiring has not landed yet, or it was reverted.", constructor)
	}
	if len(offenders) != 0 {
		t.Errorf("production code builds a populated %s literal at %v; use %s so PauseGeneration and "+
			"PreclaimFingerprint are always recorded. A record missing them cannot be matched by the key "+
			"derivation, reads as ABSENT, and permits a second delivery.", recordType, offenders, constructor)
	}
}
