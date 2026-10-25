package seatcommit

import (
	"fmt"
	"os"
	"strings"
)

// EvidencePrefix is where every evidence ref lives in a seat repository.
const EvidencePrefix = "refs/amq-squad/evidence/"

// EvidenceRefs are the evidence refs the ledger records: the discarded
// trees', the open quarantine's and the cleared quarantines' (the ledger
// keeps the record after a retention expiry has dropped the ref, with "").
func (l Ledger) EvidenceRefs() []string {
	var out []string
	add := func(r string) {
		if r != "" {
			out = append(out, r)
		}
	}
	for _, d := range l.Discarded {
		add(d.Ref)
	}
	if l.Quarantine != nil {
		add(l.Quarantine.Ref)
	}
	for _, q := range l.Cleared {
		add(q.Ref)
	}
	return out
}

// DeleteEvidenceRefs deletes the named evidence refs (uninstall --purge)
// from the repository of the first of checkouts that resolves, through the
// hardened git environment of the harness (the seat repository's config,
// hooks and filter drivers are never read). Only refs under EvidencePrefix
// are accepted; nothing else in the repository is touched: no branch, no
// object. A ref that is already gone is not an error.
func DeleteEvidenceRefs(gitBin string, checkouts, refs []string) error {
	for _, r := range refs {
		if !strings.HasPrefix(r, EvidencePrefix) || strings.ContainsAny(r, " \t\n\\~^:?*[") || strings.Contains(r, "..") {
			return fmt.Errorf("%q is not an evidence ref", r)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	var rp repoPaths
	var last error
	found := false
	for _, c := range checkouts {
		if c == "" {
			continue
		}
		var err error
		if rp, err = worktreePaths(c); err == nil {
			found = true
			break
		} else {
			last = err
		}
	}
	if !found {
		if last == nil {
			last = fmt.Errorf("no checkout of the repository is known")
		}
		return fmt.Errorf("the seat repository cannot be reached: %w", last)
	}
	g := seatRepo(gitBin, harnessEnv(os.Environ()), rp)
	for _, r := range refs {
		if _, err := g.run(nil, nil, "update-ref", "-d", r); err != nil {
			return err
		}
	}
	return nil
}
