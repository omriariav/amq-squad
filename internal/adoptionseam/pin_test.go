package adoptionseam

import (
	"runtime/debug"
	"testing"
)

// TestLinkedAMQModuleIsThePin (brief 5-amq-A, acceptance 6): go.mod pins
// PinnedAMQModule, and the module linked into this test binary is that
// version (runtime/debug build info; go1.27 test binaries list their deps,
// which the go1.25 note in floor_test.go found empty). The pin is at or
// above the adoption floor, which stays a floor.
func TestLinkedAMQModuleIsThePin(t *testing.T) {
	if got := adoptionFloorPinnedVersion(t); got != PinnedAMQModule {
		t.Fatalf("go.mod pins %s %s, PinnedAMQModule is %s", adoptionFloorModulePath, got, PinnedAMQModule)
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("no build info in the test binary")
	}
	if len(bi.Deps) == 0 {
		t.Skip("this toolchain's test binaries carry no dependency build info; the go.mod row above is the check")
	}
	linked := ""
	for _, d := range bi.Deps {
		if d.Path != adoptionFloorModulePath {
			continue
		}
		linked = d.Version
		if d.Replace != nil {
			t.Fatalf("%s is replaced by %s %s; the pin must be the released module", d.Path, d.Replace.Path, d.Replace.Version)
		}
	}
	if linked != PinnedAMQModule {
		t.Fatalf("the linked %s is %q, want %s", adoptionFloorModulePath, linked, PinnedAMQModule)
	}
	got, _ := parseAdoptionFloorSemver(linked)
	floor, _ := parseAdoptionFloorSemver(AdoptionFloorAMQVersion)
	if compareAdoptionFloorSemver(got, floor) < 0 {
		t.Fatalf("the linked module %s is below the adoption floor %s", linked, AdoptionFloorAMQVersion)
	}
}
