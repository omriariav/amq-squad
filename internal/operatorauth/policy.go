package operatorauth

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const ActionTaxonomyVersion = 1

const (
	GateSpawn                 = "spawn"
	GateMerge                 = "merge"
	GateFeatureBranchPush     = "feature_branch_push"
	GateRelease               = "release"
	GateTag                   = "tag"
	GatePublish               = "publish"
	GateExternalSend          = "external_send"
	GateDestructiveFilesystem = "destructive_filesystem"
)

// ActionCapability is one atomic action admitted by the taxonomy. The catalog
// is a capability ceiling only: callers must still establish actor, policy,
// gate, and exact-target authority before executing an action.
type ActionCapability struct {
	Action       string   `json:"action"`
	GateKind     string   `json:"gate_kind"`
	Aliases      []string `json:"aliases,omitempty"`
	HumanOnly    bool     `json:"human_only"`
	SelfEligible bool     `json:"self_eligible"`
}

var actionCatalog = mustActionCatalog([]ActionCapability{
	{Action: "default_branch_push", GateKind: GateMerge, Aliases: []string{"push_default_branch"}, SelfEligible: true},
	{Action: "destructive_filesystem", GateKind: GateDestructiveFilesystem, Aliases: []string{"destructive_fs"}, HumanOnly: true},
	{Action: "external_send", GateKind: GateExternalSend, Aliases: []string{"external_message"}, HumanOnly: true},
	{Action: "feature_branch_push", GateKind: GateFeatureBranchPush, Aliases: []string{"push_feature_branch"}, SelfEligible: true},
	{Action: "github_release", GateKind: GateRelease, Aliases: []string{"gh_release"}, HumanOnly: true},
	{Action: "package_publish", GateKind: GatePublish, HumanOnly: true},
	{Action: "protected_branch_push", GateKind: GateMerge, Aliases: []string{"push_protected_branch"}, SelfEligible: true},
	{Action: "spawn", GateKind: GateSpawn, HumanOnly: true, SelfEligible: false},
	{Action: "tag", GateKind: GateTag, Aliases: []string{"tag_push", "create_tag", "push_tag"}, HumanOnly: true},
})

func mustActionCatalog(items []ActionCapability) []ActionCapability {
	items, err := validateActionCatalog(items)
	if err != nil {
		panic("invalid operator action catalog: " + err.Error())
	}
	return items
}

func validateActionCatalog(items []ActionCapability) ([]ActionCapability, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("catalog is empty")
	}
	out := make([]ActionCapability, len(items))
	seen := make(map[string]string)
	for i, item := range items {
		item.Action = strings.TrimSpace(item.Action)
		item.GateKind = strings.TrimSpace(item.GateKind)
		if item.Action == "" || NormalizeAction(item.Action) != item.Action {
			return nil, fmt.Errorf("action %q is empty or non-canonical", item.Action)
		}
		if item.GateKind == "" || NormalizeAction(item.GateKind) != item.GateKind {
			return nil, fmt.Errorf("action %q has empty or non-canonical gate kind %q", item.Action, item.GateKind)
		}
		if item.HumanOnly == item.SelfEligible {
			return nil, fmt.Errorf("action %q must be exactly one of human-only or self-eligible", item.Action)
		}
		identifiers := append([]string{item.Action}, item.Aliases...)
		for j, identifier := range identifiers {
			identifier = strings.TrimSpace(identifier)
			if identifier == "" || NormalizeAction(identifier) != identifier {
				return nil, fmt.Errorf("action %q has empty or non-canonical identifier %q", item.Action, identifier)
			}
			if owner, exists := seen[identifier]; exists {
				return nil, fmt.Errorf("identifier %q is shared by actions %q and %q", identifier, owner, item.Action)
			}
			seen[identifier] = item.Action
			if j > 0 {
				item.Aliases[j-1] = identifier
			}
		}
		item.Aliases = append([]string(nil), item.Aliases...)
		sort.Strings(item.Aliases)
		out[i] = item
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out, nil
}

func ActionCapabilities() []ActionCapability {
	out := make([]ActionCapability, len(actionCatalog))
	for i, item := range actionCatalog {
		out[i] = item
		out[i].Aliases = append([]string(nil), item.Aliases...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out
}

func NormalizeAction(raw string) string {
	return strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(raw)))
}

func LookupAction(raw string) (ActionCapability, error) {
	key := NormalizeAction(raw)
	if key == "" {
		return ActionCapability{}, fmt.Errorf("action is required")
	}
	for _, item := range actionCatalog {
		if key == item.Action {
			return item, nil
		}
		for _, alias := range item.Aliases {
			if key == alias {
				return item, nil
			}
		}
	}
	return ActionCapability{}, fmt.Errorf("unknown atomic action %q", raw)
}

func CanonicalAction(raw string) (string, error) {
	item, err := LookupAction(raw)
	if err != nil {
		return "", err
	}
	return item.Action, nil
}

func ValidateGateAction(kind, action string) (ActionCapability, error) {
	kind, err := NormalizeGateKind(kind)
	if err != nil {
		return ActionCapability{}, err
	}
	item, err := LookupAction(action)
	if err != nil {
		return ActionCapability{}, err
	}
	if item.GateKind != kind {
		return ActionCapability{}, fmt.Errorf("gate/action mismatch: %s cannot authorize %s", kind, item.Action)
	}
	return item, nil
}

var mergeTargetPattern = regexp.MustCompile(`^PR #([1-9][0-9]*) head ([0-9A-Fa-f]{7,64}) into ([A-Za-z0-9._/-]+)$`)

type MergeTarget struct{ Subject, Head, Base string }

func ParseMergeTarget(raw string) (MergeTarget, error) {
	match := mergeTargetPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return MergeTarget{}, fmt.Errorf("merge target must exactly match 'PR #<number> head <sha> into <base>'")
	}
	return MergeTarget{Subject: "PR #" + match[1], Head: match[2], Base: match[3]}, nil
}

type Binding struct {
	GateKind string
	Action   string
	Target   string
}

func ParseStrictBinding(text string) (Binding, error) {
	values := map[string][]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "gate-kind", "action", "target":
			key = strings.ToLower(strings.TrimSpace(key))
			values[key] = append(values[key], strings.TrimSpace(value))
		}
	}
	for _, key := range []string{"gate-kind", "action", "target"} {
		if len(values[key]) != 1 || values[key][0] == "" {
			return Binding{}, fmt.Errorf("binding requires exactly one non-empty %s field", key)
		}
	}
	kind, err := NormalizeGateKind(values["gate-kind"][0])
	if err != nil {
		return Binding{}, err
	}
	action, err := CanonicalAction(values["action"][0])
	if err != nil {
		return Binding{}, err
	}
	if _, err := ValidateGateAction(kind, action); err != nil {
		return Binding{}, err
	}
	return Binding{GateKind: kind, Action: action, Target: values["target"][0]}, nil
}

func (b Binding) Matches(kind, action, target string) bool {
	item, err := ValidateGateAction(kind, action)
	return err == nil && b.GateKind == item.GateKind && b.Action == item.Action && b.Target == strings.TrimSpace(target)
}

func NormalizeGateKind(raw string) (string, error) {
	kind := NormalizeAction(raw)
	for _, item := range actionCatalog {
		if item.GateKind == kind {
			return kind, nil
		}
	}
	return "", fmt.Errorf("unknown gate kind %q", raw)
}

func KnownGateKinds() []string {
	seen := map[string]bool{}
	for _, item := range actionCatalog {
		seen[item.GateKind] = true
	}
	out := make([]string, 0, len(seen))
	for kind := range seen {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func HumanOnlyGateKind(kind string) bool {
	kind, err := NormalizeGateKind(kind)
	if err != nil {
		return true
	}
	for _, item := range actionCatalog {
		if item.GateKind == kind && item.SelfEligible {
			return false
		}
	}
	return true
}

func ValidateAllowlist(kinds []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(kinds))
	for _, raw := range kinds {
		kind, err := NormalizeGateKind(raw)
		if err != nil {
			return nil, err
		}
		if HumanOnlyGateKind(kind) {
			return nil, fmt.Errorf("gate kind %q is immutable human-only", kind)
		}
		if !seen[kind] {
			seen[kind] = true
			out = append(out, kind)
		}
	}
	sort.Strings(out)
	return out, nil
}

func Evaluate(kind, action string, allowlist []string) error {
	item, err := ValidateGateAction(kind, action)
	if err != nil {
		return err
	}
	if item.HumanOnly || !item.SelfEligible {
		return fmt.Errorf("gate kind %q action %q is immutable human-only", item.GateKind, item.Action)
	}
	allowed := false
	for _, raw := range allowlist {
		normalized, normalizeErr := NormalizeGateKind(raw)
		if normalizeErr == nil && normalized == item.GateKind {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("gate kind %q is not allowlisted", item.GateKind)
	}
	return nil
}
