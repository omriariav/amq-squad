package seatcommit

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeTreeObjects struct {
	entries []treeObjectEntry
	blobs   map[string][]byte
}

func (f fakeTreeObjects) TreeEntries(string) ([]treeObjectEntry, error) {
	return f.entries, nil
}

func (f fakeTreeObjects) Blob(oid string) ([]byte, error) {
	return f.blobs[oid], nil
}

func fakeTree(links map[string]string, files ...string) fakeTreeObjects {
	f := fakeTreeObjects{blobs: map[string][]byte{}}
	for _, name := range files {
		f.entries = append(f.entries, treeObjectEntry{Mode: "100644", OID: "file-" + name, Path: name})
	}
	for name, target := range links {
		oid := "link-" + name
		f.entries = append(f.entries, treeObjectEntry{Mode: "120000", OID: oid, Path: name})
		f.blobs[oid] = []byte(target)
	}
	return f
}

func TestTreeLinkResolverUsesOnlyTreeObjects(t *testing.T) {
	cases := []struct {
		name, alias, want, refuse string
		links                     map[string]string
		files                     []string
	}{
		{name: "ordinary", links: map[string]string{"link": "cmd/root.go"}, files: []string{"cmd/root.go"}, alias: "link", want: "cmd/root.go"},
		{name: "absolute", links: map[string]string{"link": "/etc/passwd"}, alias: "link", refuse: "absolute"},
		{name: "outside", links: map[string]string{"dir/link": "../../outside"}, alias: "dir/link", refuse: "leaves the repository"},
		{name: "unclean dangling", links: map[string]string{"link": "missing/../cmd/root.go"}, files: []string{"cmd/root.go"}, alias: "link", refuse: `dangling at "missing"`},
		{name: "cycle", links: map[string]string{"a": "b", "b": "a"}, alias: "a", refuse: "cycle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := readTreeLinkSnapshot(fakeTree(tc.links, tc.files...), "tree")
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.resolve(tc.alias)
			if tc.refuse != "" {
				if !errors.Is(err, errLinkTargetRefused) || !strings.Contains(err.Error(), tc.refuse) || !strings.Contains(err.Error(), tc.alias) {
					t.Fatalf("resolve = %q, %v", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("resolve = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestTreeLinkResolverDepthLimit(t *testing.T) {
	links := map[string]string{}
	for i := 0; i <= treeLinkMaxDepth; i++ {
		links[fmt.Sprintf("l%d", i)] = fmt.Sprintf("l%d", i+1)
	}
	links[fmt.Sprintf("l%d", treeLinkMaxDepth+1)] = "target"
	s, err := readTreeLinkSnapshot(fakeTree(links, "target"), "tree")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolve("l0"); !errors.Is(err, errLinkTargetRefused) || !strings.Contains(err.Error(), "depth limit 40") {
		t.Fatalf("depth refusal = %v", err)
	}
}

func TestTreeLinkPolicyChecksOnlyChangedLinks(t *testing.T) {
	source := fakeTree(map[string]string{"old": "missing", "new": "CLAUDE.md"}, "CLAUDE.md")
	entries := []rawEntry{
		{OldMode: "120000", NewMode: "120000", OldOID: "same", NewOID: "same", Path: "old"},
		{OldMode: "000000", NewMode: "120000", OldOID: "zero", NewOID: "new", Path: "new"},
	}
	refused, err := treeLinkRefusals(source, "tree", entries, protectedSet{managed: []string{"CLAUDE.md"}})
	if err != nil || len(refused) != 1 || !strings.Contains(refused[0], `link "new"`) || strings.Contains(refused[0], `link "old"`) {
		t.Fatalf("refusals = %q, %v", refused, err)
	}
	if refused, err := treeLinkRefusals(source, "tree", entries[:1], protectedSet{}); err != nil || len(refused) != 0 {
		t.Fatalf("unchanged dangling link was judged: %q, %v", refused, err)
	}
}
