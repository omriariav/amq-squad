package cli

const (
	trustModeSandboxed    = "sandboxed"
	trustModeApproveForMe = "approve-for-me"
	trustModeTrusted      = "trusted"
)

var (
	codexApproveForMeArgs = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	codexTrustedArgs      = []string{"--dangerously-bypass-approvals-and-sandbox"}
)

func normalizeTrustMode(mode string) (string, error) {
	switch mode {
	case "":
		return defaultTrustMode(), nil
	case trustModeSandboxed:
		return trustModeSandboxed, nil
	case trustModeApproveForMe:
		return trustModeApproveForMe, nil
	case trustModeTrusted:
		return trustModeTrusted, nil
	default:
		return "", usageErrorf("invalid trust mode %q: use sandboxed, approve-for-me, or trusted", mode)
	}
}

func defaultTrustMode() string {
	return trustModeApproveForMe
}

// validateTrustCombination rejects user input that contradicts the trust mode.
// trusted plus --no-default-args is incoherent: trust would prepend the bypass
// flag while no-default-args asks to omit defaults. sandboxed plus a manually
// supplied bypass via --codex-args is also rejected to keep the trust boundary
// the single, visible source of truth.
func validateTrustCombination(trustMode string, trustExplicit, noDefaultArgs bool, binaryArgs map[string][]string) error {
	if trustMode == trustModeTrusted && noDefaultArgs {
		return usageErrorf("--trust trusted cannot be combined with --no-default-args; trusted prepends the Codex permission flag, --no-default-args opts out of defaults")
	}
	if trustMode != trustModeTrusted {
		for _, arg := range binaryArgs["codex"] {
			if arg == "--dangerously-bypass-approvals-and-sandbox" {
				if trustExplicit {
					return usageErrorf("--trust sandboxed cannot be combined with --codex-args containing --dangerously-bypass-approvals-and-sandbox; pass --trust trusted instead")
				}
				return usageErrorf("--codex-args contains --dangerously-bypass-approvals-and-sandbox; pass --trust trusted instead so the trust boundary is explicit")
			}
		}
	}
	return nil
}
