// Package plugins embeds the Claude Code hooks plugin (brief 4-2) so the
// core can install it into each Claude seat's own CLAUDE_CONFIG_DIR at
// launch (2b-2 milestone B, plan 4.3, R36 ruling): the hook files the seat
// runs are the ones this binary was built with, never the owner's user-scope
// install.
package plugins

import "embed"

// Claude holds the hook files of plugins/claude: the plugin manifest,
// hooks.json and the hook script.
//
//go:embed claude/.claude-plugin/plugin.json claude/hooks/hooks.json claude/hooks/amq-squad-hook.sh
var Claude embed.FS

// ClaudeRoot is the directory of the plugin inside Claude.
const ClaudeRoot = "claude"
