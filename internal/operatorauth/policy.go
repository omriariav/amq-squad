package operatorauth

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

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
	return Binding{GateKind: kind, Action: NormalizeAction(values["action"][0]), Target: values["target"][0]}, nil
}

func (b Binding) Matches(kind, action, target string) bool {
	normalizedKind, err := NormalizeGateKind(kind)
	return err == nil && b.GateKind == normalizedKind && b.Action == NormalizeAction(action) && b.Target == strings.TrimSpace(target)
}

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

var knownGateKinds = map[string]bool{
	GateSpawn: true, GateMerge: true, GateFeatureBranchPush: true,
	GateRelease: true, GateTag: true, GatePublish: true,
	GateExternalSend: true, GateDestructiveFilesystem: true,
}

var humanOnlyKinds = map[string]bool{
	GateRelease: true, GateTag: true, GatePublish: true,
	GateExternalSend: true, GateDestructiveFilesystem: true,
}

func NormalizeGateKind(raw string) (string, error) {
	kind := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(raw)))
	if !knownGateKinds[kind] {
		return "", fmt.Errorf("unknown gate kind %q", raw)
	}
	return kind, nil
}

func KnownGateKinds() []string {
	out := make([]string, 0, len(knownGateKinds))
	for kind := range knownGateKinds {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func HumanOnlyGateKind(kind string) bool { return humanOnlyKinds[kind] || !knownGateKinds[kind] }

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

func NormalizeAction(raw string) string {
	return strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(raw)))
}

func Evaluate(kind, action string, allowlist []string) error {
	kind, err := NormalizeGateKind(kind)
	if err != nil {
		return err
	}
	if HumanOnlyGateKind(kind) {
		return fmt.Errorf("gate kind %q is immutable human-only", kind)
	}
	action = NormalizeAction(action)
	switch kind {
	case GateSpawn:
		if action != "spawn" {
			return fmt.Errorf("gate/action mismatch: %s cannot authorize %s", kind, action)
		}
	case GateMerge:
		if action != "protected_branch_push" {
			return fmt.Errorf("gate/action mismatch: %s cannot authorize %s", kind, action)
		}
	case GateFeatureBranchPush:
		if action != "feature_branch_push" {
			return fmt.Errorf("gate/action mismatch: %s cannot authorize %s", kind, action)
		}
	default:
		return fmt.Errorf("gate kind %q is immutable human-only", kind)
	}
	allowed := false
	for _, item := range allowlist {
		if item == kind {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("gate kind %q is not allowlisted", kind)
	}
	return nil
}
