package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// AC10: `grep -r "sha256\|drift\|generation token\|readiness" internal/cli` must
// have no launch-path hits once the ceremony layer is deleted. This test pins the
// deletion outcome so the machinery cannot come back in one file at a time.
//
// It is deliberately scoped to the launch path rather than the whole package:
// other areas (release evidence, task message digests) legitimately compute
// digests over EXTERNAL inputs, which the governing rule allows. The rule this
// pins is the other one — no owned representation whose sole purpose is to
// certify another owned representation.
var v228DeletedVocabulary = regexp.MustCompile(`(?i)sha256|drift|generation token|readiness`)

// v228LaunchPathSourcePrefixes selects the launch-path sources of internal/cli.
var v228LaunchPathSourcePrefixes = []string{
	"bootstrap",
	"doctor",
	"down",
	"identity_drift",
	"launch",
	"preflight",
	"prepared",
	"run_start",
	"status",
	"team_launch",
	"team_member_staged_launch",
	"up",
}

func v228IsLaunchPathSource(name string) bool {
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	for _, prefix := range v228LaunchPathSourcePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func TestV228ContractLaunchPathHasNoCeremonyVocabulary(t *testing.T) {
	requireV228Contract(t)
	// go test runs in the package directory, so this is internal/cli itself.
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatal(err)
	}

	var hits []string
	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() || !v228IsLaunchPathSource(entry.Name()) {
			continue
		}
		scanned++
		f, err := os.Open(filepath.Join(pkgDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			text := scanner.Text()
			if match := v228DeletedVocabulary.FindString(text); match != "" {
				hits = append(hits, entry.Name()+":"+strconv.Itoa(line)+": "+match+" | "+strings.TrimSpace(text))
			}
		}
		scanErr := scanner.Err()
		f.Close()
		if scanErr != nil {
			t.Fatal(scanErr)
		}
	}
	if scanned == 0 {
		t.Fatal("no launch-path sources matched; the file-name prefixes need updating")
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Fatalf("%d launch-path hit(s) for the deleted ceremony vocabulary across %d file(s):\n%s",
			len(hits), scanned, strings.Join(hits, "\n"))
	}
}
