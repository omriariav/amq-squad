package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Amendment 17 (b): AMQ Prepare accepts any Codex model id, and the refusal
// shows only on the first turn. The registry checks the id against the
// codex-cli version that will run before a seat launches.

// codexModelLists are the model ids a codex-cli version's own /model picker
// lists, where observed. 0.147.0: S4 closure run, 2026-10-21 (S4 report).
var codexModelLists = map[string][]string{
	"0.147.0": {"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"},
}

// CodexVerdict is the result of one model id check.
type CodexVerdict struct {
	Model   string
	Version string // the codex-cli version checked against
	Usable  bool
	Known   bool   // false: no list and no probe for this version; the id is unchecked
	Reason  string // why the id is refused or unchecked
}

// CheckCodexModel checks a model id against the codex-cli version's model
// list, then against a probe verdict recorded for that version.
func CheckCodexModel(version, model string, probed map[string]CodexVerdict) CodexVerdict {
	v := CodexVerdict{Model: model, Version: version}
	if p, ok := probed[model]; ok && p.Version == version && p.Known {
		return p
	}
	if list, ok := codexModelLists[version]; ok {
		v.Known = true
		for _, m := range list {
			if m == model {
				v.Usable = true
				return v
			}
		}
		v.Reason = fmt.Sprintf("codex-cli %s does not list model %s (its picker lists %s); a newer codex may be needed", version, model, strings.Join(list, ", "))
		return v
	}
	v.Reason = fmt.Sprintf("model %s is not checked against codex-cli %s; run 'amq-squad doctor --probe'", model, version)
	return v
}

var (
	codexNeedsNewer  = regexp.MustCompile(`(?i)model requires a newer version of Codex`)
	codexAccount     = regexp.MustCompile(`(?i)model is not supported\s+when using Codex with an? (ChatGPT|API) account`)
	codexNoModel     = regexp.MustCompile(`(?i)model .{0,40}(does not exist|not found|unknown model|you do not have access)`)
	codexErrorLine   = regexp.MustCompile(`"type"\s*:\s*"error"`)
	codexUnsupported = regexp.MustCompile(`(?i)(unsupported|invalid|unknown) model|model .{0,40}(is not supported|unsupported)`)
	spaceRE          = regexp.MustCompile(`\s+`)
)

// ClassifyCodexOutput reads the output of one codex turn (a pane capture or
// `codex exec --json` output) and reports whether the provider refused the
// model. A known refusal text marks the id refused. The id is usable only
// when the process exited 0 and a structured assistant completion's text is
// exactly OK; an echoed prompt or a plain-text error never qualifies.
// "Model metadata for X not found" alone is a warning, not a refusal.
func ClassifyCodexOutput(model, version, out string, exitOK bool) CodexVerdict {
	flat := spaceRE.ReplaceAllString(out, " ")
	v := CodexVerdict{Model: model, Version: version, Known: true}
	switch {
	case codexNeedsNewer.MatchString(flat):
		v.Reason = fmt.Sprintf("codex-cli %s: the model %s requires a newer version of Codex", version, model)
	case codexAccount.MatchString(flat):
		m := codexAccount.FindStringSubmatch(flat)
		v.Reason = fmt.Sprintf("codex-cli %s: the model %s is not supported with a %s account", version, model, m[1])
	case codexNoModel.MatchString(flat) && (codexErrorLine.MatchString(flat) || !exitOK), codexUnsupported.MatchString(flat):
		v.Reason = fmt.Sprintf("codex-cli %s: the provider refused model %s", version, model)
	case exitOK && codexAssistantText(out) == "OK":
		v.Usable = true
	default:
		v.Known = false
		v.Reason = fmt.Sprintf("codex-cli %s: no structured OK answer for model %s (exit ok %t)", version, model, exitOK)
	}
	return v
}

// codexAssistantText is the text of the last structured assistant message
// in `codex exec --json` output: an item.completed agent_message, or the
// older {"msg":{"type":"agent_message"}} event.
func codexAssistantText(out string) string {
	last := ""
	for _, line := range strings.Split(out, "\n") {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Msg struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"msg"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "item.completed" && ev.Item.Type == "agent_message":
			last = ev.Item.Text
		case ev.Msg.Type == "agent_message":
			last = ev.Msg.Message
		}
	}
	return strings.TrimSpace(last)
}

// ProbeCodexModel runs one short non-interactive turn with the model id and
// classifies the answer. It spends one turn of the owner's Codex quota, so
// only `doctor --probe` calls it.
func ProbeCodexModel(ctx context.Context, env Env, codexPath, version, model string) CodexVerdict {
	env = env.withDefaults()
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := env.Run(cctx, codexPath, "exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only",
		"--model", model, "Reply with the single word OK and nothing else.")
	return ClassifyCodexOutput(model, version, out, err == nil)
}
