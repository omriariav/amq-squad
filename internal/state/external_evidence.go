package state

import (
	"sort"
	"strings"
	"time"
)

// ExternalEvidenceRow is a read-only projection of externally orchestrated task
// traffic observed in AMQ mailboxes. It is evidence only: callers must not turn
// these rows into native task-store mutations or copy-ready mutation actions.
type ExternalEvidenceRow struct {
	Source         string    `json:"source"`
	SourceLabel    string    `json:"source_label"`
	ExternalTaskID string    `json:"external_task_id,omitempty"`
	State          string    `json:"state"`
	Labels         []string  `json:"labels,omitempty"`
	Thread         string    `json:"thread"`
	MessageID      string    `json:"message_id"`
	Subject        string    `json:"subject,omitempty"`
	Participants   []string  `json:"participants,omitempty"`
	LastEventAt    time.Time `json:"last_event_at"`
	Mutable        bool      `json:"mutable"`
}

// BuildExternalEvidence derives source-labeled external evidence from the same
// collapsed thread summaries used by the console and status surfaces.
func BuildExternalEvidence(threads []ThreadSummary) []ExternalEvidenceRow {
	rows := make([]ExternalEvidenceRow, 0)
	for _, t := range threads {
		sourceLabel, source, ok := externalSource(t.Labels)
		if !ok {
			continue
		}
		rows = append(rows, ExternalEvidenceRow{
			Source:         source,
			SourceLabel:    sourceLabel,
			ExternalTaskID: t.ExternalTaskID,
			State:          externalState(t.Labels),
			Labels:         append([]string(nil), t.Labels...),
			Thread:         t.ID,
			MessageID:      t.LatestID,
			Subject:        t.Subject,
			Participants:   append([]string(nil), t.Participants...),
			LastEventAt:    t.LastEventAt,
			Mutable:        false,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].LastEventAt.Equal(rows[j].LastEventAt) {
			return rows[i].LastEventAt.After(rows[j].LastEventAt)
		}
		if rows[i].Source != rows[j].Source {
			return rows[i].Source < rows[j].Source
		}
		return rows[i].Thread < rows[j].Thread
	})
	return rows
}

func externalSource(labels []string) (sourceLabel, source string, ok bool) {
	hasBare := false
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "orchestrator" {
			hasBare = true
			continue
		}
		raw, found := strings.CutPrefix(label, "orchestrator:")
		if !found {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		return label, normalizeExternalSource(raw), true
	}
	if hasBare {
		return "orchestrator", "external-orchestrator", true
	}
	return "", "", false
}

func normalizeExternalSource(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "swarm", "amq-swarm":
		return "amq-swarm"
	case "symphony", "amq-symphony":
		return "amq-symphony"
	case "kanban", "amq-kanban":
		return "amq-kanban"
	default:
		return raw
	}
}

func externalState(labels []string) string {
	for _, label := range labels {
		raw, ok := strings.CutPrefix(strings.TrimSpace(label), "task-state:")
		if !ok {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw != "" {
			return raw
		}
	}
	return "unknown"
}
