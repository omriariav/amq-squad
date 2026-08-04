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

// v228PermittedVocabularyFiles are excluded by EXACT BASENAME, never by prefix.
//
// Each one uses the matched words for observations of EXTERNAL systems, which the
// governing rule explicitly permits — persisting observations of external systems
// is allowed; only a second owned representation certifying another owned
// representation is not. Renaming these to satisfy a grep would be ceremony in
// reverse. The permitted uses, as ruled:
//
//	doctor.go    describePointerSyncDrift, "worktree-plan-drift", roster-drift and
//	             completion-evidence-drift wording — git worktree and pointer-sync
//	             state, i.e. external reality.
//	status.go    status.Drifted / "drifted" worktree state, plus goal-contract
//	             prose in an operator-facing detail string.
//	bootstrap.go one comment about a member announcing readiness to its lead.
//
// Exact basename matters: dropping the "bootstrap"/"status"/"doctor" PREFIXES
// instead would also hide bootstrap_drift_evidence_598.go, bootstrap_launch_
// evidence_598.go, status_board.go and doctor_*.go, which are genuine deletion
// targets. The narrowing must not silently widen as files are added.
var v228PermittedVocabularyFiles = map[string]bool{
	"doctor.go":    true,
	"status.go":    true,
	"bootstrap.go": true,
}

func v228IsLaunchPathSource(name string) bool {
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	if v228PermittedVocabularyFiles[name] {
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
	filesWithHits := map[string]bool{}
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
				filesWithHits[entry.Name()] = true
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
		// Report the count of files that CONTAIN hits, separately from how many
		// were scanned: conflating them reads as "spread across every scanned
		// file" and overstates the remaining surface.
		t.Fatalf("%d launch-path hit(s) for the deleted ceremony vocabulary in %d of %d scanned file(s):\n%s",
			len(hits), len(filesWithHits), scanned, strings.Join(hits, "\n"))
	}
}
