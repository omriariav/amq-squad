package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

const leadershipFileName = ".leadership.json"

// LeadershipState is the durable compare-and-swap recovery anchor for the
// visible lead. Epoch changes prevent a stale/recovered lead from dispatching
// over a newer handoff merely because both can still read the mailbox.
type LeadershipState struct {
	Schema      int                 `json:"schema"`
	Epoch       uint64              `json:"epoch"`
	CurrentLead string              `json:"current_lead"`
	Handoffs    []LeadershipHandoff `json:"handoffs"`
}

type LeadershipHandoff struct {
	Epoch    uint64    `json:"epoch"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Reason   string    `json:"reason"`
	Evidence string    `json:"evidence,omitempty"`
	At       time.Time `json:"at"`
}

type LeadershipHandoffInput struct {
	ExpectedEpoch uint64
	From          string
	To            string
	Reason        string
	Evidence      string
	Now           time.Time
}

func ReadLeadershipForProfile(projectDir, profile, session string) (LeadershipState, error) {
	var state LeadershipState
	err := withReadLockForProfile(projectDir, profile, session, func(dir string) error {
		var err error
		state, err = readLeadership(dir)
		return err
	})
	return state, err
}

func ReadLeadership(projectDir, session string) (LeadershipState, error) {
	return ReadLeadershipForProfile(projectDir, team.DefaultProfile, session)
}

func HandoffLeadershipForProfile(projectDir, profile, session string, in LeadershipHandoffInput) (LeadershipState, error) {
	from, to, reason := strings.TrimSpace(in.From), strings.TrimSpace(in.To), strings.TrimSpace(in.Reason)
	if from == "" || to == "" || reason == "" {
		return LeadershipState{}, fmt.Errorf("leadership handoff requires from, to, and reason")
	}
	if from == to {
		return LeadershipState{}, fmt.Errorf("leadership handoff from and to must be distinct")
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	configured, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return LeadershipState{}, fmt.Errorf("read configured lead for leadership handoff: %w", err)
	}
	initialLead := configuredLeadHandle(configured)
	if initialLead == "" {
		return LeadershipState{}, fmt.Errorf("leadership handoff requires an authoritative configured profile lead")
	}
	var out LeadershipState
	err = withLockForProfile(projectDir, profile, session, func(dir string) error {
		state, err := readLeadership(dir)
		if err != nil {
			return err
		}
		if state.Epoch != in.ExpectedEpoch {
			return fmt.Errorf("leadership epoch changed: expected %d, current %d (recover from the current record before dispatch)", in.ExpectedEpoch, state.Epoch)
		}
		if state.Epoch == 0 && from != initialLead {
			return fmt.Errorf("initial leadership incumbent is configured lead %q; %q cannot establish epoch 1", initialLead, from)
		}
		if state.Epoch > 0 && state.CurrentLead != from {
			return fmt.Errorf("leadership handoff actor %q is stale; epoch %d belongs to %q", from, state.Epoch, state.CurrentLead)
		}
		state.Schema = 1
		state.Epoch++
		state.CurrentLead = to
		state.Handoffs = append(state.Handoffs, LeadershipHandoff{
			Epoch: state.Epoch, From: from, To: to, Reason: reason,
			Evidence: strings.TrimSpace(in.Evidence), At: in.Now.UTC(),
		})
		if err := writeLeadership(dir, state); err != nil {
			return err
		}
		out = state
		return nil
	})
	return out, err
}

func configuredLeadHandle(t team.Team) string {
	lead := strings.TrimSpace(t.Lead)
	if lead == "" {
		return ""
	}
	for _, member := range t.Members {
		if strings.TrimSpace(member.Role) == lead {
			if handle := strings.TrimSpace(member.Handle); handle != "" {
				return handle
			}
			return lead
		}
	}
	return ""
}

func readLeadership(dir string) (LeadershipState, error) {
	b, err := os.ReadFile(filepath.Join(dir, leadershipFileName))
	if os.IsNotExist(err) {
		return LeadershipState{Schema: 1, Handoffs: []LeadershipHandoff{}}, nil
	}
	if err != nil {
		return LeadershipState{}, err
	}
	var state LeadershipState
	if err := json.Unmarshal(b, &state); err != nil {
		return LeadershipState{}, fmt.Errorf("parse leadership record: %w", err)
	}
	if state.Schema != 1 || state.Epoch == 0 || len(state.Handoffs) == 0 || state.CurrentLead == "" || state.Handoffs[len(state.Handoffs)-1].Epoch != state.Epoch || state.Handoffs[len(state.Handoffs)-1].To != state.CurrentLead {
		return LeadershipState{}, fmt.Errorf("leadership record is inconsistent; manual recovery required")
	}
	return state, nil
}

func writeLeadership(dir string, state LeadershipState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, leadershipFileName)
	tmp := path + ".tmp"
	if err := writeSyncedFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}
