package cli

import (
	"flag"
	"strings"
	"testing"
)

// #538 / Finding B: `new profile NAME` peels the profile name from the flag args
// using a hand-maintained list of which team-init flags take a SEPARATE value.
// A value-taking flag missing from that list is treated as valueless, its value
// falls through as a positional, and the operator gets
//
//	new profile takes exactly one profile name; got extra argument "cto=review"
//
// which blames their input for a gap in our map. --actor-mode, --tool-profile and
// --tool-config were all missing. --actor-mode is how a lead is marked `review`
// instead of `implementation`, so its absence silently produced three
// mutation-capable members where the operator asked for two, and then a
// worktree_isolation blocker with no visible cause.
//
// This test enumerates the REAL team-init flag set and fails when a value-taking
// flag is not forwarded, so the list cannot drift again as flags are added.
func TestNewProfileForwardsEveryValueTakingTeamInitFlag(t *testing.T) {
	missing := []string{}
	teamInitFlagSet(t).VisitAll(func(f *flag.Flag) {
		if !flagTakesSeparateValue(f) {
			return
		}
		if newProfileRejectedByDesign[f.Name] {
			return
		}
		if !newProfileValueFlags["--"+f.Name] {
			missing = append(missing, "--"+f.Name)
		}
	})
	if len(missing) > 0 {
		t.Fatalf("team init flags take a separate value but are not in newProfileValueFlags, so `new profile` will misparse them as the profile name: %s",
			strings.Join(missing, ", "))
	}
}

// newProfileRejectedByDesign names team-init flags that `new profile` refuses on
// purpose rather than forwards. --profile is set from the positional NAME, so
// passing it is an explicit usage error, not a forwarding gap.
var newProfileRejectedByDesign = map[string]bool{"profile": true}

// The complement: a stale entry is also a bug. A boolean flag listed as
// value-taking would swallow the NEXT argument, which for `new profile` is often
// the profile name itself.
func TestNewProfileValueFlagsHasNoStaleEntries(t *testing.T) {
	real := map[string]bool{}
	teamInitFlagSet(t).VisitAll(func(f *flag.Flag) {
		if flagTakesSeparateValue(f) {
			real["--"+f.Name] = true
		}
	})
	// Flags peeled by new profile itself before delegation, so they legitimately
	// appear in the map without being team-init flags.
	peeledByNewProfile := map[string]bool{"--project": true, "--profile": true}
	for name := range newProfileValueFlags {
		if real[name] || peeledByNewProfile[name] {
			continue
		}
		t.Fatalf("newProfileValueFlags lists %s, but team init has no such value-taking flag; a stale entry swallows the following argument (often the profile name)", name)
	}
}

// The end-to-end shape of the reported bug: --actor-mode must survive the peel
// with its value intact, and the profile name must still be extracted.
func TestNewProfilePeelsNameAndForwardsActorMode(t *testing.T) {
	args := []string{"testsquad", "--roles", "cto,dev-1", "--actor-mode", "cto=review", "--cwd", "dev-1=/tmp/wt-a"}
	out, err := newProfileTeamArgs(args)
	if err != nil {
		t.Fatalf("newProfileTeamArgs: %v", err)
	}
	joined := strings.Join(out, " ")
	if !strings.Contains(joined, "--profile testsquad") {
		t.Fatalf("profile name was not forwarded as --profile: %v", out)
	}
	for _, want := range []string{"--actor-mode cto=review", "--cwd dev-1=/tmp/wt-a", "--roles cto,dev-1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("forwarded args are missing %q: %v", want, out)
		}
	}
}

// teamInitFlagSet builds the real `team init` flag set by parsing an empty arg
// list through the same registration path the command uses, so the enumeration
// above is against production flags rather than a second hand-written list.
func teamInitFlagSet(t *testing.T) *flag.FlagSet {
	t.Helper()
	fs, err := newTeamInitFlagSetForTest()
	if err != nil {
		t.Fatalf("build team init flag set: %v", err)
	}
	return fs
}

// flagTakesSeparateValue reports whether a flag consumes the following argument.
// Go's flag package folds booleans into `-name` form, so only non-boolean flags
// take a separate value.
func flagTakesSeparateValue(f *flag.Flag) bool {
	bv, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !(ok && bv.IsBoolFlag())
}
