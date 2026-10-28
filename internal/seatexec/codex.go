package seatexec

import "strconv"

// CodexNetworkNeed is the env_needs entry with which a Codex seat keeps the
// sandbox's network access (the registry entry declares it).
const CodexNetworkNeed = "CODEX_NETWORK_ACCESS"

// SandboxPinsMissing refuses a Codex seat whose admitted argv does not carry
// the two sandbox pins exactly as the launch record requires.
const SandboxPinsMissing = "sandbox_pins_missing"

// The two workspace-write sandbox settings a Codex seat runs with, as
// config overrides on its command line (Codex gives -c precedence over
// config.toml, so a trusted project config cannot widen them; fix 3 item 5):
// no extra writable roots, and no network unless the seat declared it.
const (
	CodexWritableRootsPin = "sandbox_workspace_write.writable_roots"
	CodexNetworkAccessPin = "sandbox_workspace_write.network_access"
)

// CodexSandboxPins are the pins as provider arguments. Since AMQ v0.110.0
// (upstream ask 17) the launch admits both keys, so the compiler puts them
// in the launch request, where admission and the trust digest cover them;
// seat-exec only checks that the admitted argv carries them
// (CheckCodexSandboxPins) and adds nothing (amendment 28's exception is
// gone).
func CodexSandboxPins(network bool) []string {
	return []string{"-c", CodexWritableRootsPin + "=[]", "-c", CodexNetworkAccessPin + "=" + strconv.FormatBool(network)}
}
