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

func shouldAppendBootstrap(binary string, childArgs []string) bool {
	if len(childArgs) == 0 {
		return true
	}
	defaultArgs := defaultChildArgsForBinary(binary)
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
