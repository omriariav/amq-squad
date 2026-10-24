package routing

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/yamlsub"
)

// The routing.yaml format: the strict YAML subset of amendment 27, read
// through internal/yamlsub, so anything outside the subset (anchors, aliases,
// tags, flow maps, a second document, a repeated key) and any unknown key is
// an error, never ignored.
//
//	schema: 1
//	exploration_share: 0.05          # 0 disables; never plan or review
//	classes:
//	  implement:
//	    tier: task                   # below the plan 6.1 tier only with a mechanical verifier
//	    required_companions: [SKILL.md]
//	tiers:
//	  smol:
//	    max_cost_index: 2            # 0: no cap
//	    chain:
//	      - "pi:ollama/qwen3.6:27b:low"   # runtime:model:effort, as task --pool
//	# repository file only:
//	privacy_paths:
//	  - glob: internal/secrets/
//	    class: local                 # local paths never go to a vendor
//	sensitive_paths: [internal/auth/] # implement escalates to plan
//	no_tests: false                  # true: implement escalates to plan
//
// A class row replaces the fields it names; a tier's chain is replaced as a
// whole. Privacy path rules accumulate across files and never narrow.

// UserPath is ~/.amq-squad/routing.yaml under home ("" home: the user's home).
func UserPath(home string) string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", "routing.yaml")
}

// RepoPath is <repo root>/.amq-squad/routing.yaml.
func RepoPath(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	return filepath.Join(repoRoot, ".amq-squad", "routing.yaml")
}

// Load is the effective table: the default, then the user file, then the
// repository file. A missing file is skipped; a file that does not parse or
// breaks a table invariant is an error (the caller routes nothing).
func Load(home, repoRoot string) (Table, error) {
	t := Default()
	for _, f := range []struct {
		path string
		repo bool
	}{{UserPath(home), false}, {RepoPath(repoRoot), true}} {
		if f.path == "" {
			continue
		}
		raw, err := os.ReadFile(f.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Table{}, fmt.Errorf("read %s: %w", f.path, err)
		}
		if t, err = Apply(t, string(raw), f.path, f.repo); err != nil {
			return Table{}, err
		}
	}
	return t, nil
}

// Apply overlays one routing.yaml onto base. repo admits the repository-only
// keys. The result is validated.
func Apply(base Table, text, source string, repo bool) (Table, error) {
	t := base.Clone()
	entries, err := yamlsub.Split(text, source, true)
	if err != nil {
		return Table{}, err
	}
	fail := func(err error) (Table, error) { return Table{}, fmt.Errorf("%s: %w", source, err) }
	sawSchema := false
	for _, e := range entries {
		var err error
		switch e.Key {
		case "schema":
			var n int
			if n, err = e.Integer(1); err == nil && n != Schema {
				err = e.Errf("schema %d is not supported (want %d)", n, Schema)
			}
			sawSchema = true
		case "exploration_share":
			t.ExplorationShare, err = share(e)
		case "classes":
			err = applyClasses(&t, e)
		case "tiers":
			err = applyTiers(&t, e)
		case "privacy_paths", "sensitive_paths", "no_tests":
			if !repo {
				err = e.Errf("only the repository file %s may set it", ".amq-squad/routing.yaml")
				break
			}
			switch e.Key {
			case "privacy_paths":
				var rules []PathRule
				if rules, err = privacyRules(e); err == nil {
					t.PrivacyPaths = append(t.PrivacyPaths, rules...)
				}
			case "sensitive_paths":
				var globs []string
				if globs, err = globList(e); err == nil {
					t.SensitivePaths = append(t.SensitivePaths, globs...)
				}
			case "no_tests":
				t.NoTests, err = e.Boolean()
			}
		default:
			err = e.Errf("unknown key")
		}
		if err != nil {
			return fail(err)
		}
	}
	if !sawSchema {
		return fail(fmt.Errorf("schema: missing (want schema: %d)", Schema))
	}
	if err := t.Validate(); err != nil {
		return fail(err)
	}
	t.Sources = append(t.Sources, source)
	return t, nil
}

func share(e yamlsub.Entry) (float64, error) {
	s, err := e.Scalar()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 1 {
		return 0, e.Errf("want a share from 0 to 1, got %q", s)
	}
	return v, nil
}

func nonNegative(e yamlsub.Entry) (float64, error) {
	s, err := e.Scalar()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, e.Errf("want a number at or above 0, got %q", s)
	}
	return v, nil
}

func leaf(e yamlsub.Entry) string { return e.Key[strings.LastIndex(e.Key, ".")+1:] }

func applyClasses(t *Table, e yamlsub.Entry) error {
	rows, err := e.Map()
	if err != nil {
		return err
	}
	for _, row := range rows {
		class := leaf(row)
		if !task.ValidClass(class) {
			return row.Errf("class %q is not in the closed list %s", class, strings.Join(task.Classes, ", "))
		}
		fields, err := row.Map()
		if err != nil {
			return err
		}
		spec := t.Classes[class]
		for _, f := range fields {
			switch leaf(f) {
			case "tier":
				spec.Tier, err = f.OneOf(Tiers...)
			case "required_companions":
				spec.RequiredCompanions, err = globList(f)
			case "verifier":
				err = f.Errf("the verifier is fixed by plan 6.1")
			default:
				err = f.Errf("unknown key")
			}
			if err != nil {
				return err
			}
		}
		t.Classes[class] = spec
	}
	return nil
}

func applyTiers(t *Table, e yamlsub.Entry) error {
	rows, err := e.Map()
	if err != nil {
		return err
	}
	for _, row := range rows {
		tier := leaf(row)
		if TierRank(tier) < 0 {
			return row.Errf("unknown tier %q (want one of %s)", tier, strings.Join(Tiers, ", "))
		}
		fields, err := row.Map()
		if err != nil {
			return err
		}
		spec := t.Tiers[tier]
		for _, f := range fields {
			switch leaf(f) {
			case "chain":
				var items []string
				if items, err = f.List(); err == nil {
					spec.Chain = nil
					for _, it := range items {
						pl, perr := task.ParsePool(it)
						if perr != nil || pl.Effort == "" {
							err = f.Errf("chain entry %q: want runtime:model:effort", it)
							break
						}
						spec.Chain = append(spec.Chain, pl)
					}
				}
			case "max_cost_index":
				spec.MaxCostIndex, err = nonNegative(f)
			default:
				err = f.Errf("unknown key")
			}
			if err != nil {
				return err
			}
		}
		t.Tiers[tier] = spec
	}
	return nil
}

func globList(e yamlsub.Entry) ([]string, error) {
	items, err := e.List()
	if err != nil {
		return nil, err
	}
	out, err := task.CleanDeclaredFiles(items)
	if err != nil {
		return nil, e.Errf("%v", err)
	}
	return out, nil
}

func privacyRules(e yamlsub.Entry) ([]PathRule, error) {
	items, err := e.MapItems()
	if err != nil {
		return nil, err
	}
	var out []PathRule
	for _, it := range items {
		var r PathRule
		for _, f := range it {
			switch leaf(f) {
			case "glob":
				var g string
				if g, err = f.Scalar(); err == nil {
					var clean []string
					if clean, err = task.CleanDeclaredFiles([]string{g}); err == nil && len(clean) == 1 {
						r.Glob = clean[0]
					} else if err == nil {
						err = f.Errf("empty glob")
					}
				}
			case "class":
				r.Class, err = f.OneOf(grants.PrivacyLocal, grants.PrivacyVendor)
			default:
				err = f.Errf("unknown key")
			}
			if err != nil {
				return nil, err
			}
		}
		if r.Glob == "" || r.Class == "" {
			return nil, e.Errf("each privacy path needs glob and class")
		}
		out = append(out, r)
	}
	return out, nil
}

// RequiredPrivacy is the privacy class a task's declared files require under
// the table's path rules: local when any file may match a local glob, else
// vendor. A literal declared path is matched exactly (with "**" spanning
// segments); a declared glob or tree uses the conservative overlap of the
// file locks, so a false match keeps a task local and a missed one never
// happens.
func (t Table) RequiredPrivacy(files []string) (string, string) {
	for _, r := range t.PrivacyPaths {
		if r.Class == grants.PrivacyLocal && mayTouch(files, r.Glob) {
			return grants.PrivacyLocal, r.Glob
		}
	}
	return grants.PrivacyVendor, ""
}

// Sensitive reports whether a declared file may touch a sensitive path.
func (t Table) Sensitive(files []string) bool {
	for _, g := range t.SensitivePaths {
		if mayTouch(files, g) {
			return true
		}
	}
	return false
}

func mayTouch(files []string, glob string) bool {
	for _, f := range files {
		if strings.ContainsAny(f, "*?[") || strings.HasSuffix(f, "/") {
			if task.FilesOverlap([]string{f}, []string{glob}) {
				return true
			}
			continue
		}
		if MatchPath(glob, f) {
			return true
		}
	}
	return false
}

// MatchPath matches a literal repository path against a declared-files glob:
// path.Match per segment, "**" as a segment matches any number of segments,
// a trailing "/" or "/**" covers the tree below.
func MatchPath(glob, p string) bool {
	switch {
	case strings.HasSuffix(glob, "/**"):
		glob = strings.TrimSuffix(glob, "/**") + "/"
	}
	if strings.HasSuffix(glob, "/") {
		glob += "**"
	}
	return matchSegs(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchSegs(g, p []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			for i := 0; i <= len(p); i++ {
				if matchSegs(g[1:], p[i:]) {
					return true
				}
			}
			return false
		}
		if len(p) == 0 {
			return false
		}
		if ok, _ := path.Match(g[0], p[0]); !ok {
			return false
		}
		g, p = g[1:], p[1:]
	}
	return len(p) == 0
}
