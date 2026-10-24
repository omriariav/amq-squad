package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Review item 4: the build rebuilds the npm tree from the integrity-checked
// cache on every run (npm ci --offline into a removed directory) before it
// stages anything, so a node_modules edited after fetch is never embedded.
func TestBuildRebuildsNpmTreeBeforeStaging(t *testing.T) {
	raw, err := os.ReadFile("../build-pi-runtime.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, "\nbuild() {")
	if start < 0 {
		t.Fatal("no build function")
	}
	body := s[start:]
	end := strings.Index(body, "\n}\n")
	body = body[:end]
	rm := strings.Index(body, `/bin/rm -rf "$work/npm"`)
	ci := regexp.MustCompile(`npm ci --prefix "\$work/npm" --offline`).FindStringIndex(body)
	stage := strings.Index(body, `cp -R "$work/npm/node_modules" "$stage/pi/node_modules"`)
	if rm < 0 || ci == nil || stage < 0 {
		t.Fatalf("build must remove the npm tree, run npm ci --offline and then stage it (rm %d, ci %v, stage %d)", rm, ci, stage)
	}
	if !(rm < ci[0] && ci[0] < stage) {
		t.Fatalf("order: rm %d, npm ci %d, stage %d", rm, ci[0], stage)
	}
	if strings.Contains(body, "cmp -s pi/package-lock.json") {
		t.Fatal("build still trusts an earlier fetch's tree when the lockfile matches")
	}
}
