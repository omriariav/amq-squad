// Command redact-evidence is the redaction step every evidence file passes
// through before it is committed (briefs ruling 18, item 2): it rewrites
// each file, or every file under each directory, in place with
// internal/redact's Text, the same rule the repository scan
// (internal/redact's TestNoCredentialShapedValueUnderDocsOrEvidence) rejects.
// It prints how many values it replaced per file, never a value.
//
//	go run ./scripts/redact-evidence [-check] PATH...
//
// -check writes nothing and exits 1 when a credential-shaped value is left.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/omriariav/amq-squad/v3/internal/redact"
)

func main() {
	check := flag.Bool("check", false, "report only; exit 1 when a value is found")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: redact-evidence [-check] PATH...")
		os.Exit(2)
	}
	found := 0
	for _, p := range flag.Args() {
		n, err := run(p, *check)
		if err != nil {
			fmt.Fprintln(os.Stderr, "redact-evidence:", err)
			os.Exit(2)
		}
		found += n
	}
	if *check && found > 0 {
		os.Exit(1)
	}
}

func run(root string, check bool) (int, error) {
	if !check {
		counts, err := redact.Tree(root)
		paths := make([]string, 0, len(counts))
		for p := range counts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		total := 0
		for _, p := range paths {
			fmt.Printf("%s: %d value(s) redacted\n", p, counts[p])
			total += counts[p]
		}
		return total, err
	}
	total := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.IndexByte(raw, 0) >= 0 {
			return nil // binary: the redaction leaves it as it is
		}
		if n := redact.Count(string(raw)); n > 0 {
			fmt.Printf("%s: %d credential-shaped value(s)\n", p, n)
			total += n
		}
		return nil
	})
	return total, err
}
