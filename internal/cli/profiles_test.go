package cli

import (
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// seedProfile writes a profile config into projectDir without changing the
// process cwd. Returns the resolved profile path so the caller can assert
// on placement (default profile lives at .amq-squad/team.json; named
// profiles under .amq-squad/teams/<name>.json).
func seedProfile(t *testing.T, projectDir, profile string, cfg team.Team) string {
	t.Helper()
	if err := team.Write(projectDir, cfg); err != nil {
		t.Fatal(err)
	}
	return team.SquadPath(projectDir)
}

func TestResolveProfileFlag(t *testing.T) {
	cases := map[string]struct {
		in      string
		want    string
		wantErr bool
	}{
		"empty":   {in: "", want: squadnamespace.DefaultProfile},
		"default": {in: "default", want: squadnamespace.DefaultProfile},
		"named":   {in: "review", want: "review"},
		"bad":     {in: "Bad/Name", wantErr: true},
	}
	for name, tc := range cases {
		got, err := resolveProfileFlag(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got %q", name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected err %v", name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// TestResumeFooterCarriesProfile is deleted (gh#758/t11): its whole premise
// -- resume's plan-only output carries a resume-specific footer suggesting a
// profile-aware follow-up command -- no longer holds. The plan-only path now
// renders plan's own generic PrepareResultV1 output verbatim (no per-command
// follow-up suggestion at all, matching plan.go, which has never had one),
// per the approved "thin alias to plan" design (task/t11).
