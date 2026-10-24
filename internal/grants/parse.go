package grants

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/yamlsub"
)

// The file format is the strict YAML subset of internal/yamlsub. Schema 1 of
// the owner file:
// "k: scalar" / "k:" + list lines. Schema 1 of the owner file:
//
//	schema: 1
//	mode: auto                    # auto | manual (plan 3.4)
//	max_seats: 12                 # session seat cap (plan 7.2)
//	cost_per_day_usd: 40          # optional
//	permitted_runtimes: [claude, codex, pi]   # optional; absent = any
//	privacy_class: vendor         # vendor | local
//	default_branch: main          # optional; else origin/HEAD, else main
//	protected_paths:              # ~/, absolute or repo-relative; trailing / = tree; * ? [ globs
//	  - conf/*.yaml
//	merge:                        # standing merge grants (plan 7.2, R34)
//	  - repository: omriariav/amq-noc
//	    base_branch: main
//	    ci_required: true
//	    size_cap_lines: 300       # optional; default 300 (3-2, S6 dry run)
//	    size_cap_files: 5         # optional; default 5 (3-2, S6 dry run)
//	    required_companions: [skills/*/SKILL.md]   # optional; default none (the owner names them)
//	    push_url: git@github.com:omriariav/amq-noc.git   # optional: ssh, https://github.com, absolute path or file://
//	    local_push_ack: false     # required true for a local push_url (grants_invalid without); the merge train still refuses a real run against one
//	fleet:                        # reserved for the operator session (4-3)
//	  max_squads: 4
//	  cost_per_day_usd: 100
//	  permitted_repositories:
//	    - omriariav/amq-noc

var (
	repoPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
	branchPattern  = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	runtimePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

func pathList(e yamlsub.Entry) ([]string, error) {
	items, err := e.List()
	if err != nil {
		return nil, err
	}
	for _, p := range items {
		if strings.HasPrefix(p, ManagedPrefix) || strings.ContainsAny(p, "\x00\r\n") {
			return nil, e.Errf("bad path %q", p)
		}
	}
	return items, nil
}

func runtimeList(e yamlsub.Entry) ([]string, error) {
	items, err := e.List()
	if err != nil {
		return nil, err
	}
	for _, r := range items {
		if !runtimePattern.MatchString(r) {
			return nil, e.Errf("bad runtime %q", r)
		}
	}
	return items, nil
}

// ParseOwner parses the owner file strictly: every key known, every value
// valid, schema 1 when given.
func ParseOwner(text, source string) (Grants, error) {
	g := Defaults()
	entries, err := yamlsub.Split(text, source, true)
	if err != nil {
		return g, err
	}
	wrap := func(err error) error { return fmt.Errorf("%s: %w", source, err) }
	for _, e := range entries {
		var err error
		switch e.Key {
		case "schema":
			var v int
			if v, err = e.Integer(1); err == nil && v != Schema {
				err = e.Errf("schema %d is not %d", v, Schema)
			}
		case "mode":
			g.Mode, err = e.OneOf(ModeAuto, ModeManual)
		case "max_seats":
			g.MaxSeats, err = e.Integer(1)
		case "cost_per_day_usd":
			g.CostPerDayUSD, err = e.Money()
		case "permitted_runtimes":
			g.PermittedRuntimes, err = runtimeList(e)
		case "privacy_class":
			g.PrivacyClass, err = e.OneOf(PrivacyVendor, PrivacyLocal)
		case "default_branch":
			if g.DefaultBranch, err = e.Scalar(); err == nil && !branchPattern.MatchString(g.DefaultBranch) {
				err = e.Errf("bad branch %q", g.DefaultBranch)
			}
		case "protected_paths":
			g.ProtectedPaths, err = pathList(e)
		case "merge":
			g.Merge, err = parseMerge(e)
		case "fleet":
			g.Fleet, err = parseFleet(e)
		default:
			err = e.Errf("unknown key")
		}
		if err != nil {
			return g, wrap(err)
		}
	}
	return g, nil
}

func parseMerge(e yamlsub.Entry) ([]Merge, error) {
	items, err := e.MapItems()
	if err != nil {
		return nil, err
	}
	var out []Merge
	for _, it := range items {
		m := Merge{CIRequired: true, SizeCapLines: DefaultSizeCapLines, SizeCapFiles: DefaultSizeCapFiles}
		for _, f := range it {
			var err error
			switch f.Key {
			case "merge.repository":
				if m.Repository, err = f.Scalar(); err == nil && !repoPattern.MatchString(m.Repository) {
					err = f.Errf("want owner/name, got %q", m.Repository)
				}
			case "merge.base_branch":
				if m.BaseBranch, err = f.Scalar(); err == nil && !branchPattern.MatchString(m.BaseBranch) {
					err = f.Errf("bad branch %q", m.BaseBranch)
				}
			case "merge.ci_required":
				m.CIRequired, err = f.Boolean()
			case "merge.size_cap_lines":
				m.SizeCapLines, err = f.Integer(1)
			case "merge.size_cap_files":
				m.SizeCapFiles, err = f.Integer(1)
			case "merge.required_companions":
				m.RequiredCompanions, err = f.List()
			case "merge.push_url":
				if m.PushURL, err = f.Scalar(); err == nil && PushKind(m.PushURL) == "" {
					err = f.Errf("bad push_url %q: an absolute ssh (ssh:// or user@host:path), https://github.com/owner/name, or local (absolute path or file://) URL", m.PushURL)
				}
			case "merge.local_push_ack":
				m.LocalPushAck, err = f.Boolean()
			default:
				// force push and protected-path changes are fixed refusals, not fields
				err = f.Errf("unknown key")
			}
			if err != nil {
				return nil, err
			}
		}
		if m.Repository == "" || m.BaseBranch == "" {
			return nil, e.Errf("each merge grant needs repository and base_branch")
		}
		if PushKind(m.PushURL) == PushLocal && !m.LocalPushAck {
			return nil, e.Errf("merge grant for %s: the local push_url %q needs local_push_ack: true (a local target has no server-side ruleset)", m.Repository, m.PushURL)
		}
		if _, dup := (Grants{Merge: out}).MergeFor(m.Repository, m.BaseBranch); dup {
			return nil, e.Errf("merge grant for %s %s repeated", m.Repository, m.BaseBranch)
		}
		out = append(out, m)
	}
	return out, nil
}

func parseFleet(e yamlsub.Entry) (Fleet, error) {
	var f Fleet
	if e.Value != "" {
		return f, e.Errf("want a map")
	}
	sub, err := yamlsub.Sub(e.Key, e.Block)
	if err != nil {
		return f, err
	}
	for _, s := range sub {
		switch s.Key {
		case "fleet.max_squads":
			f.MaxSquads, err = s.Integer(1)
		case "fleet.cost_per_day_usd":
			f.CostPerDayUSD, err = s.Money()
		case "fleet.repository_paths":
			f.RepositoryPaths, err = parseRepositoryPaths(s)
		case "fleet.permitted_repositories":
			if f.PermittedRepositories, err = s.List(); err == nil {
				for _, r := range f.PermittedRepositories {
					if !repoPattern.MatchString(r) {
						err = s.Errf("want owner/name, got %q", r)
					}
				}
			}
		default:
			err = s.Errf("unknown key")
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

func parseRepositoryPaths(e yamlsub.Entry) ([]RepositoryPath, error) {
	items, err := e.MapItems()
	if err != nil {
		return nil, err
	}
	var out []RepositoryPath
	for _, it := range items {
		var rp RepositoryPath
		for _, f := range it {
			var err error
			switch f.Key {
			case "fleet.repository_paths.repository":
				if rp.Repository, err = f.Scalar(); err == nil && !repoPattern.MatchString(rp.Repository) {
					err = f.Errf("want owner/name, got %q", rp.Repository)
				}
			case "fleet.repository_paths.path":
				if rp.Path, err = f.Scalar(); err == nil && (!filepath.IsAbs(rp.Path) || filepath.Clean(rp.Path) != rp.Path) {
					err = f.Errf("want an absolute clean path, got %q", rp.Path)
				}
			case "fleet.repository_paths.remote":
				if rp.Remote, err = f.Scalar(); err == nil && (strings.Contains(rp.Remote, "://") || !strings.Contains(rp.Remote, "/")) {
					err = f.Errf("want host/path without a transport, got %q", rp.Remote)
				}
			default:
				err = f.Errf("unknown key")
			}
			if err != nil {
				return nil, err
			}
		}
		if rp.Repository == "" || rp.Path == "" {
			return nil, e.Errf("each repository_paths entry needs repository and path")
		}
		out = append(out, rp)
	}
	return out, nil
}

// ParseSquadNarrowing reads the grant keys of a squad.yaml leniently: other
// top-level keys (the rest of 2b-5's schema) are skipped, but a grant key
// with a bad value is an error, never silently dropped. Grant keys: mode,
// max_seats, cost_per_day_usd, permitted_runtimes, privacy_class,
// protected_paths, merge_repositories, merge_size_cap_lines,
// merge_size_cap_files. default_branch
// is the owner's alone and is ignored here.
func ParseSquadNarrowing(text, source string) (Narrowing, error) {
	var n Narrowing
	entries, err := yamlsub.Split(text, source, false)
	if err != nil {
		return n, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		var err error
		switch e.Key {
		case "mode", "max_seats", "cost_per_day_usd", "permitted_runtimes", "privacy_class", "protected_paths", "merge_repositories", "merge_size_cap_lines", "merge_size_cap_files":
			// a repeated grant key is an error even in this lenient read: a later
			// value must never replace an earlier, narrower one
			if seen[e.Key] {
				return n, fmt.Errorf("%s: line %d: grant key %q repeated", source, e.Line, e.Key)
			}
			seen[e.Key] = true
		}
		switch e.Key {
		case "mode":
			n.Mode, err = e.OneOf(ModeAuto, ModeManual)
		case "max_seats":
			n.MaxSeats, err = e.Integer(1)
		case "cost_per_day_usd":
			n.CostPerDayUSD, err = e.Money()
		case "permitted_runtimes":
			n.PermittedRuntimes, err = runtimeList(e)
			n.RuntimesSet = err == nil
		case "privacy_class":
			n.PrivacyClass, err = e.OneOf(PrivacyVendor, PrivacyLocal)
		case "protected_paths":
			var paths []string
			paths, err = pathList(e)
			n.ProtectedPaths = append(n.ProtectedPaths, paths...) // paths only accumulate
		case "merge_repositories":
			n.MergeRepositories, err = e.List()
		case "merge_size_cap_lines":
			n.MergeSizeCapLines, err = e.Integer(1)
		case "merge_size_cap_files":
			n.MergeSizeCapFiles, err = e.Integer(1)
		case "fleet", "max_squads", "permitted_repositories":
			n.FleetKeys = append(n.FleetKeys, e.Key)
		}
		if err != nil {
			return n, fmt.Errorf("%s: %w", source, err)
		}
	}
	return n, nil
}
