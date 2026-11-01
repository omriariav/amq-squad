package cli

import (
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// prBoundTo answers which task the store binds a repository's PR number to.
func prBoundTo(s task.Store) func(string, int) string {
	return func(repository string, n int) string {
		list, err := s.List()
		if err != nil {
			return "unknown (the task store is unreadable)"
		}
		for _, t := range list {
			if t.PRNumber == n && strings.EqualFold(t.PRRepository, repository) {
				return t.ID
			}
		}
		return ""
	}
}
