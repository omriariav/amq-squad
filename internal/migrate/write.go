package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Apply writes the plan's files, each only when absent: the content goes to
// a temporary file beside the target (mode set explicitly, whatever the
// umask) and is hard-linked into place, which fails if the target exists, so
// an existing file is never replaced even by a racing writer. A failure
// removes what this call created. It returns the paths written.
func (p *Plan) Apply() ([]string, error) {
	var done []string
	fail := func(err error) ([]string, error) {
		for _, path := range done {
			os.Remove(path)
		}
		return nil, err
	}
	for _, w := range p.Writes {
		if filepath.Base(w.Path) == team.SquadFileName && filepath.Dir(w.Path) == p.Project {
			if _, err := os.Lstat(w.Path); err == nil {
				return fail(&Refusal{Code: CodeSquadExists, Fields: []string{team.SquadFileName}, Detail: "squad.yaml appeared since the plan; migrate never overwrites it"})
			}
		}
		dirMode := os.FileMode(0o755)
		if w.Mode&0o077 == 0 {
			dirMode = 0o700
		}
		if err := os.MkdirAll(filepath.Dir(w.Path), dirMode); err != nil {
			return fail(err)
		}
		if err := createNew(w.Path, w.Content, w.Mode); err != nil {
			if errors.Is(err, os.ErrExist) {
				code := CodeSquadExists
				if filepath.Base(w.Path) == proposedName {
					code = CodeProposedExist
				}
				if strings.HasSuffix(w.Path, ".md") {
					// a role prompt that appeared since the plan: leave it, it is the owner's
					continue
				}
				return fail(&Refusal{Code: code, Fields: []string{w.Path}, Detail: "the file exists; migrate never overwrites it"})
			}
			return fail(err)
		}
		done = append(done, w.Path)
	}
	return done, nil
}

// createNew writes data to path with mode, failing with os.ErrExist when path
// exists.
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
}

// BuildRemove lists the v2 files that are dead under decision 6 (the
// profile, launch.local.json and the agents' role.md files). It refuses with
// migrate_remove_needs_squad unless a squad.yaml that loads is present at
// the project: nothing is removed from a checkout that has no replacement.
// Removing is the only act that deletes anything, and it needs the flag.
func BuildRemove(opts Options) (*RemovePlan, error) {
	in, err := readInputs(opts.Profile)
	if err != nil {
		return nil, err
	}
	path := team.SquadPath(in.Project)
	if _, err := os.Lstat(path); err != nil {
		return nil, &Refusal{Code: CodeRemoveNoSquad, Fields: []string{team.SquadFileName}, Detail: "squad.yaml is absent; run `amq-squad migrate --write` first. Nothing is removed"}
	}
	if _, err := team.LoadSquadWith(ownerPath(opts), in.Project); err != nil {
		return nil, &Refusal{Code: CodeRemoveNoSquad, Fields: []string{team.SquadFileName}, Detail: fmt.Sprintf("squad.yaml does not load, so it cannot replace the v2 files: %v. Nothing is removed", err)}
	}
	rp := &RemovePlan{Project: in.Project, Files: []string{in.ProfilePath}}
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

// Apply removes the files; it returns the ones removed.
func (r *RemovePlan) Apply() ([]string, error) {
	var removed []string
	for _, f := range r.Files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed = append(removed, f)
	}
	return removed, nil
}
