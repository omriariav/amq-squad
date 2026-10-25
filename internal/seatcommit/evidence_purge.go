package seatcommit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EvidencePrefix is where every evidence ref lives in a seat repository.
const EvidencePrefix = "refs/amq-squad/evidence/"

// ReasonEvidenceRefSymbolic: an evidence ref is a symbolic ref (a seat can
// plant one, naming a branch); nothing is deleted in that repository.
const ReasonEvidenceRefSymbolic = "evidence_ref_symbolic"

// EvidenceRefInfo is one ref git itself lists under EvidencePrefix.
type EvidenceRefInfo struct {
	Name   string
	OID    string
	Symref string
}

// reach resolves the first checkout that is a git checkout, and returns the
// seat repository's git for its own refs under the hardened environment.
func reach(gitBin string, checkouts []string) (git, repoPaths, error) {
	var last error
	for _, c := range checkouts {
		if c == "" {
			continue
		}
		rp, err := worktreePaths(c)
		if err == nil {
			return seatRepo(gitBin, harnessEnv(os.Environ()), rp), rp, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("no checkout of the repository is known")
	}
	return git{}, repoPaths{}, fmt.Errorf("the seat repository cannot be reached: %w", last)
}

// ListEvidenceRefs enumerates the evidence refs from git itself (never from
// a ledger) in the repository of the first checkout that resolves, and
// returns that repository's common dir. It only reads.
func ListEvidenceRefs(gitBin string, checkouts []string) (string, []EvidenceRefInfo, error) {
	g, rp, err := reach(gitBin, checkouts)
	if err != nil {
		return "", nil, err
	}
	out, err := g.run(nil, nil, "for-each-ref", "--format=%(refname) %(objectname) %(symref)", "refs/amq-squad/evidence/")
	if err != nil {
		return rp.Common, nil, err
	}
	var refs []EvidenceRefInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		r := EvidenceRefInfo{Name: f[0], OID: f[1]}
		if len(f) > 2 {
			r.Symref = f[2]
		}
		refs = append(refs, r)
	}
	return rp.Common, refs, nil
}

// DeleteEvidenceRefs deletes the listed refs (uninstall --purge) from the
// repository of the first checkout that resolves, through the hardened git
// environment of the harness (the seat repository's config, hooks and
// filter drivers are never read). A symbolic ref in the list refuses the
// whole call before anything is deleted (evidence_ref_symbolic). Each
// deletion is `update-ref --no-deref -d <ref> <oid>`: a ref that was
// replaced since it was listed is left, and no branch or object is touched.
func DeleteEvidenceRefs(gitBin string, checkouts []string, refs []EvidenceRefInfo) error {
	for _, r := range refs {
		if !strings.HasPrefix(r.Name, EvidencePrefix) || strings.ContainsAny(r.Name, " \t\n\\~^:?*[") || strings.Contains(r.Name, "..") {
			return fmt.Errorf("%q is not an evidence ref", r.Name)
		}
		if r.Symref != "" {
			return fmt.Errorf("%s: %s is a symbolic ref to %s; nothing was deleted in this repository", ReasonEvidenceRefSymbolic, r.Name, r.Symref)
		}
		if r.OID == "" {
			return fmt.Errorf("%s: no object id was listed", r.Name)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	g, _, err := reach(gitBin, checkouts)
	if err != nil {
		return err
	}
	var failed []string
	for _, r := range refs {
		if _, err := g.run(nil, nil, "update-ref", "--no-deref", "-d", r.Name, r.OID); err != nil {
			failed = append(failed, r.Name+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d evidence refs were not deleted (replaced since they were listed, or git failed): %s", len(failed), strings.Join(failed, "; "))
	}
	return nil
}

// VerifyWorktree reports why wt is not a worktree of the project checkout
// (nil when it is): `git worktree list --porcelain` of the project, under the
// hardened environment, must name it, and its .git file must resolve back
// into the project's own .git/worktrees.
func VerifyWorktree(gitBin, project, wt string) error {
	g, prj, err := reach(gitBin, []string{project})
	if err != nil {
		return err
	}
	if filepath.Base(prj.Common) != ".git" || filepath.Dir(prj.Common) != project {
		return fmt.Errorf("%s is not the root of a checkout whose git dir is its own .git", project)
	}
	w, err := worktreePaths(wt)
	if err != nil {
		return err
	}
	if evalOr(w.Common) != evalOr(prj.Common) || evalOr(filepath.Dir(w.GitDir)) != evalOr(filepath.Join(prj.Common, "worktrees")) {
		return fmt.Errorf("the .git of %s does not resolve into %s/worktrees", wt, prj.Common)
	}
	out, err := g.run(nil, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok && evalOr(filepath.Clean(p)) == evalOr(wt) {
			return nil
		}
	}
	return fmt.Errorf("git worktree list of %s does not name %s", project, wt)
}

// EvidenceSeatSegment is the seat's path segment in its evidence ref names.
func EvidenceSeatSegment(seat string) string {
	seg, _, _ := strings.Cut(strings.TrimPrefix(evidenceRefName(seat, "t", 1), EvidencePrefix), "/")
	return seg
}
