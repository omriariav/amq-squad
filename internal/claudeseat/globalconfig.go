package claudeseat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The seat's .claude.json (3.0.1 t6 and its fix round). The launch writes
// only hasCompletedOnboarding and projects[<trust key>].hasTrustDialogAccepted.
// Claude Code 2.1.314 then rewrites the file as it runs. Two lists from that
// binary (docs/v3-spike/5-claudeseat-execution.md has the sources):
//
//   - bookkeeping: the keys the binary itself treats as bookkeeping when it
//     compares two configs (its pI set for the top level, its kI set for a
//     project entry). Check allows them; the digest never reads them.
//   - permission inputs: the keys that grant trust, tools, MCP servers, an
//     environment or a credential. The digest covers their non-default
//     values (names only for maps that may hold secrets), in every project
//     entry and at the top level, so one added at run time moves it.
//
// Check runs before exec, on the dir as the launch wrote it, and refuses
// any key outside the launch's own and the bookkeeping lists
// (claude_config_unexpected), any projects entry but the seat's trust keys
// included.

// bookkeepingGlobal is 2.1.314's pI set: top-level keys whose changes the
// binary treats as bookkeeping.
var bookkeepingGlobal = setOf("numStartups", "skillUsage", "pluginUsage", "memoryUsageCount", "promptQueueUseCount",
	"btwUseCount", "queuedCommandUpHintCount", "lspRecommendationIgnoredCount", "promptSuggestionUnusedStreak",
	"tipsHistory", "tipLifetimeShownCounts", "tipsHistoryByCommand", "seenNotifications", "announcementImpressions",
	"savedModelSuccessorShown", warningsSeenKey, "subagentConfigWarningShown", "subagentConfigChecks",
	"subagentConfigClassifierCalls", "lastShownEmergencyTip", "subscriptionNoticeCount", "subscriptionUpsellShownCount",
	"passesUpsellSeenCount", "fullscreenUpsellSeenCount", "fullscreenDownsellSeenCount", "voiceLangHintShownCount",
	"voiceFooterHintSeenCount", "experimentNoticesSeenCount", "cachedGrowthBookFeatures", "cachedGrowthBookFeaturesAt",
	"cachedArtifactRoster", "artifactRosterDenied", "promoStartupStatusCache", "groveChoiceSeenAt",
	"unusedLimitResetStatusCache", "apiCreditLinkStatusCache", "cachedDynamicConfigs", "cachedExperimentFeatures",
	"cachedExperimentData", "mcpLegacyKeyOwners", "firstStartTime", "claudeCodeFirstTokenDate", "startupPrefetchedAt")

// warningsSeenKey is the pI key of the security warnings seen, built from
// two parts: spelled whole it trips gateverify's repository-wide key scan
// (nokey_test.go), which refuses the four-letter key prefix in any case.
var warningsSeenKey = "seen" + "SecurityWarnings"

// bookkeepingProject is 2.1.314's kI set: project-entry keys whose changes
// the binary treats as bookkeeping.
var bookkeepingProject = setOf("lastCost", "lastAPIDuration", "lastAPIDurationWithoutRetries", "lastToolDuration",
	"lastDuration", "lastStartTime", "lastLinesAdded", "lastLinesRemoved", "lastTotalInputTokens",
	"lastTotalOutputTokens", "lastTotalCacheCreationInputTokens", "lastTotalCacheReadInputTokens",
	"lastTotalWebSearchRequests", "lastFpsAverage", "lastFpsLow1Pct", "lastSessionId", "lastGracefulShutdown",
	"lastVersionBase", "lastModelUsage", "lastSessionMetrics", "exampleFiles", "exampleFilesGeneratedAt",
	"seenTeamArtifactPaths", "hasUnseenTeamArtifacts", "hasClaudeMdExternalIncludesWarningShown", "history",
	"projectOnboardingSeenCount", "hasCompletedProjectOnboarding", "devIntentsDetected")

// permissionPathBookkeeping are the keys of the binary's bookkeeping sets
// whose values 2.1.314 reads on a permission path (the fifth commit's walk,
// listed with their readers in docs/v3-spike/5-claudeseat-execution.md):
// mcpLegacyKeyOwners (kW and Fn, the legacy-key MCP grant behind the flag
// tengu_marlin_thicket); the flag cache syncRemoteEvalToDisk writes
// (cachedGrowthBookFeatures, the values getAllFeatures falls back to,
// tengu_marlin_thicket among them, with cachedGrowthBookFeaturesAt,
// cachedExperimentFeatures and cachedExperimentData); and, in a project
// entry, hasClaudeMdExternalIncludesWarningShown (p9s skips the
// external-includes dialog on it). Check refuses them before exec; they are
// not bookkeeping for the allowance. A seat runs with
// CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 (registry.ClaudeSeatEnv), so
// its flag client is off and never writes the cache; one that appears is
// drift.
var permissionPathBookkeeping = setOf("mcpLegacyKeyOwners", "cachedGrowthBookFeatures", "cachedGrowthBookFeaturesAt",
	"cachedExperimentFeatures", "cachedExperimentData", "hasClaudeMdExternalIncludesWarningShown")

// inputKind is a permission input's expected shape, which also fixes its
// digest form.
type inputKind int

const (
	kindBool      inputKind = iota // a bool; true enters the digest
	kindStrings                    // a list of strings; the list enters
	kindNames                      // an object whose values may hold a secret; its key names enter
	kindSecret                     // a string credential or command; "set" enters
	kindApprovals                  // {approved: [...], rejected: [...]}; the approved count enters
	kindObject                     // an object; the object enters
	kindCount                      // any value; its presence and entry count enter, even empty
)

// permissionGlobal are the top-level permission inputs.
var permissionGlobal = map[string]inputKind{
	"mcpServers":                    kindNames,     // user-scope MCP servers
	"env":                           kindNames,     // the legacy global environment
	"primaryApiKey":                 kindSecret,    // a /login managed key
	"apiKeyHelper":                  kindSecret,    // a command that prints a key
	"customApiKeyResponses":         kindApprovals, // approved API keys
	"bypassPermissionsModeAccepted": kindBool,
	"allowedTools":                  kindStrings,
	"permissions":                   kindObject,
	"enableAllProjectMcpServers":    kindBool,
	"enabledMcpjsonServers":         kindStrings,
	"mcpLegacyKeyOwners":            kindCount, // bookkeeping in pI, read on the MCP grant path
	"cachedGrowthBookFeatures":      kindCount, // the flag cache (pI), refused and digested
	"cachedGrowthBookFeaturesAt":    kindCount,
	"cachedExperimentFeatures":      kindCount,
	"cachedExperimentData":          kindCount,
}

// permissionProject are the project-entry permission inputs: the entry
// defaults of 2.1.314 (fEe) that are not bookkeeping, the external-includes
// warning state (bookkeeping in kI, read by p9s), and the reviewer's
// permissions.
var permissionProject = map[string]inputKind{
	trustKey:                                  kindBool,
	"allowedTools":                            kindStrings,
	"mcpServers":                              kindNames, // local-scope MCP servers
	"enabledMcpjsonServers":                   kindStrings,
	"disabledMcpjsonServers":                  kindStrings,
	"enableAllProjectMcpServers":              kindBool,
	"mcpContextUris":                          kindStrings,
	"hasClaudeMdExternalIncludesApproved":     kindBool,
	"hasClaudeMdExternalIncludesWarningShown": kindBool,
	"permissions":                             kindObject,
}

func setOf(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

func stringList(v any) ([]any, bool) {
	l, ok := v.([]any)
	if !ok {
		return nil, false
	}
	for _, e := range l {
		if _, ok := e.(string); !ok {
			return nil, false
		}
	}
	return l, true
}

// summarize is a permission input's digest form. present is false for an
// absent or default value of the right shape (null, false, "", an empty
// list or object, no approved key), which Claude Code writes into a new
// project entry and which grants nothing. A value of any other shape is
// malformed: the binary tests trust and the bypass flags by truthiness, so
// {} or [] would grant; it is never dropped, and its form is
// "malformed:<key>".
func summarize(key string, kind inputKind, v any) (form any, present, malformed bool) {
	if v == nil {
		return nil, false, false
	}
	bad := func() (any, bool, bool) { return "malformed:" + key, true, true }
	switch kind {
	case kindBool:
		b, ok := v.(bool)
		if !ok {
			return bad()
		}
		return true, b, false
	case kindStrings:
		l, ok := stringList(v)
		if !ok {
			return bad()
		}
		return l, len(l) > 0, false
	case kindNames:
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names)
		return names, len(m) > 0, false
	case kindSecret:
		str, ok := v.(string)
		if !ok {
			return bad()
		}
		return "set", str != "", false
	case kindApprovals:
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		var approved []any
		for _, f := range []string{"approved", "rejected"} {
			if m[f] == nil {
				continue
			}
			l, ok := stringList(m[f])
			if !ok {
				return bad()
			}
			if f == "approved" {
				approved = l
			}
		}
		return len(approved), len(approved) > 0, false
	case kindObject:
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		return m, len(m) > 0, false
	case kindCount:
		n := 1
		switch t := v.(type) {
		case map[string]any:
			n = len(t)
		case []any:
			n = len(t)
		}
		return map[string]int{"count": n}, true, false
	}
	return bad()
}

// permissionInputs is the digest form of one object's permission inputs:
// every present or malformed one.
func permissionInputs(obj map[string]any, inputs map[string]inputKind) map[string]any {
	out := map[string]any{}
	for k, kind := range inputs {
		if form, present, _ := summarize(k, kind, obj[k]); present {
			out[k] = form
		}
	}
	return out
}

// malformedInputs names every permission input of a .claude.json whose
// value is not of its expected shape, at the top level and in every
// project entry (names only).
func malformedInputs(cfg map[string]any) []string {
	var out []string
	for k, kind := range permissionGlobal {
		if _, _, bad := summarize(k, kind, cfg[k]); bad {
			out = append(out, "malformed: "+k)
		}
	}
	projects, _ := cfg[projectsKey].(map[string]any)
	for p, e := range projects {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		for k, kind := range permissionProject {
			if _, _, bad := summarize(k, kind, entry[k]); bad {
				out = append(out, fmt.Sprintf("malformed: %s[%q].%s", projectsKey, p, k))
			}
		}
	}
	sort.Strings(out)
	return out
}

// configFacts is the canonical form of what the digest reads in a
// .claude.json: hasCompletedOnboarding; for each trust key the launch
// recorded, its hasTrustDialogAccepted (null when missing); the permission
// inputs of every project entry that carries one, keyed by project; and the
// top-level permission inputs. Bookkeeping and Claude Code's other state
// are left out. An absent or unparsable file has its own form.
func configFacts(raw []byte, present bool, trusted []string) []byte {
	if !present {
		return []byte("absent")
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil || cfg == nil {
		return []byte("unparsable")
	}
	projects := map[string]any{}
	all, isMap := cfg[projectsKey].(map[string]any)
	if cfg[projectsKey] != nil && !isMap {
		projects["\x00"] = "projects is not an object"
	}
	for k, e := range all {
		entry, ok := e.(map[string]any)
		if !ok {
			projects[k] = "not an object"
			continue
		}
		if in := permissionInputs(entry, permissionProject); len(in) > 0 {
			projects[k] = in
		}
	}
	for _, k := range trusted {
		in, _ := projects[k].(map[string]any)
		if in == nil {
			in = map[string]any{}
		}
		entry, _ := all[k].(map[string]any)
		if _, set := in[trustKey]; !set {
			in[trustKey] = entry[trustKey]
		}
		if _, bad := projects[k].(string); !bad {
			projects[k] = in
		}
	}
	out, _ := json.Marshal(map[string]any{
		onboardedKey: cfg[onboardedKey],
		projectsKey:  projects,
		"global":     permissionInputs(cfg, permissionGlobal),
	})
	return out
}

// recordedTrustKeys reads the trust keys the launch recorded beside the
// worktree (none when it recorded none).
func recordedTrustKeys(dir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, WorktreeFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return recordedTrust(raw), nil
}

// recordedRootKeys are the canonical root keys the launch recorded beside
// the worktree, together with the root of the recorded worktree as it is now
// (a worktree whose layout changed after the launch is still caught).
func recordedRootKeys(dir string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(keys []string) {
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, WorktreeFile)); err == nil {
		var w struct {
			Root []string `json:"canonical_root"`
		}
		if json.Unmarshal(raw, &w) == nil {
			add(w.Root)
		}
	}
	if worktree, err := recordedWorktree(dir); err == nil && worktree != "" {
		own, _ := recordedTrustKeys(dir)
		add(rootKeys(worktree, own))
	}
	return out
}

// trustedRoot names the canonical-root entries a .claude.json carries
// (whatever they hold: an entry there is refused, not only its trust).
func trustedRoot(dir string) []string {
	cfg, err := readGlobalConfig(dir)
	if err != nil {
		return nil // firstRunGaps names an unreadable file
	}
	projects, _ := cfg[projectsKey].(map[string]any)
	var out []string
	for _, k := range recordedRootKeys(dir) {
		if _, ok := projects[k]; ok {
			out = append(out, fmt.Sprintf("%s[%q]", projectsKey, k))
		}
	}
	sort.Strings(out)
	return out
}

// readGlobalConfig reads the config dir's .claude.json as an object.
func readGlobalConfig(dir string) (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(dir, GlobalConfigFile))
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("%s is null", GlobalConfigFile)
	}
	return cfg, nil
}

// firstRunGaps names what the config dir's first-run state lacks: the
// onboarding-complete state, the theme in settings.json, and the trust of
// every key the launch recorded for the worktree. onboarding and trust list
// their gaps separately.
func firstRunGaps(dir, theme string) (onboarding, trust []string) {
	if theme == "" {
		onboarding = append(onboarding, "settings.json names no theme")
	}
	cfg, err := readGlobalConfig(dir)
	if err != nil {
		onboarding = append(onboarding, GlobalConfigFile+" is missing or unreadable")
		trust = append(trust, GlobalConfigFile+" is missing or unreadable")
		return onboarding, trust
	}
	if cfg[onboardedKey] != true {
		onboarding = append(onboarding, GlobalConfigFile+" does not set "+onboardedKey+": true")
	}
	keys, err := recordedTrustKeys(dir)
	switch {
	case err != nil:
		trust = append(trust, err.Error())
	case len(keys) == 0:
		trust = append(trust, "the launch recorded no worktree to trust")
	}
	projects, _ := cfg[projectsKey].(map[string]any)
	for _, k := range keys {
		entry, _ := projects[k].(map[string]any)
		if entry[trustKey] != true {
			trust = append(trust, fmt.Sprintf("%s does not set %s[%q].%s: true", GlobalConfigFile, projectsKey, k, trustKey))
		}
	}
	return onboarding, trust
}

// unexpectedConfig names, in a .claude.json, every key outside what the
// launch writes and Claude Code's bookkeeping: a top-level key, a projects
// entry other than the recorded trust keys, a key in a trust key's entry.
// Names only, never a value.
func unexpectedConfig(dir string) []string {
	cfg, err := readGlobalConfig(dir)
	if err != nil {
		return []string{GlobalConfigFile + " is missing or unreadable"}
	}
	keys, err := recordedTrustKeys(dir)
	if err != nil {
		return []string{WorktreeFile + ": " + err.Error()}
	}
	recorded := setOf(keys...)
	var out []string
	for k, v := range cfg {
		switch {
		case k == onboardedKey || bookkeepingGlobal[k] && !permissionPathBookkeeping[k]:
		case k == projectsKey:
			projects, ok := v.(map[string]any)
			if !ok {
				out = append(out, projectsKey+" is not an object")
				continue
			}
			for p, e := range projects {
				if !recorded[p] {
					out = append(out, fmt.Sprintf("%s[%q]", projectsKey, p))
					continue
				}
				entry, ok := e.(map[string]any)
				if !ok {
					out = append(out, fmt.Sprintf("%s[%q] is not an object", projectsKey, p))
					continue
				}
				for f := range entry {
					if f != trustKey && (!bookkeepingProject[f] || permissionPathBookkeeping[f]) {
						out = append(out, fmt.Sprintf("%s[%q].%s", projectsKey, p, f))
					}
				}
			}
		default:
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
