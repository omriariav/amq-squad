package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

// doctorReleaseDrift is doctor's tag and release check (3-2, ruling 12): for
// every repository with a merge grant, the repository's tags and releases
// are compared against the session's gate store (mergetrain.Drift). One with
// no accepted owner gate is written as an owner alert (status shows it under
// needs_you) and fails the check. The first run on a repository records its
// existing tags and releases as the baseline.
func doctorReleaseDrift(root string, g grants.Grants, lister mergetrain.ReleaseLister, now time.Time) []doctorCheck {
	if root == "" || len(g.Merge) == 0 {
		return nil
	}
	gates, err := gatestore.Open(root)
	if err != nil {
		return []doctorCheck{{Name: "release drift", Status: doctorWarn, Detail: err.Error()}}
	}
	var out []doctorCheck
	seen := map[string]bool{}
	for _, m := range g.Merge {
		repo := strings.ToLower(m.Repository)
		if seen[repo] {
			continue
		}
		seen[repo] = true
		check := doctorCheck{Name: "release drift " + m.Repository, Status: doctorOK, Detail: "every tag and release since the baseline has an accepted owner gate"}
		alerts, err := mergetrain.Drift(context.Background(), lister, gates, root, m.Repository, now)
		switch {
		case err != nil:
			check.Status, check.Detail = doctorWarn, "could not compare tags and releases: "+err.Error()
		case len(alerts) > 0:
			var names []string
			for _, a := range alerts {
				names = append(names, a.Kind+" "+a.Name)
			}
			check.Status, check.Detail = doctorFail, fmt.Sprintf("%d without an accepted owner gate (owner alert raised): %s", len(alerts), strings.Join(names, ", "))
		}
		out = append(out, check)
	}
	return out
}

// doctorReleaseDriftHook wires the check to the owner's grants and gh; nil
// when there is no trusted owner grants file.
func doctorReleaseDriftHook(root string) func() []doctorCheck {
	owner, err := grants.Load("")
	if err != nil || owner.Insecure {
		return nil
	}
	return func() []doctorCheck { return doctorReleaseDrift(root, owner.Grants, &mergetrain.GH{}, time.Now()) }
}
