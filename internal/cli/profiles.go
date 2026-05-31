package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// profileRow is one entry inside the team profiles list. Reused by both
// the human tabwriter output and the team_profiles JSON envelope so the
// two views can never drift.
type profileRow struct {
	Profile    string `json:"profile"`
	Path       string `json:"path"`
	Members    int    `json:"members"`
	Workstream string `json:"workstream,omitempty"`
}

// teamProfilesEnvelopeData is the kind="team_profiles" payload.
type teamProfilesEnvelopeData struct {
	Profiles []profileRow `json:"profiles"`
}

// resolveProfileFlag normalizes a --profile value: empty or "default" maps
// to the implicit default profile; non-default names are validated against
// the slug rules. Returns the canonical profile name and any error.
func resolveProfileFlag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == team.DefaultProfile {
		return team.DefaultProfile, nil
	}
	if err := team.ValidateProfileName(name); err != nil {
		return "", fmt.Errorf("--profile: %w", err)
	}
	return name, nil
}

func runTeamProfiles(args []string) error {
	fs := flag.NewFlagSet("team profiles", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned team_profiles envelope instead of the human table")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team profiles - list configured team profiles

Usage:
  amq-squad team profiles [--json]

Default first, then named profiles sorted alphabetically. Columns: PROFILE,
PATH, MEMBERS, WORKSTREAM. Read-only; no create, delete, or rename here.
Use 'amq-squad team init --profile NAME' to add a profile.

Examples:
  amq-squad team profiles
  amq-squad team profiles --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	var rows []profileRow
	if team.Exists(cwd) {
		t, err := team.Read(cwd)
		if err != nil {
			// Mirror the named-profile path: warn on stderr so the broken
			// config is visible while stdout (especially under --json) stays
			// a valid envelope of whatever was readable.
			fmt.Fprintf(os.Stderr, "warning: read profile %s: %v\n", team.DefaultProfile, err)
		} else {
			rows = append(rows, profileRow{
				Profile:    team.DefaultProfile,
				Path:       team.Path(cwd),
				Members:    len(t.Members),
				Workstream: t.Workstream,
			})
		}
	}
	named, err := team.ListProfiles(cwd)
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}
	for _, name := range named {
		t, err := team.ReadProfile(cwd, name)
		if err != nil {
			// Skip unreadable profile but warn so the user sees the
			// breakage. JSON mode still emits a valid envelope on stdout;
			// warnings only land on stderr.
			fmt.Fprintf(os.Stderr, "warning: read profile %s: %v\n", name, err)
			continue
		}
		rows = append(rows, profileRow{
			Profile:    name,
			Path:       team.ProfilePath(cwd, name),
			Members:    len(t.Members),
			Workstream: t.Workstream,
		})
	}
	if *jsonOut {
		return printJSONEnvelope("team_profiles", teamProfilesEnvelopeData{Profiles: rows})
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "No team profiles configured. Run 'amq-squad team init' to create one.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROFILE\tPATH\tMEMBERS\tWORKSTREAM")
	for _, r := range rows {
		ws := r.Workstream
		if ws == "" {
			ws = "(default)"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", r.Profile, r.Path, r.Members, ws)
	}
	return w.Flush()
}
