package registry

// PinnedAMQModule is the github.com/avivsinai/agent-message-queue version in
// go.mod. TestPinnedAMQModuleMatchesGoMod keeps the two equal, so a go.mod
// bump fails the build until this table knows the new pin.
const PinnedAMQModule = "v0.116.1"

// launchVerified is the provider version each AMQ launch library verified
// (internal/launch/adapter.go ProviderStaticInputCapabilities in that module
// version; the public launchapi exposes it only in a Prepare preview). The
// amq row is the module pin itself: the CLI should not be older.
var launchVerified = map[string]map[string]string{
	"v0.75.0": {
		"claude": "2.1.233", "codex": "0.147.0", "grok": "1.0.5", "amq": "0.75.0",
	},
	"v0.107.0": {
		"claude": "2.1.233", "codex": "0.163.0", "grok": "1.0.5",
		"cursor-agent": "2026.08.11-e8db854", "amq": "0.107.0",
	},
	// v0.116.1 (adapter.go ProviderStaticInputCapabilities): claude 2.1.233,
	// codex 0.163.0 (grammar 4), grok 1.0.5, cursor-agent unchanged; pi
	// verifies 1.2.0 (informational; the built-in pi is never a PATH tool).
	"v0.116.1": {
		"claude": "2.1.233", "codex": "0.163.0", "grok": "1.0.5",
		"cursor-agent": "2026.08.11-e8db854", "amq": "0.116.1",
	},
}

// LaunchVerifiedVersion is the provider version the pinned AMQ launch API
// verified, "" when it verifies none for that tool.
func LaunchVerifiedVersion(module, tool string) string {
	return launchVerified[module][tool]
}
