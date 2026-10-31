package team

import (
	"errors"
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/yamlsub"
)

// The final form of squad.yaml schema 1 (brief v3-5-migrate, part A; frozen
// for 3.0): the complete key list with each key's relation to the owner
// grant, the keys no squad.yaml can set, and the error codes a reader
// reports. docs/squad-yaml.md carries the same table; a test keeps the two
// equal.

// Relation is a key's relation to the owner grant.
type Relation string

const (
	RelPlain  Relation = "plain"  // describes the squad; touches no grant
	RelNarrow Relation = "narrow" // can only narrow the owner grant
	RelAdd    Relation = "add"    // can only add (a path, a verifier class)
)

// SquadKey is one key of schema 1.
type SquadKey struct {
	Name     string
	Type     string
	Default  string
	Relation Relation
}

// SquadKeys is the complete key list of schema 1, top-level keys first and
// the keys of `defaults` and of a seat after them (written as defaults.x and
// seats[].x). A key outside this list and outside NeverSettableKeys is an
// unknown key and a parse error.
var SquadKeys = []SquadKey{
	{"schema", "integer, 1", "none (required)", RelPlain},
	{"session", "AMQ session name", "none", RelPlain},
	{"lead", "a seat handle", "none", RelPlain},
	{"lead_mode", "builder | planner", "builder", RelPlain},
	{"execution_mode", "global_orchestrator | project_lead | project_team | direct_lead_session", "none", RelPlain},
	{"control_root", "absolute path", "none", RelPlain},
	{"target_project_root", "absolute path", "none", RelPlain},
	{"shared_cwd_exception", "text", "none", RelPlain},
	{"operator_interaction", "lead_pane | separate_terminal | noc", "none", RelPlain},
	{"defaults", "map of runtime, model, effort, tools", "empty", RelPlain},
	{"seats", "list of seat maps, at least one", "none (required)", RelPlain},
	{"mode", "auto | manual", "the owner's", RelNarrow},
	{"max_seats", "integer, at least 1", "the owner's", RelNarrow},
	{"cost_per_day_usd", "number", "the owner's", RelNarrow},
	{"permitted_runtimes", "list of runtimes", "the owner's", RelNarrow},
	{"privacy_class", "vendor | local", "the owner's", RelNarrow},
	{"protected_paths", "list of paths", "empty", RelAdd},
	{"merge_repositories", "list of owner/name", "the owner's merge grants", RelNarrow},
	{"merge_size_cap_lines", "integer, at least 1", "the owner's", RelNarrow},
	{"merge_size_cap_files", "integer, at least 1", "the owner's", RelNarrow},
	{"verifier_classes", "list of verifier classes", "the owner's", RelAdd},
	{"defaults.runtime", "claude | codex | cursor-agent | grok | pi", "none", RelPlain},
	{"defaults.model", "token", "none", RelPlain},
	{"defaults.effort", "token", "none", RelPlain},
	{"defaults.tools", "minimal | coding | browser | data | full", "full", RelPlain},
	{"seats[].handle", "[a-z0-9][a-z0-9_-]*, unique", "none (required)", RelPlain},
	{"seats[].role", "catalog or staged custom role id", "the handle", RelPlain},
	{"seats[].runtime", "claude | codex | cursor-agent | grok | pi", "defaults.runtime", RelPlain},
	{"seats[].model", "token (PROVIDER/ID for pi)", "defaults.model (not for pi)", RelPlain},
	{"seats[].effort", "token (not for cursor-agent)", "defaults.effort (not for pi or cursor-agent)", RelPlain},
	{"seats[].worktree", "repo-relative or absolute path, never the repository root", ".worktrees/<handle>", RelPlain},
	{"seats[].tools", "minimal | coding | browser | data | full", "defaults.tools", RelPlain},
	{"seats[].actor_mode", "implementation | review", "derived", RelPlain},
}

// neverSettable are the keys only the owner's files set. A squad.yaml that
// carries one is refused at parse with the key named, whatever its value:
// the repository file is something a seat can put in its diff (A5), so no
// grant, push target, signature mode, verifier command or pin may come
// from it. Each entry covers the key itself and every dotted or nested
// form under it (merge.push_url, verifiers.implement).
var neverSettable = []struct{ Key, Owner string }{
	{"merge", "grants.yaml: the standing merge grants, with merge.push_url and merge.local_push_ack"},
	{"merge.push_url", "grants.yaml: the push target"},
	{"merge.local_push_ack", "grants.yaml: the owner's acknowledgement of a local push target"},
	{"push_url", "grants.yaml: the push target (merge.push_url)"},
	{"local_push_ack", "grants.yaml: merge.local_push_ack"},
	{"verifiers", "grants.yaml: the verifier command per class"},
	{"verifier_goproxy", "grants.yaml: the verifier's GOPROXY"},
	{"verifier_timeout", "grants.yaml: the verifier's wall time"},
	{"verifier_output_limit", "grants.yaml: the verifier's output bound"},
	{"verifier_parallelism", "grants.yaml: verifiers running at once"},
	{"default_branch", "grants.yaml: the default branch"},
	{"fleet", "grants.yaml: the operator session's fleet grant"},
	{"max_squads", "grants.yaml: fleet.max_squads"},
	{"permitted_repositories", "grants.yaml: fleet.permitted_repositories"},
	{"pins", "registry.yaml: the owner-pinned harness tools"},
	{"registry", "registry.yaml: the registry and its pins"},
	{"unprotected_paths", "a removal from the protected set; squad.yaml only adds"},
	{"remove_protected_paths", "a removal from the protected set; squad.yaml only adds"},
}

// NeverSettableKeys lists the never-settable keys in order.
func NeverSettableKeys() []string {
	out := make([]string, len(neverSettable))
	for i, k := range neverSettable {
		out[i] = k.Key
	}
	return out
}

// NeverSettableOwner says where the owner sets a never-settable key ("" when
// key is not one).
func NeverSettableOwner(key string) string {
	for _, k := range neverSettable {
		if key == k.Key || strings.HasPrefix(key, k.Key+".") {
			return k.Owner
		}
	}
	return ""
}

// neverSettablePath is the full path of the never-settable key entry e
// carries: for a block (`merge:` with `push_url:` under it) the first nested
// key the table lists exactly (merge.push_url), else the entry's own key.
func neverSettablePath(e yamlsub.Entry) string {
	for _, b := range e.Block {
		sub, _, ok := strings.Cut(strings.TrimPrefix(b.Text, "- "), ":")
		if !ok {
			continue
		}
		path := e.Key + "." + strings.TrimSpace(sub)
		for _, k := range neverSettable {
			if k.Key == path {
				return path
			}
		}
	}
	return e.Key
}

// grantKeyNames are the top-level keys of the table that touch the grant
// (narrow or add): the keys grants.ParseSquadNarrowing reads. The parser's
// accepted set is built from SquadKeys, not listed a second time.
func grantKeyNames() []string {
	var out []string
	for _, k := range SquadKeys {
		if k.Relation == RelNarrow || k.Relation == RelAdd {
			out = append(out, k.Name)
		}
	}
	return out
}

// IsNeverSettable reports whether key, or the key it is nested under, is one
// only the owner's files set.
func IsNeverSettable(key string) bool { return NeverSettableOwner(key) != "" }

// Codes of a squad.yaml refusal, as doctor and the verbs report them.
const (
	CodeInvalid           = "squad_yaml_invalid"            // a parse or validation error
	CodeGrantsInvalid     = "grants_invalid"                // a field that would widen, or set, the owner grant
	CodeSchemaUnsupported = "squad_yaml_schema_unsupported" // a schema this build does not read
	CodeGoldenDrift       = "squad_yaml_golden_drift"       // doctor's self-check: the embedded parse drifted
	CodeDigestMismatch    = "squad_yaml_digest_mismatch"    // apply: the proposal's content digest is not the plan's
	CodeSquadChanged      = "squad_yaml_changed"            // apply: squad.yaml on disk is not the file the proposal read
)

// SchemaError is a squad.yaml of a schema this build does not read (a 3.0.x
// reader and schema 2).
type SchemaError struct {
	Source  string
	Version int
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("%s: %s: schema %d is not readable by this build, which reads schema %d only (squad.yaml schema 1 is frozen for 3.0; upgrade amq-squad)", e.Source, CodeSchemaUnsupported, e.Version, SquadSchema)
}

// CodeOf is the code of a ParseSquad error: grants_invalid for a widening or
// owner-only key, squad_yaml_schema_unsupported for a schema this build does
// not read, squad_yaml_invalid for everything else.
func CodeOf(err error) string {
	var schema *SchemaError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &schema):
		return CodeSchemaUnsupported
	case errors.Is(err, ErrWidens):
		return CodeGrantsInvalid
	}
	return CodeInvalid
}
