package routing

import (
	"errors"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// ReasonInvalid is the reason code of a routing table that does not load: a
// routing.yaml outside the strict subset, an unknown key, or a broken table
// invariant.
const ReasonInvalid = "routing_invalid"

// InvalidError is a table that does not load. The scheduler process refuses
// to start on it with ReasonInvalid; it never falls back to the default
// router (lead ruling 2026-10-24).
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return ReasonInvalid + ": " + e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

// IsInvalid reports whether err is a table that does not load.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// StartOptions are what a scheduler process has at start.
type StartOptions struct {
	Home     string // the owner's home (~/.amq-squad/routing.yaml)
	RepoRoot string // <repo root>/.amq-squad/routing.yaml; the path rules' root
	Root     string // the AMQ session root: outcome log, decisions, seed
	Registry registry.Registry
	Grants   grants.Grants
	Tasks    func() ([]task.Task, error) // task.Snapshot(store.Dir)
	Now      func() time.Time
}

// Open is the scheduler process's start step: it loads the effective table
// and builds the router. A table that does not load is an *InvalidError, and
// the process must refuse to start with ReasonInvalid.
func Open(o StartOptions) (*Router, error) {
	tbl, err := Load(o.Home, o.RepoRoot)
	if err != nil {
		return nil, &InvalidError{Err: err}
	}
	return New(Options{Table: tbl, Registry: o.Registry, Grants: o.Grants, Root: o.Root, RepoRoot: o.RepoRoot,
		Tasks: o.Tasks, Now: o.Now})
}
