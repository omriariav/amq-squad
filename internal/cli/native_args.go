package cli

import "strings"

type nativeValueSpec struct {
	Canonical string
	Aliases   []string
	Singleton bool
}

var claudeValueSpecs = []nativeValueSpec{
	{Canonical: "--effort", Aliases: []string{"--effort"}, Singleton: true},
	{Canonical: "--model", Aliases: []string{"--model"}, Singleton: true},
	{Canonical: "--permission-mode", Aliases: []string{"--permission-mode"}, Singleton: true},
	{Canonical: "--settings", Aliases: []string{"--settings"}},
	{Canonical: "--agent", Aliases: []string{"--agent"}},
	{Canonical: "--agents", Aliases: []string{"--agents"}},
	{Canonical: "--append-system-prompt", Aliases: []string{"--append-system-prompt"}},
	{Canonical: "--debug-file", Aliases: []string{"--debug-file"}},
	{Canonical: "--fallback-model", Aliases: []string{"--fallback-model"}},
	{Canonical: "--input-format", Aliases: []string{"--input-format"}},
	{Canonical: "--json-schema", Aliases: []string{"--json-schema"}},
	{Canonical: "--max-budget-usd", Aliases: []string{"--max-budget-usd"}},
	{Canonical: "--name", Aliases: []string{"--name", "-n"}},
	{Canonical: "--output-format", Aliases: []string{"--output-format"}},
	{Canonical: "--plugin-dir", Aliases: []string{"--plugin-dir"}},
	{Canonical: "--plugin-url", Aliases: []string{"--plugin-url"}},
	{Canonical: "--remote-control-session-name-prefix", Aliases: []string{"--remote-control-session-name-prefix"}},
	{Canonical: "--session-id", Aliases: []string{"--session-id"}},
	{Canonical: "--setting-sources", Aliases: []string{"--setting-sources"}},
	{Canonical: "--system-prompt", Aliases: []string{"--system-prompt"}},
	{Canonical: "--add-dir", Aliases: []string{"--add-dir"}},
	{Canonical: "--allowed-tools", Aliases: []string{"--allowedTools", "--allowed-tools"}},
	{Canonical: "--betas", Aliases: []string{"--betas"}},
	{Canonical: "--disallowed-tools", Aliases: []string{"--disallowedTools", "--disallowed-tools"}},
	{Canonical: "--file", Aliases: []string{"--file"}},
	{Canonical: "--mcp-config", Aliases: []string{"--mcp-config"}},
	{Canonical: "--tools", Aliases: []string{"--tools"}},
	{Canonical: "--debug", Aliases: []string{"--debug", "-d"}},
	{Canonical: "--from-pr", Aliases: []string{"--from-pr"}},
	{Canonical: "--prompt-suggestions", Aliases: []string{"--prompt-suggestions"}},
	{Canonical: "--remote-control", Aliases: []string{"--remote-control"}},
	{Canonical: "--resume", Aliases: []string{"--resume", "-r"}},
	{Canonical: "--worktree", Aliases: []string{"--worktree", "-w"}},
}

var codexValueSpecs = []nativeValueSpec{
	{Canonical: "--config", Aliases: []string{"--config", "-c"}},
	{Canonical: "--model", Aliases: []string{"--model", "-m"}, Singleton: true},
	{Canonical: "--profile", Aliases: []string{"--profile", "-p"}, Singleton: true},
	{Canonical: "--sandbox", Aliases: []string{"--sandbox", "-s"}, Singleton: true},
	{Canonical: "--ask-for-approval", Aliases: []string{"--ask-for-approval", "-a"}, Singleton: true},
	{Canonical: "--cd", Aliases: []string{"--cd", "-C"}},
	{Canonical: "--add-dir", Aliases: []string{"--add-dir"}},
	{Canonical: "--enable", Aliases: []string{"--enable"}},
	{Canonical: "--disable", Aliases: []string{"--disable"}},
	{Canonical: "--local-provider", Aliases: []string{"--local-provider"}},
	{Canonical: "--remote", Aliases: []string{"--remote"}},
	{Canonical: "--remote-auth-token-env", Aliases: []string{"--remote-auth-token-env"}},
	{Canonical: "--image", Aliases: []string{"--image", "-i"}},
}

func nativeValueSpecForArg(binary, arg string) (nativeValueSpec, bool, bool) {
	specs := claudeValueSpecs
	if binary == "codex" {
		if strings.HasPrefix(arg, "-c=") || (strings.HasPrefix(arg, "-c") && len(arg) > 2) {
			raw := strings.TrimPrefix(strings.TrimPrefix(arg, "-c"), "=")
			if strings.Contains(raw, "=") {
				return codexValueSpecs[0], true, true
			}
		}
		specs = codexValueSpecs
	}
	for _, spec := range specs {
		for _, alias := range spec.Aliases {
			if arg == alias {
				return spec, false, true
			}
			if strings.HasPrefix(arg, alias+"=") {
				return spec, true, true
			}
		}
	}
	return nativeValueSpec{}, false, false
}
