package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// The squad.yaml golden self-check (final form A2): ok on the embedded
// fixture and in a doctor run; a drift of the fixture or of the expected
// structure fails with squad_yaml_golden_drift.
func TestDoctorSquadYamlGoldenSelfCheck(t *testing.T) {
	if c := squadGoldenCheck(team.GoldenYAML, team.GoldenExpected); c.Status != doctorOK {
		t.Fatalf("embedded fixture: %+v", c)
	}
	drifted := strings.Replace(string(team.GoldenExpected), `"MaxSeats": 6`, `"MaxSeats": 7`, 1)
	c := squadGoldenCheck(team.GoldenYAML, []byte(drifted))
	if c.Status != doctorFail || c.Kind != "squad_yaml_golden_drift" || !strings.Contains(c.Detail, "squad_yaml_golden_drift") {
		t.Fatalf("a drifted expected structure passed: %+v", c)
	}
	changed := strings.Replace(string(team.GoldenYAML), "max_seats: 6", "max_seats: 5", 1)
	if c := squadGoldenCheck([]byte(changed), team.GoldenExpected); c.Status != doctorFail || c.Kind != "squad_yaml_golden_drift" {
		t.Fatalf("a drifted fixture passed: %+v", c)
	}
	data, _ := runHostDoctor(t, hostWith(t, t.TempDir(), "amq"), t.TempDir())
	if c := findCheck(data.Checks, "squad.yaml golden"); c == nil || c.Status != doctorOK {
		t.Fatalf("doctor row %+v", c)
	}
}
