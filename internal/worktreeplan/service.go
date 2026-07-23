package worktreeplan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type Service struct {
	team    team.Team
	profile string
	session string
	git     GitRunner
	now     func() time.Time
}

func NewService(t team.Team, profile, session string, git GitRunner, now func() time.Time) (*Service, error) {
	if strings.TrimSpace(t.Project) == "" {
		return nil, fmt.Errorf("team home is required")
	}
	if strings.TrimSpace(profile) == "" {
		profile = team.DefaultProfile
	}
	if profile != team.DefaultProfile {
		if err := team.ValidateProfileName(profile); err != nil {
			return nil, err
		}
	}
	project, err := filepath.Abs(t.Project)
	if err != nil {
		return nil, fmt.Errorf("resolve team home: %w", err)
	}
	t.Project = filepath.Clean(project)
	// A linked worktree may contain the tracked team.json, but its persisted
	// control_root still points at the canonical team-home. Prefer that copy
	// when it exists so no lifecycle mutation ever rewrites a worktree-local
	// control plane.
	control := ControlRoot(t)
	if control != t.Project && team.ExistsProfile(control, profile) {
		canonical, readErr := team.ReadProfile(control, profile)
		if readErr != nil {
			return nil, fmt.Errorf("read canonical team profile at control root: %w", readErr)
		}
		t = canonical
	}
	if strings.TrimSpace(session) == "" {
		return nil, fmt.Errorf("session is required")
	}
	session = strings.TrimSpace(session)
	if session == "." || session == ".." || filepath.Base(session) != session || strings.Contains(session, `\`) {
		return nil, fmt.Errorf("session %q is not a safe path segment", session)
	}
	if git == nil {
		git = ExecGit{}
	}
	if now == nil {
		now = time.Now
	}
	return &Service{team: t, profile: profile, session: session, git: git, now: now}, nil
}

func (s *Service) Plan(req Request) (Set, Record, error) {
	set, exists, err := Read(s.team, s.profile, s.session)
	if err != nil {
		return Set{}, Record{}, err
	}
	originalCreated, originalUpdated := set.CreatedAt, set.UpdatedAt
	previous, _ := recordByRole(set, req.Role)
	set, record, index, err := s.prepare(set, exists, req)
	if err != nil {
		return Set{}, Record{}, err
	}
	// A read-only preview must be byte-deterministic for the same repository
	// state. Persisted timestamps remain visible; proposed timestamps do not
	// exist until materialize commits the planned record.
	set.CreatedAt, set.UpdatedAt = originalCreated, originalUpdated
	if previous == nil || previous.State == StateCleaned {
		record.CreatedAt, record.UpdatedAt = nil, nil
		set.Plans[index] = record
	}
	return set, record, err
}

func (s *Service) Materialize(req Request) (Set, Record, error) {
	var result Set
	var record Record
	err := WithLock(s.team, s.profile, s.session, func() error {
		set, exists, err := Read(s.team, s.profile, s.session)
		if err != nil {
			return err
		}
		preexisting := false
		if existing, _ := recordByRole(set, req.Role); existing != nil {
			preexisting = true
		}
		set, planned, index, err := s.prepare(set, exists, req)
		if err != nil {
			return err
		}
		if planned.State == StateMaterialized || planned.State == StateActive || planned.State == StateHandoff {
			status, err := s.inspectRecord(set, planned)
			if err == nil && status.Registered && !status.Drifted {
				result, record = set, planned
				return nil
			}
		}
		registered, err := listWorktrees(s.git, planned.RepoRoot)
		if err != nil {
			return err
		}
		atPath, branchAt := findRegistrations(registered, planned.Path, planned.Branch)
		switch {
		case atPath != nil:
			if !preexisting {
				return fmt.Errorf("refuse occupied unknown worktree path %s", planned.Path)
			}
			if atPath.BranchRef != branchRef(planned.Branch) {
				return fmt.Errorf("refuse occupied worktree path %s: registered branch is %q, want %q", planned.Path, atPath.BranchRef, branchRef(planned.Branch))
			}
			if branchAt != nil && filepath.Clean(branchAt.Path) != filepath.Clean(planned.Path) {
				return fmt.Errorf("refuse duplicate branch %s: registered at %s", planned.Branch, branchAt.Path)
			}
		case branchAt != nil:
			return fmt.Errorf("refuse duplicate branch %s: registered at %s", planned.Branch, branchAt.Path)
		default:
			if _, statErr := os.Lstat(planned.Path); statErr == nil {
				return fmt.Errorf("refuse occupied unknown path %s: it is not a registered Git worktree", planned.Path)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("inspect worktree path %s: %w", planned.Path, statErr)
			}
			if commit, branchExists := branchCommit(s.git, planned.RepoRoot, planned.Branch); branchExists {
				if !preexisting || commit != planned.BaseSHA {
					return fmt.Errorf("refuse existing branch %s at %s (accepted base %s)", planned.Branch, commit, planned.BaseSHA)
				}
			}
		}
		planned.State = StatePlanned
		planned.HandoffSHA = ""
		updatedAt := s.now().UTC()
		planned.UpdatedAt = &updatedAt
		set.Plans[index] = planned
		set.UpdatedAt = planned.UpdatedAt
		if err := WriteUnderLock(s.team, s.profile, s.session, set); err != nil {
			return err
		}

		switch {
		case atPath != nil:
			// A matching registered path can only reach this point through a
			// pre-existing durable record, so it is safe crash recovery.
		case branchAt != nil:
			return fmt.Errorf("refuse duplicate branch %s: registered at %s", planned.Branch, branchAt.Path)
		default:
			if _, branchExists := branchCommit(s.git, planned.RepoRoot, planned.Branch); branchExists {
				if _, err := s.git.Run(planned.RepoRoot, "worktree", "add", planned.Path, planned.Branch); err != nil {
					return fmt.Errorf("recover planned worktree: %w", err)
				}
			} else if _, err := s.git.Run(planned.RepoRoot, "worktree", "add", "-b", planned.Branch, planned.Path, planned.BaseSHA); err != nil {
				return fmt.Errorf("materialize worktree: %w", err)
			}
		}

		if err := s.setMemberCWD(planned, planned.Path); err != nil {
			return err
		}
		status, err := s.inspectRecord(set, planned)
		if err != nil {
			return err
		}
		if !status.Registered || status.Drifted || !status.Clean {
			return fmt.Errorf("materialized worktree failed validation: %s", status.Detail)
		}
		planned.State = StateMaterialized
		updatedAt = s.now().UTC()
		planned.UpdatedAt = &updatedAt
		set.Plans[index] = planned
		set.UpdatedAt = planned.UpdatedAt
		if err := WriteUnderLock(s.team, s.profile, s.session, set); err != nil {
			return err
		}
		result, record = set, planned
		return nil
	})
	return result, record, err
}

func (s *Service) Activate(role string) (Record, error) {
	return s.transition(role, func(set Set, record *Record) error {
		if record.State == StatePlanned {
			return fmt.Errorf("refuse activation: planned worktree must finish materialization recovery first")
		}
		status, err := s.inspectRecord(set, *record)
		if err != nil {
			return err
		}
		if !status.Registered || status.Drifted {
			return fmt.Errorf("refuse activation: %s", status.Detail)
		}
		record.State = StateActive
		record.HandoffSHA = ""
		return nil
	})
}

func (s *Service) Handoff(role, expectedSHA string) (Record, error) {
	return s.transition(role, func(set Set, record *Record) error {
		if record.State == StatePlanned {
			return fmt.Errorf("refuse handoff: planned worktree must finish materialization recovery first")
		}
		status, err := s.inspectRecord(set, *record)
		if err != nil {
			return err
		}
		if !status.Registered || status.Drifted || !status.Clean {
			return fmt.Errorf("refuse handoff: %s", status.Detail)
		}
		if expectedSHA != "" && status.CurrentHEAD != strings.TrimSpace(expectedSHA) {
			return fmt.Errorf("refuse handoff: HEAD %s does not match expected %s", status.CurrentHEAD, strings.TrimSpace(expectedSHA))
		}
		record.State = StateHandoff
		record.HandoffSHA = status.CurrentHEAD
		return nil
	})
}

func (s *Service) Cleanup(req CleanupRequest) (Record, error) {
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "accepted" && decision != "rejected" {
		return Record{}, fmt.Errorf("cleanup decision must be accepted or rejected")
	}
	var result Record
	err := WithLock(s.team, s.profile, s.session, func() error {
		set, exists, err := Read(s.team, s.profile, s.session)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("no worktree plan for %s/%s", s.profile, s.session)
		}
		record, index := recordByRole(set, req.Role)
		if record == nil {
			return fmt.Errorf("role %q has no registered worktree plan", req.Role)
		}
		if record.State == StateCleaned {
			if record.CleanupDecision != "" && record.CleanupDecision != decision {
				return fmt.Errorf("worktree was already cleaned with decision %q", record.CleanupDecision)
			}
			result = *record
			return nil
		}
		status, inspectErr := s.inspectRecord(set, *record)
		recovering := record.CleanupStartedAt != nil
		if inspectErr != nil && !recovering {
			return inspectErr
		}
		if !status.Registered {
			if !recovering {
				return fmt.Errorf("refuse cleanup of unknown path %s: it is not a registered Git worktree", record.Path)
			}
		} else {
			if status.Dirty {
				return fmt.Errorf("refuse cleanup of dirty worktree %s", record.Path)
			}
			if status.Drifted {
				return fmt.Errorf("refuse cleanup of drifted worktree: %s", status.Detail)
			}
			if record.HandoffSHA != "" && status.CurrentHEAD != record.HandoffSHA {
				return fmt.Errorf("refuse cleanup: current HEAD %s differs from handoff %s", status.CurrentHEAD, record.HandoffSHA)
			}
		}
		if !recovering {
			record.CleanupDecision = decision
			startedAt := s.now().UTC()
			record.CleanupStartedAt = &startedAt
			record.UpdatedAt = &startedAt
			set.Plans[index] = *record
			set.UpdatedAt = record.UpdatedAt
			if err := WriteUnderLock(s.team, s.profile, s.session, set); err != nil {
				return err
			}
		} else if record.CleanupDecision != decision {
			return fmt.Errorf("cleanup recovery is already bound to decision %q", record.CleanupDecision)
		}
		if err := s.restoreMemberCWD(*record); err != nil {
			return err
		}
		if status.Registered {
			if _, err := s.git.Run(record.RepoRoot, "worktree", "remove", record.Path); err != nil {
				return fmt.Errorf("remove registered worktree: %w", err)
			}
		}
		record.State = StateCleaned
		record.HandoffSHA = ""
		updatedAt := s.now().UTC()
		record.UpdatedAt = &updatedAt
		set.Plans[index] = *record
		set.UpdatedAt = record.UpdatedAt
		if err := WriteUnderLock(s.team, s.profile, s.session, set); err != nil {
			return err
		}
		result = *record
		return nil
	})
	return result, err
}

func (s *Service) SetSharedCWDException(enabled bool, reason string) (Set, error) {
	return s.SetSharedCWDExceptionAtAMQRoot(enabled, reason, "")
}

// SetSharedCWDExceptionAtAMQRoot preserves an explicitly resolved custom AMQ
// root when the exception is the first mutation that creates the session set.
func (s *Service) SetSharedCWDExceptionAtAMQRoot(enabled bool, reason, amqRoot string) (Set, error) {
	var result Set
	err := WithLock(s.team, s.profile, s.session, func() error {
		set, exists, err := Read(s.team, s.profile, s.session)
		if err != nil {
			return err
		}
		if !exists {
			set, err = s.newSet(Request{AMQRoot: amqRoot})
			if err != nil {
				return err
			}
		}
		reason = strings.TrimSpace(reason)
		if enabled && reason == "" {
			return fmt.Errorf("a non-empty reason is required for the shared-cwd exception")
		}
		updatedAt := s.now().UTC()
		set.SharedCWDException = SharedCWDException{Enabled: enabled, Reason: reason, UpdatedAt: &updatedAt}
		set.UpdatedAt = &updatedAt
		if err := WriteUnderLock(s.team, s.profile, s.session, set); err != nil {
			return err
		}
		result = set
		return nil
	})
	return result, err
}

func (s *Service) Inspect() (Inspection, error) {
	set, exists, err := Read(s.team, s.profile, s.session)
	if err != nil {
		return Inspection{StorePath: StorePath(s.team, s.profile, s.session), Exists: true}, err
	}
	inspection := Inspection{StorePath: StorePath(s.team, s.profile, s.session), Exists: exists, Set: set}
	planByRole := map[string]Record{}
	for _, record := range set.Plans {
		planByRole[record.Role] = record
	}
	for _, member := range s.team.Members {
		if team.EffectiveActorMode(s.team, member) != team.ActorModeImplementation {
			continue
		}
		record, ok := planByRole[member.Role]
		if !ok {
			inspection.Members = append(inspection.Members, s.inspectUnplanned(member))
			continue
		}
		status, inspectErr := s.inspectRecord(set, record)
		status.CWD = member.EffectiveCWD(s.team.Project)
		if record.State != StatePlanned && record.State != StateCleaned && filepath.Clean(status.CWD) != filepath.Clean(record.Path) {
			status.Drifted = true
			status.Detail = strings.Trim(strings.TrimSpace(status.Detail)+"; member cwd is "+status.CWD+", want "+record.Path, "; ")
		}
		if inspectErr != nil && status.Detail == "" {
			status.Detail = inspectErr.Error()
		}
		inspection.Members = append(inspection.Members, status)
	}
	sort.Slice(inspection.Members, func(i, j int) bool { return inspection.Members[i].Role < inspection.Members[j].Role })
	inspection.Diagnostics = s.diagnostics(inspection)
	return inspection, nil
}

func (s *Service) transition(role string, change func(Set, *Record) error) (Record, error) {
	var result Record
	err := WithLock(s.team, s.profile, s.session, func() error {
		set, exists, err := Read(s.team, s.profile, s.session)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("no worktree plan for %s/%s", s.profile, s.session)
		}
		record, index := recordByRole(set, role)
		if record == nil {
			return fmt.Errorf("role %q has no worktree plan", role)
		}
		if record.State == StateCleaned {
			return fmt.Errorf("role %q worktree is already cleaned", role)
		}
		if err := change(set, record); err != nil {
			return err
		}
		updatedAt := s.now().UTC()
		record.UpdatedAt = &updatedAt
		set.Plans[index] = *record
		set.UpdatedAt = record.UpdatedAt
		if err := WriteUnderLock(s.team, s.profile, s.session, set); err != nil {
			return err
		}
		result = *record
		return nil
	})
	return result, err
}

func (s *Service) prepare(set Set, exists bool, req Request) (Set, Record, int, error) {
	member, err := s.member(req.Role)
	if err != nil {
		return Set{}, Record{}, -1, err
	}
	repoCandidate := strings.TrimSpace(req.RepoRoot)
	if repoCandidate == "" {
		repoCandidate = strings.TrimSpace(s.team.TargetProjectRoot)
	}
	if repoCandidate == "" {
		repoCandidate = s.team.Project
	}
	repoRoot, err := gitRoot(s.git, repoCandidate)
	if err != nil {
		return Set{}, Record{}, -1, fmt.Errorf("resolve target repository: %w", err)
	}
	baseSHA, err := resolveCommit(s.git, repoRoot, req.Base)
	if err != nil {
		return Set{}, Record{}, -1, fmt.Errorf("resolve accepted base: %w", err)
	}
	if !exists {
		set, err = s.newSet(req)
		if err != nil {
			return Set{}, Record{}, -1, err
		}
	}
	if filepath.Clean(set.TeamHome) != filepath.Clean(s.team.Project) || filepath.Clean(set.ControlRoot) != filepath.Clean(ControlRoot(s.team)) {
		return Set{}, Record{}, -1, fmt.Errorf("worktree plan canonical roots do not match the current team profile")
	}
	if requestedAMQRoot := strings.TrimSpace(req.AMQRoot); requestedAMQRoot != "" && filepath.Clean(requestedAMQRoot) != filepath.Clean(set.AMQRoot) {
		return Set{}, Record{}, -1, fmt.Errorf("canonical AMQ root drift: session plan is %s, request is %s", set.AMQRoot, requestedAMQRoot)
	}
	if set.AcceptedBaseSHA != "" && set.AcceptedBaseSHA != baseSHA {
		return Set{}, Record{}, -1, fmt.Errorf("accepted base drift: session plan is %s, request resolves to %s", set.AcceptedBaseSHA, baseSHA)
	}
	if set.AcceptedBaseSHA == "" {
		set.AcceptedBaseSHA = baseSHA
	}
	scope := normalizedScope(req.Scope)
	if len(scope) == 0 {
		return Set{}, Record{}, -1, fmt.Errorf("at least one --scope entry is required")
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		return Set{}, Record{}, -1, fmt.Errorf("task id is required")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = defaultPath(repoRoot, s.profile, s.session, member.Role)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Set{}, Record{}, -1, fmt.Errorf("resolve worktree path: %w", err)
	}
	path = filepath.Clean(path)
	if path == repoRoot {
		return Set{}, Record{}, -1, fmt.Errorf("worktree path cannot be the primary repository")
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = defaultBranch(s.profile, s.session, member.Role, taskID)
	}
	if _, err := s.git.Run(repoRoot, "check-ref-format", "--branch", branch); err != nil {
		return Set{}, Record{}, -1, fmt.Errorf("invalid worktree branch %q: %w", branch, err)
	}
	now := s.now().UTC()
	record := Record{
		Role: member.Role, Handle: effectiveHandle(member), Path: path, Branch: branch,
		BaseSHA: baseSHA, TaskID: taskID, Scope: scope, State: StatePlanned,
		RepoRoot: repoRoot, PreviousCWD: member.CWD, PreviousExplicit: strings.TrimSpace(member.CWD) != "",
		CreatedAt: &now, UpdatedAt: &now,
	}
	index := len(set.Plans)
	if current, i := recordByRole(set, member.Role); current != nil {
		index = i
		if current.State != StateCleaned {
			if current.Path != record.Path || current.Branch != record.Branch || current.BaseSHA != record.BaseSHA ||
				current.TaskID != record.TaskID || strings.Join(current.Scope, "\x00") != strings.Join(record.Scope, "\x00") {
				return Set{}, Record{}, -1, fmt.Errorf("role %s already has a different %s worktree plan", member.Role, current.State)
			}
			record = *current
		} else {
			record.CreatedAt = &now
		}
	}
	for i, other := range set.Plans {
		if i == index || other.State == StateCleaned {
			continue
		}
		if filepath.Clean(other.RepoRoot) != record.RepoRoot {
			return Set{}, Record{}, -1, fmt.Errorf("target repository drift: %s uses %s while %s uses %s", record.Role, record.RepoRoot, other.Role, other.RepoRoot)
		}
		if filepath.Clean(other.Path) == record.Path {
			return Set{}, Record{}, -1, fmt.Errorf("duplicate worktree path %s already belongs to %s", record.Path, other.Role)
		}
		if other.Branch == record.Branch {
			return Set{}, Record{}, -1, fmt.Errorf("duplicate branch %s already belongs to %s", record.Branch, other.Role)
		}
	}
	if index == len(set.Plans) {
		set.Plans = append(set.Plans, record)
	} else {
		set.Plans[index] = record
	}
	set.UpdatedAt = &now
	return set, record, index, nil
}

func (s *Service) newSet(req Request) (Set, error) {
	teamHome, _ := filepath.Abs(s.team.Project)
	control, _ := filepath.Abs(ControlRoot(s.team))
	amqRoot := strings.TrimSpace(req.AMQRoot)
	if amqRoot == "" {
		amqRoot = s.defaultAMQRoot()
	}
	if !filepath.IsAbs(amqRoot) {
		return Set{}, fmt.Errorf("canonical AMQ root must be absolute")
	}
	now := s.now().UTC()
	return Set{
		Schema: SchemaVersion, Profile: normalizedProfile(s.profile), Session: s.session,
		TeamHome: filepath.Clean(teamHome), ControlRoot: filepath.Clean(control), AMQRoot: filepath.Clean(amqRoot),
		Plans: []Record{}, CreatedAt: &now, UpdatedAt: &now,
	}, nil
}

func (s *Service) defaultAMQRoot() string {
	return squadnamespace.AMQRoot(s.team.Project, s.profile, s.session)
}

func (s *Service) member(role string) (team.Member, error) {
	role = strings.TrimSpace(role)
	for _, member := range s.team.Members {
		if member.Role == role {
			if team.EffectiveActorMode(s.team, member) != team.ActorModeImplementation {
				return team.Member{}, fmt.Errorf("role %q is not mutation-capable", role)
			}
			return member, nil
		}
	}
	return team.Member{}, fmt.Errorf("unknown team role %q", role)
}

func (s *Service) setMemberCWD(record Record, cwd string) error {
	err := team.WithProfileLock(s.team.Project, s.profile, func() error {
		current, err := team.ReadProfile(s.team.Project, s.profile)
		if err != nil {
			return err
		}
		for i := range current.Members {
			if current.Members[i].Role != record.Role {
				continue
			}
			existing := current.Members[i].EffectiveCWD(s.team.Project)
			if filepath.Clean(existing) != filepath.Clean(record.PreviousCWD) &&
				!(record.PreviousCWD == "" && filepath.Clean(existing) == filepath.Clean(s.team.Project)) &&
				filepath.Clean(existing) != filepath.Clean(cwd) {
				return fmt.Errorf("refuse member cwd overwrite: %s now points at %s", record.Role, existing)
			}
			current.Members[i].CWD = filepath.Clean(cwd)
			return team.WriteProfileUnderLock(s.team.Project, s.profile, current)
		}
		return fmt.Errorf("role %q disappeared from team profile", record.Role)
	})
	if err == nil {
		for i := range s.team.Members {
			if s.team.Members[i].Role == record.Role {
				s.team.Members[i].CWD = filepath.Clean(cwd)
			}
		}
	}
	return err
}

func (s *Service) restoreMemberCWD(record Record) error {
	err := team.WithProfileLock(s.team.Project, s.profile, func() error {
		current, err := team.ReadProfile(s.team.Project, s.profile)
		if err != nil {
			return err
		}
		for i := range current.Members {
			if current.Members[i].Role != record.Role {
				continue
			}
			existing := current.Members[i].EffectiveCWD(s.team.Project)
			if filepath.Clean(existing) != filepath.Clean(record.Path) {
				want := record.PreviousCWD
				if want == "" {
					want = s.team.Project
				}
				if filepath.Clean(existing) == filepath.Clean(want) {
					return nil
				}
				return fmt.Errorf("refuse cwd restore: %s now points at %s", record.Role, existing)
			}
			if record.PreviousExplicit {
				current.Members[i].CWD = record.PreviousCWD
			} else {
				current.Members[i].CWD = ""
			}
			return team.WriteProfileUnderLock(s.team.Project, s.profile, current)
		}
		return fmt.Errorf("role %q disappeared from team profile", record.Role)
	})
	if err == nil {
		for i := range s.team.Members {
			if s.team.Members[i].Role != record.Role {
				continue
			}
			if record.PreviousExplicit {
				s.team.Members[i].CWD = record.PreviousCWD
			} else {
				s.team.Members[i].CWD = ""
			}
		}
	}
	return err
}

func (s *Service) inspectUnplanned(member team.Member) MemberStatus {
	status := MemberStatus{
		Role: member.Role, Handle: effectiveHandle(member), CWD: member.EffectiveCWD(s.team.Project),
		State: StateUnplanned, Detail: "unplanned mutation-capable member",
	}
	status.Worktree = status.CWD
	root, err := gitRoot(s.git, status.CWD)
	if err != nil {
		status.Detail += "; cwd is not an inspectable Git worktree"
		return status
	}
	registered, _ := listWorktrees(s.git, root)
	atPath, _ := findRegistrations(registered, status.CWD, "")
	status.Registered = atPath != nil
	status.CurrentHEAD, _ = currentHEAD(s.git, status.CWD)
	status.Branch, _ = currentBranch(s.git, status.CWD)
	status.Index, _ = indexPath(s.git, status.CWD)
	status.Clean, _ = worktreeClean(s.git, status.CWD)
	status.Dirty = !status.Clean
	return status
}

func (s *Service) inspectRecord(set Set, record Record) (MemberStatus, error) {
	status := MemberStatus{
		Role: record.Role, Handle: record.Handle, CWD: record.Path, Worktree: record.Path,
		Branch: record.Branch, BaseSHA: record.BaseSHA, Scope: append([]string(nil), record.Scope...),
		TaskID: record.TaskID, State: record.State, HandoffSHA: record.HandoffSHA,
	}
	registered, err := listWorktrees(s.git, record.RepoRoot)
	if err != nil {
		status.Detail = err.Error()
		return status, err
	}
	atPath, branchAt := findRegistrations(registered, record.Path, record.Branch)
	if record.State == StateCleaned {
		status.Clean = true
		if atPath != nil || branchAt != nil {
			status.Drifted = true
			status.Detail = "cleaned plan still has a registered worktree"
		} else {
			status.Detail = "cleaned"
		}
		return status, nil
	}
	if atPath == nil {
		status.Orphaned = pathExists(record.Path)
		status.Drifted = record.State != StatePlanned
		status.Detail = "planned path is not registered"
		if status.Orphaned {
			status.Detail = "path exists but is not registered by Git"
		}
		if branchAt != nil {
			status.Detail = "planned branch is registered at " + branchAt.Path
			status.Drifted = true
		}
		return status, nil
	}
	status.Registered = true
	if atPath.Prunable || !pathExists(record.Path) {
		status.Orphaned = true
		status.Drifted = true
		status.Detail = "Git registration is stale or prunable"
		return status, nil
	}
	status.CurrentHEAD, _ = currentHEAD(s.git, record.Path)
	actualBranch, branchErr := currentBranch(s.git, record.Path)
	status.Index, _ = indexPath(s.git, record.Path)
	status.Clean, _ = worktreeClean(s.git, record.Path)
	status.Dirty = !status.Clean
	status.CoordinationRootDiverged = coordinationDiverged(set, record.Path)
	var drift []string
	if branchErr != nil || actualBranch != record.Branch || atPath.BranchRef != branchRef(record.Branch) {
		drift = append(drift, "branch mismatch")
	}
	if branchAt != nil && filepath.Clean(branchAt.Path) != filepath.Clean(record.Path) {
		drift = append(drift, "branch registered at duplicate path")
	}
	if status.CurrentHEAD == "" || !baseIsAncestor(s.git, record.Path, record.BaseSHA, status.CurrentHEAD) {
		drift = append(drift, "accepted base is not an ancestor of HEAD")
	}
	if status.CoordinationRootDiverged {
		drift = append(drift, "coordination root diverged into worktree")
	}
	status.Drifted = len(drift) > 0
	status.HandoffValid = record.State == StateHandoff && status.Clean && !status.Drifted && status.CurrentHEAD == record.HandoffSHA
	if status.Dirty {
		drift = append(drift, "dirty")
	}
	if record.State == StateHandoff && !status.HandoffValid {
		drift = append(drift, "handoff invalid")
	}
	if len(drift) == 0 {
		status.Detail = "registered, clean, and based on " + shortSHA(record.BaseSHA)
	} else {
		status.Detail = strings.Join(drift, "; ")
	}
	return status, nil
}

func (s *Service) diagnostics(inspection Inspection) []Diagnostic {
	diagnostics := []Diagnostic{{
		Kind: "store", Status: DiagnosticOK,
		Detail: fmt.Sprintf("session plan %s", ternary(inspection.Exists, inspection.StorePath, "not materialized")),
	}}
	indexRoles := map[string][]string{}
	for _, status := range inspection.Members {
		if status.State != StateCleaned && status.Index != "" {
			indexRoles[status.Index] = append(indexRoles[status.Index], status.Role)
		}
	}
	var collisions []string
	for index, roles := range indexRoles {
		if len(roles) > 1 {
			sort.Strings(roles)
			collisions = append(collisions, strings.Join(roles, ",")+" share "+index)
		}
	}
	sort.Strings(collisions)
	collisionStatus := DiagnosticOK
	collisionDetail := "mutation-capable members have distinct Git indexes"
	if len(collisions) > 0 {
		collisionStatus = DiagnosticFail
		collisionDetail = strings.Join(collisions, "; ")
		if inspection.Set.SharedCWDException.Enabled {
			collisionStatus = DiagnosticOK
			collisionDetail += "; explicit exception: " + inspection.Set.SharedCWDException.Reason
		}
	}
	diagnostics = append(diagnostics, Diagnostic{Kind: "shared-index-collision", Status: collisionStatus, Detail: collisionDetail})

	duplicates := duplicateIdentityFindings(inspection.Set.Plans)
	diagnostics = append(diagnostics, diagnosticFromFindings("duplicate-worktree-identity", "no duplicate worktree path or branch", duplicates, DiagnosticFail))
	var drift, dirtyHandoff, roots, stale []string
	for _, status := range inspection.Members {
		if status.Drifted {
			drift = append(drift, status.Role+": "+status.Detail)
		}
		if status.State == StateHandoff && !status.HandoffValid {
			dirtyHandoff = append(dirtyHandoff, status.Role+": "+status.Detail)
		}
		if status.CoordinationRootDiverged {
			roots = append(roots, status.Role+": canonical coordination state resolves inside "+status.Worktree)
		}
		if status.Orphaned || (status.State != "" && status.State != StatePlanned && status.State != StateCleaned && !status.Registered) {
			stale = append(stale, status.Role+": "+status.Detail)
		}
	}
	diagnostics = append(diagnostics,
		diagnosticFromFindings("worktree-plan-drift", "registered worktrees match their plans", drift, DiagnosticFail),
		diagnosticFromFindings("worktree-handoff-cleanliness", "all handoffs are clean and immutable", dirtyHandoff, DiagnosticFail),
		diagnosticFromFindings("coordination-root-divergence", "team-home, control, and AMQ roots remain canonical", roots, DiagnosticFail),
		diagnosticFromFindings("registered-worktree-liveness", "registered worktrees are live; planned absences are expected", stale, DiagnosticFail),
	)
	return diagnostics
}

func diagnosticFromFindings(kind, okDetail string, findings []string, failure DiagnosticStatus) Diagnostic {
	if len(findings) == 0 {
		return Diagnostic{Kind: kind, Status: DiagnosticOK, Detail: okDetail}
	}
	sort.Strings(findings)
	return Diagnostic{Kind: kind, Status: failure, Detail: strings.Join(findings, "; ")}
}

func duplicateIdentityFindings(plans []Record) []string {
	paths, branches := map[string]string{}, map[string]string{}
	var findings []string
	for _, record := range plans {
		if record.State == StateCleaned {
			continue
		}
		path := filepath.Clean(record.Path)
		if previous, ok := paths[path]; ok {
			findings = append(findings, previous+" and "+record.Role+" share path "+path)
		} else {
			paths[path] = record.Role
		}
		if previous, ok := branches[record.Branch]; ok {
			findings = append(findings, previous+" and "+record.Role+" share branch "+record.Branch)
		} else {
			branches[record.Branch] = record.Role
		}
	}
	return findings
}

func coordinationDiverged(set Set, worktree string) bool {
	worktree = filepath.Clean(worktree)
	for _, root := range []string{set.TeamHome, set.ControlRoot, set.AMQRoot} {
		if pathWithin(filepath.Clean(root), worktree) {
			return true
		}
	}
	for _, local := range []string{filepath.Join(worktree, ".agent-mail"), filepath.Join(worktree, ".amqrc"), filepath.Join(worktree, team.DirName, DirName)} {
		if pathExists(local) {
			return true
		}
	}
	return false
}

func pathWithin(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func findRegistrations(records []RegisteredWorktree, path, branch string) (*RegisteredWorktree, *RegisteredWorktree) {
	var atPath, branchAt *RegisteredWorktree
	for i := range records {
		if filepath.Clean(records[i].Path) == filepath.Clean(path) {
			atPath = &records[i]
		}
		if branch != "" && records[i].BranchRef == branchRef(branch) {
			branchAt = &records[i]
		}
	}
	return atPath, branchAt
}

func recordByRole(set Set, role string) (*Record, int) {
	for i := range set.Plans {
		if set.Plans[i].Role == strings.TrimSpace(role) {
			record := set.Plans[i]
			return &record, i
		}
	}
	return nil, -1
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func namePart(value string) string {
	value = strings.Trim(unsafeName.ReplaceAllString(strings.TrimSpace(value), "-"), "-.")
	if value == "" {
		return "unknown"
	}
	return value
}

func defaultBranch(profile, session, role, task string) string {
	return strings.Join([]string{"amq-squad", namePart(profile), namePart(session), namePart(role), namePart(task)}, "/")
}

func defaultPath(repo, profile, session, role string) string {
	name := filepath.Base(repo) + "-wt-" + namePart(profile) + "-" + namePart(session) + "-" + namePart(role)
	return filepath.Join(filepath.Dir(repo), name)
}

func effectiveHandle(member team.Member) string {
	if strings.TrimSpace(member.Handle) != "" {
		return member.Handle
	}
	return member.Role
}

func normalizedProfile(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return team.DefaultProfile
	}
	return strings.TrimSpace(profile)
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func ternary(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}
