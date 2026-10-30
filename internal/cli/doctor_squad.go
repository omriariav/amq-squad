package cli

import (
	"fmt"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// squadGoldenCheck is the squad.yaml self-check, the pattern of the status
// contract check: the embedded golden fixture must parse, in this build, to
// the embedded expected structure (schema 1 is frozen for 3.0). A drift is a
// failing row of kind squad_yaml_golden_drift. golden and expected are
// team.GoldenYAML and team.GoldenExpected; tests pass a changed copy.
func squadGoldenCheck(golden, expected []byte) doctorCheck {
	const name = "squad.yaml golden"
	if err := team.GoldenCheck(golden, expected); err != nil {
		return doctorCheck{Name: name, Status: doctorFail, Kind: team.CodeGoldenDrift, Detail: fmt.Sprintf("%s: %v", team.CodeGoldenDrift, err)}
	}
	return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("schema %d: the golden fixture parses to the expected structure", team.SquadSchema)}
}
