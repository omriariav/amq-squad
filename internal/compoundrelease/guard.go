package compoundrelease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
)

// InvocationGuard converts an inspected eligibility claim into one retained,
// exclusive callback lease. It is deliberately single-use; recursive Run and
// reuse after either success or failure are rejected.
type InvocationGuard struct {
	scope   SessionScope
	query   ResolveQuery
	claim   EligibilityClaim
	adapter InspectionAdapter
	state   atomic.Uint32
}

var invocationGuardFault = func(string) error { return nil }

func NewInvocationGuard(scope SessionScope, query ResolveQuery, claim EligibilityClaim, adapter InspectionAdapter) (*InvocationGuard, error) {
	if adapter == nil {
		return nil, fmt.Errorf("release inspection adapter is required")
	}
	if claim.Token == "" || eligibilityToken(claim) != claim.Token || claim.SeriesID == "" {
		return nil, fmt.Errorf("release eligibility claim is malformed")
	}
	return &InvocationGuard{scope: scope, query: query, claim: claim, adapter: adapter}, nil
}

// Run revalidates the full immutable claim while holding an exclusive lease on
// the exact verified store.lock descriptor, then retains that lease throughout
// fn. A callback panic is captured only long enough to release, then re-panics.
func (g *InvocationGuard) Run(fn func() error) (err error) {
	if g == nil || fn == nil {
		return fmt.Errorf("release invocation guard and callback are required")
	}
	if !g.state.CompareAndSwap(0, 1) {
		return fmt.Errorf("release invocation guard is single-use and non-nested")
	}
	defer g.state.Store(2)

	root, _, items, err := enumerateSessionSeries(g.scope)
	if root != nil {
		defer root.Close()
	}
	if err != nil {
		return err
	}
	defer closeEnumerated(items)
	var selected *enumeratedSeries
	for i := range items {
		if items[i].name == g.claim.SeriesID {
			selected = &items[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("claimed release series no longer exists")
	}
	lock, err := selected.store.openLeaf("store.lock", os.O_RDWR, 0, false)
	if err != nil {
		return err
	}
	artifact, err := readLockArtifact(lock)
	if err != nil {
		lock.Close()
		return err
	}
	lease, err := flock.AcquireExclusiveFile(lock)
	if err != nil {
		return err
	}
	released := false
	release := func() error {
		if released {
			return nil
		}
		released = true
		closeErr := lease.Close()
		faultErr := invocationGuardFault("after_release")
		if closeErr != nil {
			return closeErr
		}
		return faultErr
	}
	defer func() {
		if !released {
			err = errors.Join(err, release())
		}
	}()

	inspection, err := inspectSeriesLocked(*selected, artifact)
	if err != nil {
		return err
	}
	evidence := lockedEvidence{adapter: g.adapter}
	evidence.root, err = g.adapter.ResolveSessionRoot(inspection.Scope)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(evidence.root) || filepath.Clean(evidence.root) != evidence.root {
		return fmt.Errorf("resolved session root is not canonical absolute")
	}
	messages, warnings := g.adapter.ScanSessionMessages(evidence.root, timeNow)
	if len(warnings) != 0 {
		return fmt.Errorf("release mailbox scan produced %d warning(s)", len(warnings))
	}
	evidence.groups = groupReleaseMessages(messages)
	_, claims, _, _, err := resolveSeriesLocked(inspection, g.query, evidence)
	if err != nil {
		return err
	}
	if len(claims) != 1 || claims[0].Token != g.claim.Token || !reflect.DeepEqual(claims[0], g.claim) {
		return fmt.Errorf("release eligibility claim changed before invocation")
	}

	var callbackPanic any
	callbackErr := func() (callbackErr error) {
		defer func() { callbackPanic = recover() }()
		return fn()
	}()
	releaseErr := release()
	if callbackPanic != nil {
		panic(callbackPanic)
	}
	if callbackErr != nil {
		return errors.Join(callbackErr, releaseErr)
	}
	if releaseErr != nil {
		return releaseErr
	}
	return nil
}

var timeNow = func() time.Time { return time.Now() }
