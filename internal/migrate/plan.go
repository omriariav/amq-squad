// Package migrate is the one-shot converter from a v2 profile to squad.yaml
// schema 1 (decision 6: a converter, not a compatibility layer; brief
// v3-5-migrate part B). It reads the v2 files read-only, maps every field
// that has a 3.0 meaning, names every field that has none, and writes only
// what the owner asked for with --write: squad.yaml when absent, the staged
// role prompts when absent and grants.yaml.proposed beside the owner's files.
// It never writes grants.yaml or registry.yaml, never trusts anything, never
// copies an environment block, and deletes nothing unless asked to.
package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/catalog"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Options configure Plan.
type Options struct {
	// Profile is the v2 profile file: <project>/.amq-squad/team.json or
	// <project>/.amq-squad/teams/<name>.json.
	Profile string
	// OwnerPath is the owner's grants.yaml; "" is grants.OwnerPath(). The
	// proposal is written beside it. migrate only ever reads it.
	OwnerPath string
}

// Unmapped is a v2 key (or file) with no 3.0 meaning.
type Unmapped struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// Write is one file migrate would write.
type Write struct {
	Path    string
	Mode    os.FileMode
	Content []byte
	Note    string
}

// Plan is the full result of a migrate: everything it read, mapped,
// found unmapped and would write. Producing a Plan changes nothing on disk.
type Plan struct {
	Project     string
	ProfilePath string
	ProfileName string
	Read        []string
	Squad       team.Squad
	SquadText   string
	Unmapped    []Unmapped
	Kept        []string // v2 files 3.0 keeps reading at the same path
	Dead        []string // v2 files dead under decision 6, left in place
	Writes      []Write
	TrustCmd    string // the owner-run command, "" when no role prompt was found
}

const (
	reasonNoMeaning = "no 3.0 meaning"
)

// topReasons are the reasons for the profile keys main's loader reads that
// squad.yaml has no field for.
var topReasons = map[string]string{
	"trust":               "3.0 launches Codex under its own sandbox and approval flags; trust is the owner's codex-cli step (amq-squad trust)",
	"binary_args":         "3.0 compiles provider arguments from the typed seat fields",
	"drafter":             "3.0 drafts through the kept user config, not a profile block",
	"composition":         "3.0 starts the seats squad.yaml lists; runtime composition is the scheduler's",
	"autonomous":          reasonNoMeaning,
	"goal_supervision":    reasonNoMeaning,
	"target_contract":     reasonNoMeaning,
	"lead_execution":      reasonNoMeaning,
	"max_spawn_depth":     reasonNoMeaning,
	"created_at":          reasonNoMeaning,
	"orchestrated":        "3.0 names the lead with lead; the flag has no meaning",
	"workstream":          "a deprecated v2 shim; 3.0 names the session with session",
	"lead":                "no seat has this role, so no 3.0 lead",
	"execution_mode":      "not one of the four 3.0 execution modes",
	"control_root":        "not an absolute or project-relative path",
	"target_project_root": "not an absolute or project-relative path",
}

var memberReasons = map[string]string{
	"tool_config":            "the materialized policy file of a v2 tool profile; 3.0 derives it from tools",
	"tool_mcp_config":        "the materialized MCP config of a v2 tool profile; 3.0 derives it from tools",
	"tool_blocklist":         "3.0 derives the blocked set from tools",
	"tool_policy_sources":    "audit metadata of the v2 tool policy",
	"tool_policy_drift":      "audit metadata of the v2 tool policy",
	"tool_disable_all_hooks": "3.0 always runs its guard hooks",
	"spawn_origin":           "runtime composition metadata of v2",
	"spawn_depth":            "runtime composition metadata of v2",
	"claude_args":            "3.0 compiles provider arguments from the typed seat fields",
	"codex_args":             "3.0 compiles provider arguments from the typed seat fields",
	"session":                "differs from the squad's session; 3.0 has one session per squad",
}

// Build reads the v2 files and plans the migration. It writes nothing.
func Build(opts Options) (*Plan, error) {
	in, err := readInputs(opts.Profile)
	if err != nil {
		return nil, err
	}
	squadPath := team.SquadPath(in.Project)
	if _, err := os.Lstat(squadPath); err == nil {
		return nil, &Refusal{Code: CodeSquadExists, Fields: []string{rel(in.Project, squadPath)}, Detail: "squad.yaml is already present; migrate never overwrites it"}
	}
	if err := checkCredentials(in); err != nil {
		return nil, err
	}
	m := &mapper{in: in, unmapped: map[string]string{}}
	if err := m.run(); err != nil {
		return nil, err
	}
	p := &Plan{Project: in.Project, ProfilePath: in.ProfilePath, ProfileName: in.ProfileName, Read: in.Read, Squad: m.squad}
	p.SquadText = team.Render(m.squad)
	p.Writes = append(p.Writes, Write{Path: squadPath, Mode: 0o644, Content: []byte(p.SquadText), Note: fmt.Sprintf("squad.yaml, %d seats", len(m.squad.Seats))})
	m.rolePrompts(p)
	m.files(p)
	m.proposal(p, opts)
	if proposedExists(p) {
		return nil, &Refusal{Code: CodeProposedExist, Fields: []string{proposedName}, Detail: "grants.yaml.proposed is already present beside the owner's files; migrate never overwrites it. Read or delete it and run again"}
	}
	for k, v := range m.unmapped {
		p.Unmapped = append(p.Unmapped, Unmapped{Key: k, Reason: v})
	}
	sort.Slice(p.Unmapped, func(i, j int) bool { return p.Unmapped[i].Key < p.Unmapped[j].Key })
	if err := validateOutputs(p, opts); err != nil {
		return nil, err
	}
	return p, nil
}

func ownerPath(opts Options) string {
	if opts.OwnerPath != "" {
		return opts.OwnerPath
	}
	return grants.OwnerPath()
}

// validateOutputs refuses an output that holds a credential shape or that
// would not load: the squad.yaml migrate writes must parse against the
// owner's grant, and the proposal must parse as an owner file.
func validateOutputs(p *Plan, opts Options) error {
	var fields []string
	for i, w := range p.Writes {
		if unsafeText(string(w.Content)) {
			fields = append(fields, fmt.Sprintf("output file #%d (%s)", i+1, w.Note))
		}
	}
	for _, wrote := range []bool{false, true} {
		if unsafeText(p.Report(wrote)) {
			fields = append(fields, "the printed report")
			break
		}
	}
	if len(fields) > 0 {
		return &Refusal{Code: CodeCredential, Fields: fields, Detail: "an output has the shape of a real credential; nothing is written"}
	}
	owner := grants.Defaults()
	if r, err := grants.LoadFrom(ownerPath(opts), ""); err == nil && !r.Insecure {
		owner = r.Grants
	}
	if _, err := team.ParseSquad(p.SquadText, team.SquadPath(p.Project), owner); err != nil {
		return &Refusal{Code: CodeSquadInvalid, Detail: fmt.Sprintf("the squad.yaml this profile maps to does not load: %v", err)}
	}
	for _, w := range p.Writes {
		if filepath.Base(w.Path) == proposedName {
			if _, err := grants.ParseOwner(string(w.Content), w.Path); err != nil {
				return &Refusal{Code: CodeSquadInvalid, Detail: fmt.Sprintf("the grants proposal does not parse as an owner file: %v", err)}
			}
		}
	}
	return nil
}

// checkCredentials scans every input for the shape of a real credential and
// refuses, naming the field and never the value (B8).
func checkCredentials(in *inputs) error {
	rp := func(p string) string { return rel(in.Project, p) }
	fields := pathCredentialFields(in)
	fields = append(fields, credentialFields(rp(in.ProfilePath), in.Profile)...)
	fields = append(fields, credentialKeyFields(rp(in.ProfilePath), in.Profile)...)
	if in.LaunchLocal != nil {
		fields = append(fields, scanFile(rp(in.LaunchLocal.Path), in.LaunchLocal.Data)...)
	}
	texts := []file{}
	if in.TeamRules != nil {
		texts = append(texts, *in.TeamRules)
	}
	texts = append(texts, in.Briefs...)
	for _, r := range in.AgentRoleMDs {
		texts = append(texts, r.file)
	}
	for _, f := range in.StagedRoles {
		texts = append(texts, f)
	}
	for _, f := range texts {
		if looksLikeCredential(string(f.Data)) {
			fields = append(fields, rp(f.Path))
		}
	}
	if len(fields) == 0 {
		return nil
	}
	sort.Strings(fields)
	return &Refusal{Code: CodeCredential, Fields: fields, Detail: "a v2 file holds a value with the shape of a real credential; migrate copies no env block and writes nothing. Move the credential out of the file and run again"}
}

// scanFile names the credential fields of a JSON file, or the file when it
// is not JSON.
func scanFile(name string, data []byte) []string {
	var v any
	if err := jsonUnmarshal(data, &v); err != nil {
		if looksLikeCredential(string(data)) {
			return []string{name}
		}
		return nil
	}
	return append(credentialFields(name, v), credentialKeyFields(name, v)...)
}

// pathCredentialFields names a credential-shaped input file name without
// printing it: the number of the input file (the order Plan.Read lists them).
// A credential-shaped profile or project path is refused earlier, by
// readInputs, as "the profile path".
func pathCredentialFields(in *inputs) []string {
	var out []string
	for i, p := range in.Read {
		if looksLikeCredential(p) {
			out = append(out, fmt.Sprintf("the name of input file #%d", i+1))
		}
	}
	return out
}

func rel(project, path string) string {
	if r, err := filepath.Rel(project, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return path
}

// roleKnown reports a role the catalog defines.
func roleKnown(id string) bool { return catalog.Lookup(id) != nil }
