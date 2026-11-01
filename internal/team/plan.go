package team

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The lead's plan (brief 31-3, section 1): what the lead writes when it
// proposes a team from the conversation, before any squad.yaml exists. It
// names the goal, the seats it wants (handle, role, runtime, model, effort),
// the files the work touches and the first tasks. `team propose --from
// PLAN.json` renders squad.yaml from it (RenderPlan) and validates that
// squad.yaml against the owner grant exactly as a squad.yaml draft is
// validated; the goal, the files and the tasks are printed with the plan
// and are not squad.yaml keys (squad.yaml has none for them).

// PlanSeat is one seat the lead wants.
type PlanSeat struct {
	Handle  string `json:"handle"`
	Role    string `json:"role,omitempty"`
	Runtime string `json:"runtime"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// PlanTask is one first task: printed with the plan for the owner.
type PlanTask struct {
	Title string   `json:"title"`
	Class string   `json:"class,omitempty"`
	Files []string `json:"files,omitempty"`
}

// LeadPlan is the plan the lead writes.
type LeadPlan struct {
	Goal    string     `json:"goal"`
	Session string     `json:"session"`
	Lead    string     `json:"lead"` // a seat handle
	Seats   []PlanSeat `json:"seats"`
	Files   []string   `json:"files,omitempty"`
	Tasks   []PlanTask `json:"tasks,omitempty"`
}

// IsPlanText reports whether a --from file is a plan (a JSON object) rather
// than a squad.yaml draft.
func IsPlanText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "{")
}

// ParsePlan reads a plan strictly: one JSON object, every key known.
func ParsePlan(text string) (LeadPlan, error) {
	var p LeadPlan
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("the plan is not a plan object (goal, session, lead, seats, files, tasks): %w", err)
	}
	if dec.More() {
		return p, fmt.Errorf("the plan holds more than one JSON value")
	}
	if strings.TrimSpace(p.Goal) == "" {
		return p, fmt.Errorf("the plan names no goal")
	}
	if len(p.Seats) == 0 {
		return p, fmt.Errorf("the plan names no seat")
	}
	return p, nil
}

// RenderPlan is the squad.yaml text of a plan, in canonical form
// (Render). ParseSquad checks it against the owner grant afterwards.
func RenderPlan(p LeadPlan) string {
	s := Squad{Schema: SquadSchema, Session: p.Session, Lead: p.Lead}
	for _, seat := range p.Seats {
		role := seat.Role
		if role == "" {
			role = seat.Handle
		}
		s.Seats = append(s.Seats, Seat{Handle: seat.Handle, Role: role, Runtime: seat.Runtime, Model: seat.Model, Effort: seat.Effort})
	}
	return Render(s)
}

// PlanLines is the plan as the owner reads it: the goal, each seat, the
// files and the first tasks.
func PlanLines(p LeadPlan) []string {
	out := []string{"goal: " + oneLine(p.Goal), fmt.Sprintf("session %s, lead %s", p.Session, p.Lead)}
	for _, s := range p.Seats {
		rt := strings.Trim(strings.Join([]string{s.Runtime, s.Model, s.Effort}, "/"), "/")
		role := s.Role
		if role == "" {
			role = s.Handle
		}
		out = append(out, fmt.Sprintf("seat %s (%s) %s", s.Handle, role, rt))
	}
	if len(p.Files) > 0 {
		out = append(out, "files: "+strings.Join(p.Files, ", "))
	}
	for i, t := range p.Tasks {
		line := fmt.Sprintf("task %d: %s", i+1, oneLine(t.Title))
		if t.Class != "" {
			line += " [" + t.Class + "]"
		}
		if len(t.Files) > 0 {
			line += " files " + strings.Join(t.Files, ", ")
		}
		out = append(out, line)
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// DigestPrefix is the 16-hex prefix the owner types to confirm a proposal
// (R55; the typed-prefix length of R32): the first 16 hex of the content
// digest, the part of the apply token after "sha256:".
func DigestPrefix(digest string) string {
	content, _, _ := SplitDigest(digest)
	content = strings.TrimPrefix(content, "sha256:")
	if len(content) < 16 {
		return content
	}
	return content[:16]
}
