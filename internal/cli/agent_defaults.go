package cli

func defaultChildArgsForBinary(binary string) []string {
	switch defaultHandleFor(binary) {
	case "codex":
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	case "claude":
		return []string{"--permission-mode", "auto"}
	default:
		return nil
	}
}

func launchDefaultChildArgs(binary string, includeBuiltIn bool, extraArgs []string) []string {
	out := []string{}
	if includeBuiltIn {
		out = append(out, defaultChildArgsForBinary(binary)...)
	}
	out = append(out, extraArgs...)
	return out
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
