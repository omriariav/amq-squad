package cli

import (
	"context"
	"errors"
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
		case errors.Is(err, mergetrain.ErrBaselineInvalid):
			check.Status, check.Detail = doctorFail, err.Error()
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
	return func() []doctorCheck { return doctorReleaseDrift(root, owner.Grants, doctorReleaseLister(), time.Now()) }
}

// doctorReleaseLister is the drift check's tag and release source, gh (a
// seam: the seat rows count its calls and never reach GitHub).
var doctorReleaseLister = func() mergetrain.ReleaseLister { return &mergetrain.GH{} }

// doctorReleaseDriftFor is doctor's drift hook. Inside a seat (seatEnvMarker)
// it is not installed: the check calls GitHub and mergetrain.Drift writes
// baselines, coverage bindings and owner alerts under
// <AM_ROOT>/extensions/amq-squad/alerts, squad state the owner session owns
// (brief 3.0.1-t15 fix round). The seat sees one row saying the check was
// skipped and that the owner runs it; the owner's grants are not even read.
func doctorReleaseDriftFor(getenv func(string) string, root string) func() []doctorCheck {
	marker := seatEnvMarker(getenv)
	if marker == "" {
		return doctorReleaseDriftHook(root)
	}
	return func() []doctorCheck {
		return []doctorCheck{{Name: "release drift", Status: doctorOK,
			Detail: fmt.Sprintf("skipped: %s is set, so this process is a seat; the tag and release drift check calls GitHub and writes owner alerts under the session root, so the owner runs it (amq-squad doctor from their own terminal)", marker)}}
	}
}
