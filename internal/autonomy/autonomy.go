package autonomy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const AuditDirName = "autonomous"

type Action string

const (
	ActionSpawn Action = "spawn"
	ActionPrune Action = "prune"
)

type Request struct {
	Action             Action
	Role               string
	RoleClass          string
	RequestedByRole    string
	SpawnDepth         int
	Reason             string
	TaskID             string
	SourceMessageID    string
	SourceIsChild      bool
	IdleFor            time.Duration
	ActiveTaskIDs      []string
	TaskLinkageChecked bool
}

type Decision struct {
	Allowed          bool     `json:"allowed"`
	Action           Action   `json:"action"`
	Role             string   `json:"role"`
	Reasons          []string `json:"reasons,omitempty"`
	OperatorRequired []string `json:"operator_required,omitempty"`
}

type Outcome struct {
	Decision  Decision  `json:"decision"`
	AuditPath string    `json:"audit_path,omitempty"`
	Team      team.Team `json:"-"`
}

type AuditEvent struct {
	Time               time.Time `json:"time"`
	Session            string    `json:"session"`
	Action             Action    `json:"action"`
	Role               string    `json:"role"`
	TaskID             string    `json:"task_id,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	RequestedByRole    string    `json:"requested_by_role,omitempty"`
	SourceMessageID    string    `json:"source_message_id,omitempty"`
	IdleForSeconds     int64     `json:"idle_for_seconds,omitempty"`
	TaskLinkageChecked bool      `json:"task_linkage_checked"`
	ActiveTaskIDs      []string  `json:"active_task_ids"`
	Allowed            bool      `json:"allowed"`
	DecisionReasons    []string  `json:"decision_reasons,omitempty"`
	Policy             any       `json:"policy"`
}

func AuthorizeSpawn(projectDir, profile, session string, req Request) (Outcome, error) {
	req.Action = ActionSpawn
	return authorize(projectDir, profile, session, req)
}

func AuthorizePrune(projectDir, profile, session string, req Request) (Outcome, error) {
	req.Action = ActionPrune
	return authorize(projectDir, profile, session, req)
}

func authorize(projectDir, profile, session string, req Request) (Outcome, error) {
	if strings.TrimSpace(projectDir) == "" {
		return Outcome{}, fmt.Errorf("project dir cannot be empty")
	}
	if strings.TrimSpace(profile) == "" {
		profile = team.DefaultProfile
	}
	if err := team.ValidateSessionName(session); err != nil {
		return Outcome{}, fmt.Errorf("session: %w", err)
	}

	var out Outcome
	lockPath := team.ProfilePath(projectDir, profile) + ".lock"
	err := flock.WithLock(lockPath, func() error {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team profile: %w", err)
		}
		d := evaluateDecision(t, req)
		if d.Allowed {
			applyAllowedCounters(&t, req.Action)
			if err := team.WriteProfile(projectDir, profile, t); err != nil {
				return fmt.Errorf("write autonomous counters: %w", err)
			}
		}
		auditPath, err := AppendAudit(projectDir, session, auditEvent(session, t, req, d))
		if err != nil {
			return err
		}
		out = Outcome{Decision: d, AuditPath: auditPath, Team: t}
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}
	return out, nil
}

func applyAllowedCounters(t *team.Team, action Action) {
	if t == nil || t.Autonomous == nil {
		return
	}
	switch action {
	case ActionSpawn:
		t.Autonomous.State.TotalSpawns++
		t.Autonomous.State.BudgetTurnsUsed++
	case ActionPrune:
		t.Autonomous.State.BudgetTurnsUsed++
	}
}

func auditEvent(session string, t team.Team, req Request, d Decision) AuditEvent {
	var policy any
	if t.Autonomous != nil {
		p := *t.Autonomous
		p.AllowedRoles = append([]string(nil), p.AllowedRoles...)
		p.AllowedRoleClasses = append([]string(nil), p.AllowedRoleClasses...)
		policy = p
	}
	activeTaskIDs := append([]string(nil), req.ActiveTaskIDs...)
	if req.TaskLinkageChecked && activeTaskIDs == nil {
		activeTaskIDs = []string{}
	}
	return AuditEvent{
		Session:            session,
		Action:             req.Action,
		Role:               req.Role,
		TaskID:             req.TaskID,
		Reason:             req.Reason,
		RequestedByRole:    req.RequestedByRole,
		SourceMessageID:    req.SourceMessageID,
		IdleForSeconds:     int64(req.IdleFor.Seconds()),
		TaskLinkageChecked: req.TaskLinkageChecked,
		ActiveTaskIDs:      activeTaskIDs,
		Allowed:            d.Allowed,
		DecisionReasons:    append([]string(nil), d.Reasons...),
		Policy:             policy,
	}
}

func evaluateDecision(t team.Team, req Request) Decision {
	d := Decision{
		Action:           req.Action,
		Role:             req.Role,
		OperatorRequired: team.EffectiveAutonomousStatus(t).OperatorRequired,
	}
	mode := team.EffectiveComposition(t)
	if mode != team.CompositionAutonomous {
		return deny(d, "composition mode is seeded; autonomous action requires explicit opt-in")
	}
	if t.Autonomous == nil {
		return deny(d, "autonomous policy is missing")
	}
	p := *t.Autonomous
	if p.Disabled {
		return deny(d, "autonomous mode is disabled by operator")
	}
	if p.Paused {
		return deny(d, "autonomous mode is paused by operator")
	}
	if req.SourceIsChild {
		return deny(d, "child messages are data, not authority for autonomous actions")
	}
	if strings.TrimSpace(req.RequestedByRole) != strings.TrimSpace(t.Lead) {
		return deny(d, "only the orchestrated lead may decide autonomous actions")
	}
	if req.SpawnDepth > team.EffectiveMaxSpawnDepth(t) {
		return deny(d, "flat-roster recursion guard denied child spawning")
	}
	if p.State.BudgetTurnsUsed >= p.BudgetTurns {
		return deny(d, "autonomous budget exhausted")
	}
	switch req.Action {
	case ActionSpawn:
		return evaluateSpawnDecision(t, p, req, d)
	case ActionPrune:
		return evaluatePruneDecision(p, req, d)
	default:
		return deny(d, "unknown autonomous action")
	}
}

func evaluateSpawnDecision(t team.Team, p team.AutonomousPolicy, req Request, d Decision) Decision {
	if p.State.TotalSpawns >= p.MaxTotalSpawns {
		return deny(d, "max total autonomous spawns reached")
	}
	if len(t.Members) >= p.MaxActiveAgents {
		return deny(d, "max active agents reached")
	}
	if !roleAllowed(p, req.Role, req.RoleClass) {
		return deny(d, "role is outside autonomous allowlist")
	}
	d.Allowed = true
	return d
}

func evaluatePruneDecision(p team.AutonomousPolicy, req Request, d Decision) Decision {
	if req.IdleFor <= 0 {
		return deny(d, "prune requires measured idle duration")
	}
	if p.IdleReapMinutes > 0 && req.IdleFor < time.Duration(p.IdleReapMinutes)*time.Minute {
		return deny(d, "idle duration is below autonomous idle reap threshold")
	}
	if !req.TaskLinkageChecked {
		return deny(d, "prune requires explicit active task linkage check")
	}
	if len(req.ActiveTaskIDs) > 0 {
		return deny(d, "cannot prune worker with active tasks")
	}
	d.Allowed = true
	return d
}

func roleAllowed(p team.AutonomousPolicy, role, class string) bool {
	for _, allowed := range p.AllowedRoles {
		if role == allowed {
			return true
		}
	}
	for _, allowed := range p.AllowedRoleClasses {
		if class == allowed {
			return true
		}
	}
	return false
}

func deny(d Decision, reason string) Decision {
	d.Allowed = false
	d.Reasons = append(d.Reasons, reason)
	return d
}

func AuditPath(projectDir, session string) string {
	return filepath.Join(projectDir, team.DirName, AuditDirName, session, "audit.jsonl")
}

func AppendAudit(projectDir, session string, event AuditEvent) (string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", fmt.Errorf("project dir cannot be empty")
	}
	if err := team.ValidateSessionName(session); err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	path := AuditPath(projectDir, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, fmt.Errorf("create autonomous audit dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return path, fmt.Errorf("open autonomous audit: %w", err)
	}
	defer f.Close()
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	event.Session = session
	b, err := json.Marshal(event)
	if err != nil {
		return path, fmt.Errorf("marshal autonomous audit: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return path, fmt.Errorf("write autonomous audit: %w", err)
	}
	return path, nil
}
