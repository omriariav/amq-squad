package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func operatorMailboxOnlyError(action, target string) error {
	return usageErrorf("%s cannot target operator %q: operator is a non-runnable mailbox participant; use a gate/<topic> thread or operator directive/reply instead", action, target)
}

func isOperatorTarget(t team.Team, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.EqualFold(target, team.DefaultOperatorHandle) {
		return true
	}
	op := team.EffectiveOperator(t)
	if !op.Enabled {
		return false
	}
	handle := strings.TrimSpace(op.Handle)
	if handle == "" {
		handle = team.DefaultOperatorHandle
	}
	return strings.EqualFold(target, handle)
}

func ensureTargetIsNotOperator(t team.Team, action, target string) error {
	if isOperatorTarget(t, target) {
		return operatorMailboxOnlyError(action, strings.TrimSpace(target))
	}
	return nil
}

func ensureLaunchTargetIsNotOperator(projectDir, profile, action, role, handle string) error {
	reserved := map[string]bool{strings.ToLower(team.DefaultOperatorHandle): true}
	if team.ExistsProfile(projectDir, profile) {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		op := team.EffectiveOperator(t)
		if op.Enabled {
			operatorHandle := strings.TrimSpace(op.Handle)
			if operatorHandle == "" {
				operatorHandle = team.DefaultOperatorHandle
			}
			reserved[strings.ToLower(operatorHandle)] = true
		}
	}
	for _, target := range []string{role, handle} {
		target = strings.TrimSpace(target)
		if target != "" && reserved[strings.ToLower(target)] {
			return operatorMailboxOnlyError(action, target)
		}
	}
	return nil
}
