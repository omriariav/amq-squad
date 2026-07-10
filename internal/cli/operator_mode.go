package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func validateCanonicalOperatorMode(raw string) (string, error) {
	mode := strings.TrimSpace(raw)
	if err := team.ValidateOperatorInteractionMode(mode); err != nil {
		return "", err
	}
	if mode == team.OperatorInteractionSelfOperator {
		return "", fmt.Errorf("operator interaction mode %q is not available; delegated self-approval ships with #391 and no authorization behavior is enabled", mode)
	}
	return mode, nil
}
