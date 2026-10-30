package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// TestGeneralOperationFloorUnchanged pins the one AMQ floor of 3.0 (5-amq-A
// fix round): doctorMinAMQVersion is the release of the module go.mod pins,
// v0.116.2, and the installed-binary floor is the same constant. Every 3.0
// path needs that release's launch adapters and its mailbox, so there is no
// separate, lower general-operation floor any more (gh#736's two floors
// merged when the launch API became the only launch path). Raise it only
// together with the go.mod pin.
func TestGeneralOperationFloorUnchanged(t *testing.T) {
	const want = "0.116.2"
	if doctorMinAMQVersion != want {
		t.Fatalf("doctorMinAMQVersion = %q, want %q (raise it only with the go.mod pin)", doctorMinAMQVersion, want)
	}
	if doctorInstalledAMQFloor != doctorMinAMQVersion {
		t.Fatalf("the installed-binary floor %q is not doctorMinAMQVersion %q: one floor for one binary", doctorInstalledAMQFloor, doctorMinAMQVersion)
	}
	if "v"+want != registry.PinnedAMQModule || strings.TrimPrefix(registry.PinnedAMQModule, "v") != doctorMinAMQVersion {
		t.Fatalf("doctorMinAMQVersion %s is not the go.mod pin %s", doctorMinAMQVersion, registry.PinnedAMQModule)
	}
}
