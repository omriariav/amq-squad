package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
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
func claudeInScopePreauthAllowlist(session string) []string {
	if strings.TrimSpace(session) == "" {
		return nil
	}
	return []string{
		"Bash(gh pr create:*)",
	}
}

// claudePreauthChildArgs turns a Claude allowlist into the child flags appended
// at launch. Empty allowlist yields no args (so non-enabled launches are
// untouched and back-compatible).
func claudePreauthChildArgs(allow []string) []string {
	if len(allow) == 0 {
		return nil
	}
	// native_args.go recognizes the equals-joined alias. Keeping the validated
	// value in the same argv token prevents it from ever being reinterpreted as
	// another native option if a malformed profile bypasses validation.
	return []string{"--allowedTools=" + strings.Join(allow, ",")}
}

func applyClaudeWorkerPreauth(projectDir, profile, role, binary, session string, childArgs []string, includeBuiltIn bool) ([]string, []string, bool) {
	out := append([]string(nil), childArgs...)
	configured := configuredClaudePermissionAllowlist(projectDir, profile, role, binary)
	var actions []string
	if includeBuiltIn && claudeWorkerPreauthEligible(projectDir, profile, role, binary) {
		actions = appendUniquePermissionPatterns(actions, claudeInScopePreauthAllowlist(session)...)
	}
	actions = appendUniquePermissionPatterns(actions, configured...)
	if len(actions) == 0 {
		if explicit, found := collectClaudeAllowedTools(out); found {
			// Even without launcher-owned policy, canonicalize explicit native
			// grants into the safe equals form and collapse duplicate aliases.
			return replaceClaudeAllowedTools(out, explicit), nil, false
		}
		return out, nil, false
	}
	if childArgsHasAllowedTools(out) {
		if childArgsAllowedToolsEquals(out, strings.Join(actions, ",")) {
			// Team launch previews emit launcher-owned preauth into the copied
			// command. Treat the exact allowlist as ours so bootstrap eligibility
			// and launch-record audit fields stay aligned with live launch.
			return out, actions, false
		}
		// Recomposition always preserves genuinely explicit native grants while
		// rebuilding launcher-owned grants from current policy. This is what lets
		// profile removal/narrowing revoke the old launcher-owned value on replay.
		actions = appendUniquePermissionPatterns(childArgsAllowedTools(out), actions...)
	}
	return replaceClaudeAllowedTools(out, actions), actions, true
}

func configuredClaudePermissionAllowlist(projectDir, profile, role, binary string) []string {
	if normalizedAgentBinary(binary) != "claude" || strings.TrimSpace(role) == "" || !team.ExistsProfile(projectDir, profile) {
		return nil
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return nil
	}
	m, ok := teamMemberByRole(t, role)
	if !ok || normalizedAgentBinary(m.Binary) != "claude" {
		return nil
	}
	return appendUniquePermissionPatterns(nil, m.PermissionAllowlist...)
}

func appendUniquePermissionPatterns(dst []string, patterns ...string) []string {
	seen := make(map[string]bool, len(dst)+len(patterns))
	for _, pattern := range dst {
		seen[pattern] = true
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		dst = append(dst, pattern)
	}
	return dst
}

func childArgsAllowedTools(args []string) []string {
	values, _ := collectClaudeAllowedTools(args)
	return values
}

// collectClaudeAllowedTools reads every native allowed-tools spelling before
// the literal -- prompt boundary. Claude treats this option as variadic, while
// amq-squad emits a single comma-joined value so the effective launch grant is
// unambiguous and auditable.
func collectClaudeAllowedTools(args []string) ([]string, bool) {
	var values []string
	found := false
	for i := 0; i < len(args); {
		arg := args[i]
		if arg == "--" {
			break
		}
		spec, inline, ok := nativeValueSpecForArg("claude", arg)
		if !ok || spec.Canonical != "--allowed-tools" {
			i++
			continue
		}
		found = true
		if inline {
			if _, raw, ok := strings.Cut(arg, "="); ok {
				values = appendUniquePermissionPatterns(values, strings.Split(raw, ",")...)
			}
			i++
			continue
		}
		i++
		for i < len(args) && args[i] != "--" && !strings.HasPrefix(args[i], "-") {
			values = appendUniquePermissionPatterns(values, strings.Split(args[i], ",")...)
			i++
		}
	}
	return values, found
}

func replaceClaudeAllowedTools(args, actions []string) []string {
	clean := make([]string, 0, len(args)+2)
	boundary := -1
	for i := 0; i < len(args); {
		arg := args[i]
		if arg == "--" {
			boundary = len(clean)
			clean = append(clean, args[i:]...)
			break
		}
		spec, inline, ok := nativeValueSpecForArg("claude", arg)
		if !ok || spec.Canonical != "--allowed-tools" {
			clean = append(clean, arg)
			i++
			continue
		}
		i++
		if inline {
			continue
		}
		for i < len(args) && args[i] != "--" && !strings.HasPrefix(args[i], "-") {
			i++
		}
	}
	grant := claudePreauthChildArgs(actions)
	if boundary < 0 {
		return append(clean, grant...)
	}
	out := make([]string, 0, len(clean)+len(grant))
	out = append(out, clean[:boundary]...)
	out = append(out, grant...)
	out = append(out, clean[boundary:]...)
	return out
}

func stripTrailingLauncherPreauthArgs(childArgs, preauthorizedActions []string) []string {
	return stripRecordedLauncherPreauth(childArgs, preauthorizedActions)
}

// stripRecordedLauncherPreauth removes only the exact launcher-owned grant
// identified by launch.Record.PreauthorizedActions. It recognizes both the
// historical two-token spelling and the injection-safe equals spelling. Any
// independently configured native allowed-tools value remains available via
// the record's ClaudeArgs/ExplicitAllowedTools and is recomposed with current
// member policy.
func stripRecordedLauncherPreauth(args, preauthorizedActions []string) []string {
	if len(preauthorizedActions) == 0 {
		return append([]string(nil), args...)
	}
	want := strings.Join(preauthorizedActions, ",")
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		spec, inline, ok := nativeValueSpecForArg("claude", arg)
		if !ok || spec.Canonical != "--allowed-tools" {
			out = append(out, arg)
			i++
			continue
		}
		if inline {
			_, value, _ := strings.Cut(arg, "=")
			if value == want {
				i++
				continue
			}
			out = append(out, arg)
			i++
			continue
		}
		if i+1 < len(args) && args[i+1] == want {
			i += 2
			continue
		}
		out = append(out, arg)
		i++
	}
	return out
}

func childArgsHasAllowedTools(args []string) bool {
	_, found := collectClaudeAllowedTools(args)
	return found
}

func childArgsAllowedToolsEquals(args []string, want string) bool {
	values, found := collectClaudeAllowedTools(args)
	return found && strings.Join(values, ",") == want
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

func defaultChildArgsForBinary(binary string) []string {
	return defaultChildArgsForBinaryWithTrust(binary, defaultTrustMode())
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

func launchDefaultChildArgs(binary string, includeBuiltIn bool, modelArgs, extraArgs []string) []string {
	return launchDefaultChildArgsWithTrust(binary, includeBuiltIn, modelArgs, extraArgs, defaultTrustMode())
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

// validateMembersTrust applies the same trust-vs-args contradiction check to
// each member's per-member native args (team.json claude_args/codex_args), so
// a sandboxed team cannot smuggle the Codex bypass flag through one member's
// codex_args. Runs at plan/launch time, next to the team-level check, so the
// rejection happens before any pane exists instead of inside runLaunch.
func validateMembersTrust(trustMode string, trustExplicit bool, members []team.Member) error {
	for _, m := range members {
		// ExtraArgs selects the field matching the member's binary, so a
		// member can never be judged on args that don't apply to it (the
		// schema validator rejects mismatched fields, but this helper must
		// not depend on that having run).
		extra := m.ExtraArgs()
		if len(extra) == 0 {
			continue
		}
		memberArgs := map[string][]string{normalizedAgentBinary(m.Binary): extra}
		if err := validateTrustCombination(trustMode, trustExplicit, false, memberArgs); err != nil {
			return fmt.Errorf("member %s: %w", m.Role, err)
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

func ensureDefaultChildArgs(binary string, childArgs []string) []string {
	return ensureLeadingChildArgs(defaultChildArgsForBinary(binary), childArgs)
}

func ensureLeadingChildArgs(defaultArgs, childArgs []string) []string {
	if len(defaultArgs) == 0 || hasLeadingArgs(defaultArgs, childArgs) {
		return childArgs
	}
	prefixLen := leadingDefaultPrefixLen(defaultArgs, childArgs)
	out := append([]string(nil), defaultArgs...)
	return append(out, childArgs[prefixLen:]...)
}

func hasLeadingDefaultChildArgs(binary string, childArgs []string) bool {
	return hasLeadingArgs(defaultChildArgsForBinary(binary), childArgs)
}

func hasLeadingArgs(defaultArgs, childArgs []string) bool {
	if len(defaultArgs) == 0 || len(childArgs) < len(defaultArgs) {
		return false
	}
	for i := range defaultArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
}

func leadingDefaultPrefixLen(defaultArgs, childArgs []string) int {
	max := len(defaultArgs)
	if len(childArgs) < max {
		max = len(childArgs)
	}
	for n := max; n > 0; n-- {
		match := true
		for i := 0; i < n; i++ {
			if childArgs[i] != defaultArgs[i] {
				match = false
				break
			}
		}
		if match {
			return n
		}
	}
	return 0
}

func shouldAppendBootstrap(binary string, childArgs []string) bool {
	return shouldAppendBootstrapWithDefaults(childArgs, defaultChildArgsForBinary(binary))
}

func shouldAppendBootstrapWithDefaults(childArgs []string, defaultArgs []string) bool {
	if len(childArgs) == 0 {
		return true
	}
	if len(childArgs) != len(defaultArgs) {
		return false
	}
	for i := range childArgs {
		if childArgs[i] != defaultArgs[i] {
			return false
		}
	}
	return true
}
