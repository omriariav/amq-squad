// Package leadsession is the one entrypoint of a lead session (plan 3.5,
// R39): a squad lead and the fleet's operator session are the same program
// with different grants. "This is a design constraint, not an option." Start
// takes one Config; the grant alone decides the scope. A squad grant
// (grants.Load with a repository root) carries no fleet grant and gives a
// squad lead; the owner's grant with a fleet grant gives the operator
// session, which in auto mode is the mayor. Nothing else in Config says
// which one it is, so there is no second program to drift.
//
// The lead and the mayor run on the built-in pi with the registry's named
// default model, on API keys (plan 14, 16.5; 4-3 ruling 1). The live run on
// the pi package is brief 4-1's; this package is the Go-level contract the
// pi lead starts from.
package leadsession

import (
	"errors"
	"slices"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Scope is what the grant lets the session run.
type Scope string

const (
	ScopeSquad Scope = "squad" // a squad lead
	ScopeFleet Scope = "fleet" // the operator session
)

// Program is the process a lead session runs: the same for every scope.
type Program struct {
	Runtime     string // "pi" (plan 16.5)
	Model       string // the registry's named default model
	Root        string // the AMQ session root it runs in
	ProjectPath string // its working directory
	Handle      string // its mailbox handle
}

// Config is everything a lead session starts from: the program and its grant.
type Config struct {
	Program Program
	Grant   grants.Resolved
}

// Session is a started lead session: its program, its scope and the
// amq-squad verbs its grant admits.
type Session struct {
	Program Program
	Grant   grants.Resolved
	Scope   Scope
	Mayor   bool // auto mode with a fleet grant: the mayor may start squads
	Verbs   []string
}

// LeadVerbs are the verbs every lead session runs (the squad's own loop).
var LeadVerbs = []string{"status", "task", "gate", "team", "context", "evidence"}

// FleetVerbs are added by a fleet grant: the fleet document and the
// operator inbox (manual mode).
var FleetVerbs = []string{"status --fleet", "operator inbox"}

// MayorVerbs are added by a fleet grant in auto mode. No verb answers a
// typed gate: only the owner does, with a signature (R41).
var MayorVerbs = []string{"operator start-squad"}

// Start is the entrypoint. The same Config with another grant gives the
// other scope and the same Program.
func Start(c Config) (Session, error) {
	if c.Program.Runtime == "" || c.Program.Root == "" || c.Program.Handle == "" {
		return Session{}, errors.New("lead session needs a runtime, a root and a handle")
	}
	s := Session{Program: c.Program, Grant: c.Grant, Scope: ScopeSquad, Verbs: slices.Clone(LeadVerbs)}
	if c.Grant.FleetScope() {
		s.Scope = ScopeFleet
		s.Verbs = append(s.Verbs, FleetVerbs...)
		if c.Grant.Mode == grants.ModeAuto {
			s.Mayor = true
			s.Verbs = append(s.Verbs, MayorVerbs...)
		}
	}
	return s, nil
}

// Admits reports whether the session's grant admits a verb.
func (s Session) Admits(verb string) bool { return slices.Contains(s.Verbs, verb) }
