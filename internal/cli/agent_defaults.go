package cli

import (
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/launchintent"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

const (
	trustModeSandboxed    = "sandboxed"
	trustModeApproveForMe = "approve-for-me"
	trustModeTrusted      = "trusted"
)

var (
	codexApproveForMeArgs = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	codexTrustedArgs      = []string{"--dangerously-bypass-approvals-and-sandbox"}
)

// claudeInScopePreauthAllowlist returns the Claude --allowedTools patterns that
// pre-authorize a worker's in-scope deliverable action (#296): creating its PR.
//
// It deliberately authorizes ONLY `gh pr create`. Feature-branch `git push` is
// intentionally NOT pre-authorized: Claude's --allowedTools prefix match
// (`Bash(<prefix>:*)`) cannot be bounded, so any `git push origin codex/...:*`
// pattern would also match the same command with `--tags`/`--follow-tags` or an
// extra refspec appended — which would defeat the guardrail that tags/main/broad
// push stay gated. There is no safe pattern form for "this feature branch and
// nothing else" (the branch name is itself a dynamic suffix). Pre-authorizing
// push therefore needs a constrained wrapper command and is tracked as a
// follow-up; until then a feature-branch push still prompts (safe degradation),
// while the recurring `gh pr create` stall is removed. By keeping this list to a
// single PR-domain pattern, it cannot — by construction — authorize push, tags,
// releases, or destructive git.
//
// The pattern itself is internal/launchintent.ScopedPreauthGrant, gh#747's
// single source of truth: that package's new-path compiler gates the same
// literal on the seat's observed argv-grammar capability rather than
// keeping its own copy, so the two paths cannot silently drift apart.
func claudeInScopePreauthAllowlist(session string) []string {
	if strings.TrimSpace(session) == "" {
		return nil
	}
	return []string{
		launchintent.ScopedPreauthGrant,
	}
}

// claudeWorkerPreauthEligible reports whether an `agent up` launch is an
// amq-squad-launched orchestrated NON-LEAD worker on the Claude binary — the
// only case #296 pre-authorizes. cto/global-lead (the lead role), operator, and
// non-orchestrated/standalone launches are excluded, so their permission posture
// is unchanged. Codex is out of scope for this slice and always returns false.
func claudeWorkerPreauthEligible(projectDir, profile, role, binary string) bool {
	if defaultHandleFor(binary) != "claude" {
		return false
	}
	role = strings.TrimSpace(role)
	if role == "" {
		return false
	}
	if !team.ExistsProfile(projectDir, profile) {
		return false
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil || !t.Orchestrated {
		return false
	}
	lead := strings.TrimSpace(t.Lead)
	if lead == "" || strings.EqualFold(role, lead) {
		return false
	}
	// Require a CONFIGURED active team member for this role, and require that
	// member to be a Claude binary. This rejects unknown/ad-hoc roles (e.g. a
	// `scratch` launch in an orchestrated profile) and a role configured for a
	// different binary that happens to be launched as claude.
	m, ok := teamMemberByRole(t, role)
	if !ok {
		return false
	}
	if defaultHandleFor(strings.TrimSpace(m.Binary)) != "claude" {
		return false
	}
	return true
}

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

func defaultChildArgsForBinaryWithTrust(binary, trustMode string) []string {
	switch defaultHandleFor(binary) {
	case "codex":
		switch trustMode {
		case trustModeTrusted:
			return append([]string(nil), codexTrustedArgs...)
		case trustModeApproveForMe:
			return append([]string(nil), codexApproveForMeArgs...)
		}
		return nil
	case "claude":
		return []string{"--permission-mode", "auto"}
	default:
		return nil
	}
}

func launchDefaultChildArgsWithTrust(binary string, includeBuiltIn bool, modelArgs, extraArgs []string, trustMode string) []string {
	var builtIn []string
	if includeBuiltIn {
		builtIn = defaultChildArgsForBinaryWithTrust(binary, trustMode)
	}
	return composeBinaryArgs(binary, builtIn, modelArgs, extraArgs)
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

// modelArgsForBinary returns the native model-selection flag for the binary.
// codex and claude both accept --model <name>; unknown binaries get nothing.
func modelArgsForBinary(binary, model string) []string {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	switch normalizedAgentBinary(binary) {
	case "codex", "claude":
		return []string{"--model", model}
	default:
		return nil
	}
}
