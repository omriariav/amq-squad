package cli

import "strings"

type nativeArgCardinality int

const (
	nativeRequired nativeArgCardinality = iota
	nativeOptional
	nativeVariadic
)

type nativeValueSpec struct {
	Canonical   string
	Aliases     []string
	Cardinality nativeArgCardinality
	Singleton   bool
}

var claudeValueSpecs = []nativeValueSpec{
	{Canonical: "--effort", Aliases: []string{"--effort"}, Cardinality: nativeRequired, Singleton: true},
	{Canonical: "--model", Aliases: []string{"--model"}, Cardinality: nativeRequired, Singleton: true},
	{Canonical: "--permission-mode", Aliases: []string{"--permission-mode"}, Cardinality: nativeRequired, Singleton: true},
	{Canonical: "--settings", Aliases: []string{"--settings"}, Cardinality: nativeRequired},
	{Canonical: "--agent", Aliases: []string{"--agent"}, Cardinality: nativeRequired},
	{Canonical: "--agents", Aliases: []string{"--agents"}, Cardinality: nativeRequired},
	{Canonical: "--append-system-prompt", Aliases: []string{"--append-system-prompt"}, Cardinality: nativeRequired},
	{Canonical: "--debug-file", Aliases: []string{"--debug-file"}, Cardinality: nativeRequired},
	{Canonical: "--fallback-model", Aliases: []string{"--fallback-model"}, Cardinality: nativeRequired},
	{Canonical: "--input-format", Aliases: []string{"--input-format"}, Cardinality: nativeRequired},
	{Canonical: "--json-schema", Aliases: []string{"--json-schema"}, Cardinality: nativeRequired},
	{Canonical: "--max-budget-usd", Aliases: []string{"--max-budget-usd"}, Cardinality: nativeRequired},
	{Canonical: "--name", Aliases: []string{"--name", "-n"}, Cardinality: nativeRequired},
	{Canonical: "--output-format", Aliases: []string{"--output-format"}, Cardinality: nativeRequired},
	{Canonical: "--plugin-dir", Aliases: []string{"--plugin-dir"}, Cardinality: nativeRequired},
	{Canonical: "--plugin-url", Aliases: []string{"--plugin-url"}, Cardinality: nativeRequired},
	{Canonical: "--remote-control-session-name-prefix", Aliases: []string{"--remote-control-session-name-prefix"}, Cardinality: nativeRequired},
	{Canonical: "--session-id", Aliases: []string{"--session-id"}, Cardinality: nativeRequired},
	{Canonical: "--setting-sources", Aliases: []string{"--setting-sources"}, Cardinality: nativeRequired},
	{Canonical: "--system-prompt", Aliases: []string{"--system-prompt"}, Cardinality: nativeRequired},
	{Canonical: "--add-dir", Aliases: []string{"--add-dir"}, Cardinality: nativeVariadic},
	{Canonical: "--allowed-tools", Aliases: []string{"--allowedTools", "--allowed-tools"}, Cardinality: nativeVariadic},
	{Canonical: "--betas", Aliases: []string{"--betas"}, Cardinality: nativeVariadic},
	{Canonical: "--disallowed-tools", Aliases: []string{"--disallowedTools", "--disallowed-tools"}, Cardinality: nativeVariadic},
	{Canonical: "--file", Aliases: []string{"--file"}, Cardinality: nativeVariadic},
	{Canonical: "--mcp-config", Aliases: []string{"--mcp-config"}, Cardinality: nativeVariadic},
	{Canonical: "--tools", Aliases: []string{"--tools"}, Cardinality: nativeVariadic},
	{Canonical: "--debug", Aliases: []string{"--debug", "-d"}, Cardinality: nativeOptional},
	{Canonical: "--from-pr", Aliases: []string{"--from-pr"}, Cardinality: nativeOptional},
	{Canonical: "--prompt-suggestions", Aliases: []string{"--prompt-suggestions"}, Cardinality: nativeOptional},
	{Canonical: "--remote-control", Aliases: []string{"--remote-control"}, Cardinality: nativeOptional},
	{Canonical: "--resume", Aliases: []string{"--resume", "-r"}, Cardinality: nativeOptional},
	{Canonical: "--worktree", Aliases: []string{"--worktree", "-w"}, Cardinality: nativeOptional},
}

var codexValueSpecs = []nativeValueSpec{
	{Canonical: "--config", Aliases: []string{"--config", "-c"}, Cardinality: nativeRequired},
	{Canonical: "--model", Aliases: []string{"--model", "-m"}, Cardinality: nativeRequired, Singleton: true},
	{Canonical: "--profile", Aliases: []string{"--profile", "-p"}, Cardinality: nativeRequired, Singleton: true},
	{Canonical: "--sandbox", Aliases: []string{"--sandbox", "-s"}, Cardinality: nativeRequired, Singleton: true},
	{Canonical: "--ask-for-approval", Aliases: []string{"--ask-for-approval", "-a"}, Cardinality: nativeRequired, Singleton: true},
	{Canonical: "--cd", Aliases: []string{"--cd", "-C"}, Cardinality: nativeRequired},
	{Canonical: "--add-dir", Aliases: []string{"--add-dir"}, Cardinality: nativeRequired},
	{Canonical: "--enable", Aliases: []string{"--enable"}, Cardinality: nativeRequired},
	{Canonical: "--disable", Aliases: []string{"--disable"}, Cardinality: nativeRequired},
	{Canonical: "--local-provider", Aliases: []string{"--local-provider"}, Cardinality: nativeRequired},
	{Canonical: "--remote", Aliases: []string{"--remote"}, Cardinality: nativeRequired},
	{Canonical: "--remote-auth-token-env", Aliases: []string{"--remote-auth-token-env"}, Cardinality: nativeRequired},
	{Canonical: "--image", Aliases: []string{"--image", "-i"}, Cardinality: nativeVariadic},
}

func nativeValueSpecForArg(binary, arg string) (nativeValueSpec, bool, bool) {
	specs := claudeValueSpecs
	if binary == "codex" {
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

var claudeBooleanArgs = map[string]bool{
	"--allow-dangerously-skip-permissions": true, "--ax-screen-reader": true, "--background": true, "--bg": true,
	"--bare": true, "--brief": true, "--chrome": true, "--continue": true, "-c": true,
	"--dangerously-skip-permissions": true, "--disable-slash-commands": true, "--exclude-dynamic-system-prompt-sections": true,
	"--fork-session": true, "--help": true, "-h": true, "--ide": true, "--include-hook-events": true,
	"--include-partial-messages": true, "--no-chrome": true, "--no-session-persistence": true, "--print": true, "-p": true,
	"--replay-user-messages": true, "--safe-mode": true, "--strict-mcp-config": true, "--verbose": true, "--version": true, "-v": true,
}

var codexBooleanArgs = map[string]bool{
	"--strict-config": true, "--oss": true, "--dangerously-bypass-approvals-and-sandbox": true,
	"--dangerously-bypass-hook-trust": true, "--search": true, "--no-alt-screen": true,
	"--help": true, "-h": true, "--version": true, "-V": true,
}

type nativePromptBoundary struct {
	Safe   bool
	Reason string
}

func assessNativePromptBoundary(binary string, args []string) (nativePromptBoundary, error) {
	binary = normalizedAgentBinary(binary)
	booleans := claudeBooleanArgs
	if binary == "codex" {
		booleans = codexBooleanArgs
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i == len(args)-1 {
				return nativePromptBoundary{Safe: true}, nil
			}
			return nativePromptBoundary{Reason: "native argv already contains positional tokens after --"}, nil
		}
		spec, inline, ok := nativeValueSpecForArg(binary, arg)
		if ok {
			if inline {
				continue
			}
			switch spec.Cardinality {
			case nativeOptional:
				if i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
					i++
				}
			case nativeRequired:
				if i+1 >= len(args) || args[i+1] == "--" || strings.HasPrefix(args[i+1], "-") {
					return nativePromptBoundary{}, usageErrorf("%s requires a value before the generated bootstrap prompt", arg)
				}
				i++
			case nativeVariadic:
				if i+1 >= len(args) || args[i+1] == "--" || strings.HasPrefix(args[i+1], "-") {
					return nativePromptBoundary{}, usageErrorf("%s requires at least one value before the generated bootstrap prompt", arg)
				}
				for i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
					i++
				}
			}
			continue
		}
		if booleans[arg] {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return nativePromptBoundary{Reason: "native argv contains an unknown option whose prompt arity is ambiguous"}, nil
		}
		return nativePromptBoundary{Reason: "native argv already contains a positional token"}, nil
	}
	return nativePromptBoundary{Safe: true}, nil
}

func validateNativePromptBoundary(binary string, args []string) error {
	_, err := assessNativePromptBoundary(binary, args)
	return err
}
