package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Apply writes the plan's files, each only when absent, in order: squad.yaml,
// the staged role prompts, then grants.yaml.proposed. A file inside the
// project goes through the plan's one root descriptor (root.create: temp file
// beside the target, hard-linked into place, which fails if the target
// exists); the proposal, beside the owner's files, is created the same way by
// path. The plan refused everything it could before this point; a change
// during the apply (a file that appeared, a root that moved) stops at the
// first refusal. Apply returns the paths written so far with the refusal: it
// does not undo them (no transaction), and the caller reports the partial
// result.
func (p *Plan) Apply() ([]string, error) {
	var done []string
	for _, w := range p.Writes {
		var err error
		if w.Rel != "" {
			err = p.root.create(w.Rel, w.Content, w.Mode)
		} else {
			dirMode := os.FileMode(0o755)
			if w.Mode&0o077 == 0 {
				dirMode = 0o700
			}
			if err = os.MkdirAll(filepath.Dir(w.Path), dirMode); err == nil {
				err = createNew(w.Path, w.Content, w.Mode)
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				switch {
				case strings.HasSuffix(w.Path, ".md"):
					continue // a role prompt that appeared since the plan is the owner's; leave it
				case filepath.Base(w.Path) == proposedName:
					err = &Refusal{Code: CodeProposedExist, Fields: []string{proposedName}, Detail: "grants.yaml.proposed exists; migrate never overwrites it"}
				default:
					err = &Refusal{Code: CodeSquadExists, Fields: []string{w.Rel}, Detail: "the file appeared since the plan; migrate never overwrites it"}
				}
			}
			return done, err
		}
		done = append(done, w.Path)
	}
	return done, nil
}

// createNew writes data to path with mode, failing with os.ErrExist when path
// exists. It is for the one file outside the project, the proposal.
func createNew(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".migrate-"+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(name, path)
}

// RemovePlan is the v2 files `migrate --remove-v2` removes.
type RemovePlan struct {
	Project string
	Files   []string
	root    *root
}

// Close releases the project-root descriptor.
func (r *RemovePlan) Close() { r.root.Close() }

// BuildRemove lists the v2 files that are dead under decision 6 (the
// profile, launch.local.json and the agents' role.md files). It refuses with
// migrate_remove_needs_squad unless a squad.yaml that loads is present at
// the project: nothing is removed from a checkout that has no replacement.
// Every file was found through the root descriptor, so a symlink on the way
// to one is already migrate_refused_symlink here. Removing is the only act
// that deletes anything, and it needs the flag.
func BuildRemove(opts Options) (rp *RemovePlan, err error) {
	in, err := readInputs(opts.Profile)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			in.Root.Close()
		}
	}()
	top, err := in.Root.names("")
	if err != nil {
		return nil, err
	}
	if !contains(top, team.SquadFileName) {
		return nil, &Refusal{Code: CodeRemoveNoSquad, Fields: []string{team.SquadFileName}, Detail: "squad.yaml is absent; run `amq-squad migrate --write` first. Nothing is removed"}
	}
	// the bytes come through the root descriptor (a symlinked squad.yaml is a
	// refusal, not followed) and are parsed against the owner's grant, as
	// team.LoadSquadWith does
	raw, err := in.Root.read(team.SquadFileName)
	if err != nil {
		return nil, err
	}
	owner, err := grants.LoadFrom(ownerPath(opts), "")
	if err == nil && raw != nil {
		_, err = team.ParseSquad(string(raw.Data), team.SquadPath(in.Project), owner.Grants)
	}
	if err != nil || raw == nil {
		return nil, &Refusal{Code: CodeRemoveNoSquad, Fields: []string{team.SquadFileName}, Detail: fmt.Sprintf("squad.yaml does not load, so it cannot replace the v2 files: %v. Nothing is removed", err)}
	}
	rp = &RemovePlan{Project: in.Project, Files: []string{in.ProfilePath}, root: in.Root}
	if in.LaunchLocal != nil {
		rp.Files = append(rp.Files, in.LaunchLocal.Path)
	}
	for _, r := range in.AgentRoleMDs {
		rp.Files = append(rp.Files, r.Path)
	}
	sort.Strings(rp.Files)
	rp.Files = dedupe(rp.Files)
	return rp, nil
}

// Apply removes the files through the root descriptor, one O_NOFOLLOW walk per
// file. A symlink introduced after the plan, or a root that moved, stops at
// that file: Apply returns the files removed so far and the refusal, and
// removes nothing more. There is no transaction; the caller reports the
// partial result.
func (r *RemovePlan) Apply() ([]string, error) {
	var removed []string
	for _, f := range r.Files {
		rel, ok := r.root.rel(f)
		if !ok {
			return removed, &Refusal{Code: CodeSymlink, Fields: []string{"a removal path"}, Detail: "a path outside the project root"}
		}
		if err := r.root.remove(rel); err != nil {
			return removed, err
		}
		removed = append(removed, f)
	}
	return removed, nil
}
