package cli

import (
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func validateCanonicalOperatorMode(raw string) (string, error) {
	mode := strings.TrimSpace(raw)
	if err := team.ValidateOperatorInteractionMode(mode); err != nil {
		return "", err
	}
	return mode, nil
}
