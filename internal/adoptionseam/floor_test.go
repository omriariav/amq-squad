package adoptionseam

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

const adoptionFloorModulePath = "github.com/avivsinai/agent-message-queue"

var adoptionFloorRequireLine = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(adoptionFloorModulePath) + `\s+(v\S+)\s*$`)

// TestPinnedAMQModuleAtOrAboveAdoptionFloor is the real gh#746 floor guard.
// launchapi's negotiable Compatibility()/Negotiate() contract did not move
// between v0.70.0 and v0.73.0 (see AdoptionFloorContractSemver's doc
// comment), so Negotiate cannot detect an older pinned module -- the actual
// floor for this in-process path is the go.mod pin itself, since launchapi
// and its internals run inside this binary.
//
// This reads go.mod directly rather than runtime/debug.ReadBuildInfo():
// verified empirically that `go test -c` binaries in this toolchain
// (go1.25.12 darwin/arm64) embed zero dependency entries at all -- confirmed
// systemic (internal/cli's test binary has the same empty Deps), not
// specific to this package, by comparing against `go version -m` on a
// regular `go build` binary, which correctly lists all 23 deps including
// this one. ReadBuildInfo would make this test either always fail or
// silently prove nothing; go.mod is the actual, unambiguous source of truth
// for what's pinned regardless of that quirk.
func TestPinnedAMQModuleAtOrAboveAdoptionFloor(t *testing.T) {
	pinned := adoptionFloorPinnedVersion(t)
	got, ok := parseAdoptionFloorSemver(pinned)
	if !ok {
		t.Fatalf("could not parse pinned module version %q as semver", pinned)
	}
	want, ok := parseAdoptionFloorSemver(AdoptionFloorAMQVersion)
	if !ok {
		t.Fatalf("could not parse AdoptionFloorAMQVersion %q as semver", AdoptionFloorAMQVersion)
	}
	if compareAdoptionFloorSemver(got, want) < 0 {
		t.Fatalf("pinned %s version %s is below the documented adoption floor %s", adoptionFloorModulePath, pinned, AdoptionFloorAMQVersion)
	}
}

// adoptionFloorPinnedVersion locates the repo-root go.mod relative to this
// test file's own source location (robust to the package moving, and to
// where `go test` happens to set its working directory) and extracts the
// pinned agent-message-queue require-line version.
func adoptionFloorPinnedVersion(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	dir := filepath.Dir(thisFile)
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(candidate)
		if err == nil {
			m := adoptionFloorRequireLine.FindSubmatch(data)
			if m == nil {
				t.Fatalf("%s has no require line for %s", candidate, adoptionFloorModulePath)
			}
			return string(m[1])
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate the repo-root go.mod by walking up from this test file")
	return ""
}

func parseAdoptionFloorSemver(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-") // drop any pre-release/pseudo-version suffix
	v, _, _ = strings.Cut(v, "+") // drop any build-metadata suffix
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func compareAdoptionFloorSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// TestNegotiateAdoptionFloorSatisfiedByPinnedAMQ is the positive control:
// the floor this package declares must actually be satisfied by the
// launchapi version pinned in go.mod today. If this ever fails, the floor
// constants drifted ahead of the pinned dependency.
func TestNegotiateAdoptionFloorSatisfiedByPinnedAMQ(t *testing.T) {
	if _, err := NegotiateAdoptionFloor(); err != nil {
		t.Fatalf("NegotiateAdoptionFloor() against the pinned launchapi dependency: %v", err)
	}
}

// TestNegotiateRejectsBelowAdoptionFloor proves the refusal mechanism gh#736
// asks for: an older contract, or a missing required feature, refuses
// before any Prepare call. The compiled-in launchapi contract itself can't
// be swapped mid-test, so this exercises the same launchapi.Negotiate
// entrypoint NegotiateAdoptionFloor uses with requirements engineered to be
// unsatisfiable by the pinned v0.70.0 contract — proving Negotiate (and by
// extension Prepare, wired below) fails closed rather than silently
// proceeding.
func TestNegotiateRejectsBelowAdoptionFloor(t *testing.T) {
	cases := []struct {
		name        string
		requirement launchapi.RequirementV1
	}{
		{
			name: "contract semver ahead of what is compiled in",
			requirement: launchapi.RequirementV1{
				ContractSemver: ">=99.0.0",
				IntentVersion:  launchapi.IntentVersionV1,
				ResultVersion:  launchapi.ResultVersionV1,
				Features:       AdoptionFloorFeatures,
			},
		},
		{
			name: "missing required feature",
			requirement: launchapi.RequirementV1{
				ContractSemver: AdoptionFloorContractSemver,
				IntentVersion:  launchapi.IntentVersionV1,
				ResultVersion:  launchapi.ResultVersionV1,
				Features:       append(append([]string(nil), AdoptionFloorFeatures...), "not_a_real_feature_v99"),
			},
		},
		{
			name: "unsupported intent version",
			requirement: launchapi.RequirementV1{
				ContractSemver: AdoptionFloorContractSemver,
				IntentVersion:  999,
				ResultVersion:  launchapi.ResultVersionV1,
				Features:       AdoptionFloorFeatures,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := launchapi.Negotiate(tc.requirement); err == nil {
				t.Fatalf("Negotiate(%+v) succeeded, want a refusal", tc.requirement)
			}
		})
	}
}

// TestPrepareFailsClosedBelowAdoptionFloor proves the guard is actually
// wired into Prepare, not just available as a standalone function: with a
// valid intent and a valid (non-empty) base root, Prepare still refuses
// before touching launchintent.Compile or launchapi.Prepare if the
// negotiated floor cannot be met. It substitutes the injectable
// negotiateAdoptionFloor var with a stub that refuses, since the compiled-in
// launchapi contract itself can't be swapped mid-test.
func TestPrepareFailsClosedBelowAdoptionFloor(t *testing.T) {
	stubErr := errors.New("stub: below adoption floor")
	original := negotiateAdoptionFloor
	negotiateAdoptionFloor = func() (launchapi.NegotiatedV1, error) { return launchapi.NegotiatedV1{}, stubErr }
	t.Cleanup(func() { negotiateAdoptionFloor = original })

	projectRoot := t.TempDir()
	in := PrepareInput{
		Intent:   baseIntentInput(t, projectRoot),
		Launcher: "tmux",
	}
	_, err := Prepare(context.Background(), in)
	if err == nil {
		t.Fatal("expected Prepare to refuse below the adoption floor")
	}
	if !errors.Is(err, stubErr) {
		t.Fatalf("Prepare err = %v, want it to wrap the floor-negotiation refusal", err)
	}
}
