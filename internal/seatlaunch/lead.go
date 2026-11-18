package seatlaunch

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// StageLead stages the squad lead's own launch record (3.3.1): C5a made
// every trust decision (task show's lead role, the owner's phone routing to
// a lead) read the record the core staged under
// <root>/extensions/amq-squad/launch/<handle>/, and only a roster launch
// staged one, so the lead `up` starts had none. The lead's start (`lead
// --team`, and the start of a confirmed team in the solo lead's process)
// calls it after taking the root's lead lock, through the same staging as a
// seat's (stageOne), in the same core-owned directory no seat can write.
//
// The record is the lead's, not a seat's: squad.yaml's lead seat's handle,
// runtime and role id, the session, a fresh launch id, the start, the lead
// process's pid, and SquadProject (pending; PromoteLead sets SquadLead once
// the start went on; launch.Record.LeadOnly). It names no
// worktree, branch, request, proof ticket or provider (the lead runs in the
// repository, outside the roster, whose seats squad.yaml's lead is never
// one of, and under no seat-exec), and no working directory, team home,
// rules or role digest, and no role.md is staged: the lead's role and rules
// are its start's own (AMQ_SQUAD_ROLE_PATH, AMQ_SQUAD_RULES_PATH), which
// `amq-squad context` falls back to while the staged directory holds no
// role, and a staged role or rules digest would replace or contradict them.
// A role.md an earlier seat launch of the handle left is removed, under the
// record's lock, with the record's replacement.
//
// launched: the lead runs under an AMQ launch of its own handle (AM_ME is
// the lead): its launch's staged record (a seat launch's, not LeadOnly) is
// kept whole and only marked (SquadProject, then SquadLead), so its launch id, proof and exec
// record stay that launch's.
//
// Every other handle's record that marks a squad lead is a former lead's:
// a LeadOnly one is removed (it names no launch, and its role id must not
// keep lead privileges), a launch's is unmarked.
//
// q needs ProjectRoot, BaseRoot, Session and Seats (the lead's among them).
func StageLead(q Squad, lead string, pid int, now time.Time, launched bool) (launch.Record, error) {
	if !handlePattern.MatchString(lead) {
		return launch.Record{}, fmt.Errorf("lead %q is not a valid AMQ handle", lead)
	}
	if !filepath.IsAbs(q.ProjectRoot) || !filepath.IsAbs(q.BaseRoot) || !handlePattern.MatchString(q.Session) {
		return launch.Record{}, fmt.Errorf("the squad's project root %q, base root %q and session %q do not name a session root", q.ProjectRoot, q.BaseRoot, q.Session)
	}
	var seat *Seat
	for i := range q.Seats {
		if q.Seats[i].Handle == lead {
			seat = &q.Seats[i]
		}
	}
	if seat == nil {
		return launch.Record{}, fmt.Errorf("squad.yaml's lead %s is not one of its seats", lead)
	}
	root := q.SessionRoot()
	if err := retireOtherLeads(root, lead); err != nil {
		return launch.Record{}, err
	}
	dir := launch.StagedDir(root, lead)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return launch.Record{}, err
	}
	path := filepath.Join(dir, launch.FileName)
	var out launch.Record
	err := withStagedLock(path, func() error {
		if launched {
			if cur, err := launch.ReadStaged(root, lead); err == nil && !cur.LeadOnly() {
				cur.SquadLead, cur.SquadProject = false, q.ProjectRoot // pending: PromoteLead
				out = cur
				return launch.WriteFile(path, cur)
			}
		}
		if err := os.Remove(filepath.Join(dir, launch.RoleFileName)); err != nil && !os.IsNotExist(err) {
			return err
		}
		out = launch.Record{
			Schema: launch.SchemaVersion, Handle: lead, Root: root, BaseRoot: q.BaseRoot, Session: q.Session,
			Runtime: seat.Runtime, Role: seat.RoleName, TeamProfile: q.Profile,
			LaunchID: newLaunchID(), StartedAt: now.UTC(), AgentPID: pid, SquadProject: q.ProjectRoot, // pending: PromoteLead
		}
		return launch.WriteFile(path, out)
	})
	if err != nil {
		return launch.Record{}, err
	}
	return out, nil
}

// PromoteLead gives a lead start's staged record lead authority
// (SquadLead) once the start went on: the lead's pi started, or the
// confirmed team's roster came up. Until then the record is pending: a
// start killed after staging leaves no authority behind (3.3.1 round 2). A
// record of another launch id is left as it is.
func PromoteLead(root, lead, launchID string) error {
	if !handlePattern.MatchString(lead) || launchID == "" {
		return fmt.Errorf("lead %q, launch %q: nothing to promote", lead, launchID)
	}
	path := launch.StagedPath(root, lead)
	return withStagedLock(path, func() error {
		rec, err := launch.ReadStaged(root, lead)
		if err != nil {
			return err
		}
		if rec.LaunchID != launchID || rec.SquadProject == "" {
			return fmt.Errorf("%s's staged record is no longer this start's (launch %s)", lead, rec.LaunchID)
		}
		rec.SquadLead = true
		return launch.WriteFile(path, rec)
	})
}

// UnstageLead undoes a lead start's staging that did not go on (its
// scheduler, runtime, spawn or roster failed, or the start was closed): the
// record is removed when it is still that start's lead-only record, or
// unmarked when it is a launched lead's record that start marked; a record
// a later start or launch staged (another launch id) is left as it is.
func UnstageLead(root, lead, launchID string) error {
	if !handlePattern.MatchString(lead) || launchID == "" {
		return fmt.Errorf("lead %q, launch %q: nothing to unstage", lead, launchID)
	}
	path := launch.StagedPath(root, lead)
	return withStagedLock(path, func() error {
		rec, err := launch.ReadStaged(root, lead)
		switch {
		case os.IsNotExist(err):
			return nil
		case err != nil:
			return err
		case rec.LaunchID != launchID || rec.SquadProject == "":
			return nil
		case rec.LeadOnly():
			return os.Remove(path)
		default:
			rec.SquadLead, rec.SquadProject = false, ""
			return launch.WriteFile(path, rec)
		}
	})
}

// retireOtherLeads removes every other handle's LeadOnly record and clears
// SquadLead on a launch's record (a lead squad.yaml named before), each
// under its record lock.
func retireOtherLeads(root, lead string) error {
	dirs, err := os.ReadDir(filepath.Dir(launch.StagedDir(root, lead)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, d := range dirs {
		if !d.IsDir() || d.Name() == lead || !handlePattern.MatchString(d.Name()) {
			continue
		}
		h := d.Name()
		path := launch.StagedPath(root, h)
		if err := withStagedLock(path, func() error {
			rec, err := launch.ReadStaged(root, h)
			switch {
			case err != nil || rec.SquadProject == "":
				return nil
			case rec.LeadOnly():
				return os.Remove(path)
			default:
				rec.SquadLead, rec.SquadProject = false, ""
				return launch.WriteFile(path, rec)
			}
		}); err != nil {
			return fmt.Errorf("retire %s's lead record: %w", h, err)
		}
	}
	return nil
}
