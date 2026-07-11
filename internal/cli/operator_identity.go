package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type verifiedOperatorActor struct {
	Role, Handle, Profile, Session, Root, PaneID string
}

var resolveVerifiedOperatorActor = defaultVerifiedOperatorActor
var resolveVerifiedCurrentPaneActor = defaultVerifiedCurrentPaneActor
var resolveOperatorActorBaseRoot = scanBaseRootForProject
var errNoVerifiedRosterPane = errors.New("current pane is not a verified runnable roster actor")

func defaultVerifiedOperatorActor(projectDir, profile, session, requiredRole, requiredHandle string) (verifiedOperatorActor, error) {
	ctx, err := resolveAMQContextForNamespace(projectDir, profile, session, requiredHandle)
	if err != nil {
		return verifiedOperatorActor{}, fmt.Errorf("resolve exact AMQ namespace: %w", err)
	}
	if strings.TrimSpace(os.Getenv("AM_ME")) != requiredHandle || !rootsMatch(os.Getenv("AM_ROOT"), ctx.Root) {
		return verifiedOperatorActor{}, fmt.Errorf("verified actor requires exact AM_ME=%s and AM_ROOT=%s", requiredHandle, ctx.Root)
	}
	paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if paneID == "" {
		return verifiedOperatorActor{}, fmt.Errorf("verified actor requires TMUX_PANE")
	}
	_, rec, ok := findLaunchRecordByPane(ctx.Root, paneID)
	profile = squadnamespace.NormalizeProfile(profile)
	if !ok {
		return verifiedOperatorActor{}, fmt.Errorf("current pane has no matching launch identity for %s/%s", requiredRole, requiredHandle)
	}
	cfg, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return verifiedOperatorActor{}, err
	}
	member, ok := operatorRosterMember(cfg, requiredRole, requiredHandle)
	if !ok {
		return verifiedOperatorActor{}, fmt.Errorf("actor is not a member of the target roster")
	}
	if err := validateOperatorLaunchRecord(rec, member, cfg.Project, requiredRole, requiredHandle, profile, session, ctx.Root, paneID); err != nil {
		return verifiedOperatorActor{}, err
	}
	return verifiedOperatorActor{Role: requiredRole, Handle: requiredHandle, Profile: profile, Session: session, Root: ctx.Root, PaneID: paneID}, nil
}

func validateOperatorLaunchRecord(rec launch.Record, member team.Member, projectDir, role, handle, profile, session, root, paneID string) error {
	if !launchRecordAuthorizesProjectLeadPane(rec, role, handle, profile, session, root, paneID) {
		return fmt.Errorf("launch record does not authorize this project actor/pane")
	}
	expectedCWD := member.EffectiveCWD(projectDir)
	if !sameFilesystemPath(rec.CWD, expectedCWD) || !currentWorkingDirMatches(expectedCWD) {
		return fmt.Errorf("launch record, current working directory, and roster member CWD do not match")
	}
	if rec.AgentPID <= 0 || !procinfo.Alive(rec.AgentPID) {
		return fmt.Errorf("matching launch record is not live")
	}
	return nil
}

func operatorRosterMember(cfg team.Team, role, handle string) (team.Member, bool) {
	for _, member := range cfg.Members {
		memberHandle := member.Handle
		if memberHandle == "" {
			memberHandle = member.Role
		}
		if member.Role == role && memberHandle == handle {
			return member, true
		}
	}
	return team.Member{}, false
}

func defaultVerifiedCurrentPaneActor(projectDir, profile, _ string, cfg team.Team) (verifiedOperatorActor, error) {
	paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if paneID == "" {
		return verifiedOperatorActor{}, errNoVerifiedRosterPane
	}
	profile = squadnamespace.NormalizeProfile(profile)
	base, err := resolveOperatorActorBaseRoot(projectDir)
	if err != nil {
		return verifiedOperatorActor{}, fmt.Errorf("resolve AMQ base root for actor discovery: %w", err)
	}
	entries, err := launch.ScanRestorableEntriesInRoot(projectDir, base)
	if err != nil {
		return verifiedOperatorActor{}, fmt.Errorf("scan AMQ launch records for actor discovery: %w", err)
	}
	for _, entry := range entries {
		rec := entry.Record
		if squadnamespace.NormalizeProfile(rec.TeamProfile) != profile || !launchRecordMatchesPane(rec, paneID) {
			continue
		}
		root := filepath.Dir(filepath.Dir(entry.AgentDir))
		profileBase := base
		if profile != team.DefaultProfile {
			profileBase = filepath.Join(base, profile)
		}
		actualSession := ""
		if !sameFilesystemPath(root, profileBase) {
			actualSession = filepath.Base(root)
		}
		if strings.TrimSpace(rec.Session) != actualSession || rec.AgentPID <= 0 || !procinfo.Alive(rec.AgentPID) {
			continue
		}
		for _, member := range cfg.Members {
			handle := member.Handle
			if handle == "" {
				handle = member.Role
			}
			if strings.TrimSpace(rec.Role) == member.Role && strings.TrimSpace(rec.Handle) == handle {
				return verifiedOperatorActor{Role: member.Role, Handle: handle, Profile: profile, Session: actualSession, Root: root, PaneID: paneID}, nil
			}
		}
	}
	return verifiedOperatorActor{}, errNoVerifiedRosterPane
}

func verifiedCurrentRosterActor(projectDir, profile, session string, cfg team.Team) (verifiedOperatorActor, error) {
	me := strings.TrimSpace(os.Getenv("AM_ME"))
	for _, member := range cfg.Members {
		handle := member.Handle
		if handle == "" {
			handle = member.Role
		}
		if handle == me {
			return resolveVerifiedOperatorActor(projectDir, profile, session, member.Role, handle)
		}
	}
	return verifiedOperatorActor{}, fmt.Errorf("AM_ME does not identify a roster member")
}
