package seatcommit

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const treeLinkMaxDepth = 40

var errLinkTargetRefused = errors.New(ReasonLinkTarget)

type treeObjectEntry struct{ Mode, OID, Path string }

type treeObjectSource interface {
	TreeEntries(tree string) ([]treeObjectEntry, error)
	Blob(oid string) ([]byte, error)
}

type gitTreeObjectSource struct{ g git }

func (s gitTreeObjectSource) TreeEntries(tree string) ([]treeObjectEntry, error) {
	raw, err := s.g.run(nil, nil, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, err
	}
	var out []treeObjectEntry
	for _, rec := range strings.Split(string(raw), "\x00") {
		if rec == "" {
			continue
		}
		meta, name, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		out = append(out, treeObjectEntry{Mode: fields[0], OID: fields[2], Path: name})
	}
	return out, nil
}

func (s gitTreeObjectSource) Blob(oid string) ([]byte, error) {
	return s.g.run(nil, nil, "cat-file", "blob", oid)
}

type treeLinkSnapshot struct {
	entries map[string]treeObjectEntry
	links   map[string]string
}

func readTreeLinkSnapshot(source treeObjectSource, tree string) (*treeLinkSnapshot, error) {
	listed, err := source.TreeEntries(tree)
	if err != nil {
		return nil, err
	}
	s := &treeLinkSnapshot{entries: map[string]treeObjectEntry{}, links: map[string]string{}}
	for _, e := range listed {
		s.entries[e.Path] = e
		for parent := path.Dir(e.Path); parent != "."; parent = path.Dir(parent) {
			if _, ok := s.entries[parent]; !ok {
				s.entries[parent] = treeObjectEntry{Mode: "040000", Path: parent}
			}
		}
		if e.Mode == "120000" {
			body, err := source.Blob(e.OID)
			if err != nil {
				return nil, fmt.Errorf("read link blob %s for %q: %w", e.OID, e.Path, err)
			}
			s.links[e.Path] = string(body)
		}
	}
	return s, nil
}

// resolve walks components without cleaning first: missing/../x is dangling.
func (s *treeLinkSnapshot) resolve(alias string) (string, error) {
	target, ok := s.links[alias]
	if !ok {
		return "", fmt.Errorf("%w: link %q has no 120000 entry", errLinkTargetRefused, alias)
	}
	if path.IsAbs(target) {
		return "", fmt.Errorf("%w: link %q target %q is absolute", errLinkTargetRefused, alias, target)
	}
	var current []string
	if dir := path.Dir(alias); dir != "." {
		current = strings.Split(dir, "/")
	}
	remaining := strings.Split(target, "/")
	seen := map[string]bool{alias: true}
	followed := 0
	for len(remaining) > 0 {
		part := remaining[0]
		remaining = remaining[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(current) == 0 {
				return "", fmt.Errorf("%w: link %q target %q leaves the repository", errLinkTargetRefused, alias, target)
			}
			current = current[:len(current)-1]
			continue
		}
		candidate := strings.Join(append(append([]string(nil), current...), part), "/")
		e, exists := s.entries[candidate]
		if !exists {
			return "", fmt.Errorf("%w: link %q target %q is dangling at %q", errLinkTargetRefused, alias, target, candidate)
		}
		if e.Mode == "120000" {
			followed++
			if followed > treeLinkMaxDepth {
				return "", fmt.Errorf("%w: link %q target %q exceeds the depth limit %d", errLinkTargetRefused, alias, target, treeLinkMaxDepth)
			}
			if seen[candidate] {
				return "", fmt.Errorf("%w: link %q target %q has a cycle at %q", errLinkTargetRefused, alias, target, candidate)
			}
			seen[candidate] = true
			next := s.links[candidate]
			if path.IsAbs(next) {
				return "", fmt.Errorf("%w: link %q target %q reaches absolute target %q", errLinkTargetRefused, alias, target, next)
			}
			remaining = append(strings.Split(next, "/"), remaining...)
			continue
		}
		current = append(current, part)
		if len(remaining) > 0 && e.Mode != "040000" {
			return "", fmt.Errorf("%w: link %q target %q is dangling below %q", errLinkTargetRefused, alias, target, candidate)
		}
	}
	return strings.Join(current, "/"), nil
}

func changedLinkPaths(entries []rawEntry) []string {
	var paths []string
	for _, e := range entries {
		if e.NewMode == "120000" && e.NewOID != e.OldOID {
			paths = append(paths, e.Path)
		}
	}
	return paths
}

func treeLinkRefusals(source treeObjectSource, tree string, entries []rawEntry, protected protectedSet) ([]string, error) {
	paths := changedLinkPaths(entries)
	if len(paths) == 0 {
		return nil, nil
	}
	snapshot, err := readTreeLinkSnapshot(source, tree)
	if err != nil {
		return nil, err
	}
	var refused []string
	for _, alias := range paths {
		// A protected link path keeps the established protected_path reason.
		if _, hit := protected.match(alias); hit || protected.managedName(alias) != "" {
			continue
		}
		destination, err := snapshot.resolve(alias)
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		if _, hit := protected.match(destination); hit || protected.managedName(destination) != "" {
			refused = append(refused, fmt.Sprintf("%s: link %q target %q resolves to protected destination %q", ReasonLinkTarget, alias, snapshot.links[alias], destination))
		}
	}
	return refused, nil
}
