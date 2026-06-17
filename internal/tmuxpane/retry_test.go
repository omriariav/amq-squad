package tmuxpane

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIsPermissionDenied(t *testing.T) {
	if IsPermissionDenied(nil) {
		t.Error("nil is not a permission denial")
	}
	if IsPermissionDenied(errors.New("exit status 1")) {
		t.Error("a generic exit is not a permission denial")
	}
	if !IsPermissionDenied(errors.New("error connecting to /tmp/tmux-1/default (Operation not permitted)")) {
		t.Error("the socket-denied signature must be detected")
	}
	if !IsPermissionDenied(errors.New("connect: permission denied")) {
		t.Error("a 'permission denied' phrasing must be detected")
	}
	if !IsPermissionDenied(fmt.Errorf("tmux list-panes: %w", errors.New("Operation not permitted"))) {
		t.Error("a wrapped permission denial must be detected")
	}
	// A server-not-running failure also reads "error connecting to <socket>",
	// but it is NOT a permission denial — it must not be classified as sandbox.
	if IsPermissionDenied(errors.New("error connecting to /tmp/tmux-1/default (No such file or directory)")) {
		t.Error("a server-not-running error must NOT be classified as permission denial")
	}
	if IsPermissionDenied(errors.New("no server running on /tmp/tmux-1/default")) {
		t.Error("a no-server error must NOT be classified as permission denial")
	}
}

func TestInspectPaneByIDFailsFastOnPermissionDenied(t *testing.T) {
	zeroReadBackoff(t)
	calls := 0
	prev := captureExec
	captureExec = func(...string) (string, error) {
		calls++
		return "", errors.New("error connecting to /tmp/tmux/default (Operation not permitted)")
	}
	t.Cleanup(func() { captureExec = prev })

	if _, ok := InspectPaneByID("%5"); ok {
		t.Fatal("a permission denial must return false")
	}
	if calls != 1 {
		t.Fatalf("a permission denial must NOT retry; got %d attempts", calls)
	}
}

func TestDefaultPaneListerFailsFastOnPermissionDenied(t *testing.T) {
	zeroReadBackoff(t)
	calls := 0
	prev := listPanesExec
	listPanesExec = func() (string, error) {
		calls++
		return "", errors.New("Operation not permitted")
	}
	t.Cleanup(func() { listPanesExec = prev })

	_, err := DefaultPaneLister()
	if err == nil {
		t.Fatal("a permission denial must return an error")
	}
	if !IsPermissionDenied(err) {
		t.Errorf("the returned error must still read as permission-denied: %v", err)
	}
	if calls != 1 {
		t.Fatalf("a permission denial must NOT retry; got %d attempts", calls)
	}
}

// zeroReadBackoff makes the retry sleep a no-op for a test so the bounded-retry
// paths run instantly (no real wall-clock delay).
func zeroReadBackoff(t *testing.T) {
	t.Helper()
	prev := tmuxReadSleep
	tmuxReadSleep = func(time.Duration) {}
	t.Cleanup(func() { tmuxReadSleep = prev })
}

// A full paneListFormat row used across the retry tests.
const retryRow = "main\t0\t1\t1234\tcodex\t/repo\t%265\t@42\tamq:issue-96:cto\tdog\n"

func TestInspectPaneByIDRetriesThroughTransientFailure(t *testing.T) {
	zeroReadBackoff(t)
	calls := 0
	prev := captureExec
	captureExec = func(...string) (string, error) {
		calls++
		if calls < 2 {
			return "", errors.New("exit status 1") // -CC pause: transient
		}
		return retryRow, nil
	}
	t.Cleanup(func() { captureExec = prev })

	p, ok := InspectPaneByID("%265")
	if !ok || p.PaneID != "%265" {
		t.Fatalf("transient failure then success must resolve; ok=%v p=%+v", ok, p)
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts (fail then success), got %d", calls)
	}
}

func TestInspectPaneByIDGivesUpAtBound(t *testing.T) {
	zeroReadBackoff(t)
	calls := 0
	prev := captureExec
	captureExec = func(...string) (string, error) {
		calls++
		return "", errors.New("can't find pane %9") // genuinely gone: always fails
	}
	t.Cleanup(func() { captureExec = prev })

	if _, ok := InspectPaneByID("%9"); ok {
		t.Fatal("a genuinely-gone pane must return false")
	}
	if calls != tmuxReadAttempts {
		t.Fatalf("gone pane must fail at the retry bound %d, got %d attempts", tmuxReadAttempts, calls)
	}
}

func TestDefaultPaneListerRetriesThroughTransientFailure(t *testing.T) {
	zeroReadBackoff(t)
	calls := 0
	prev := listPanesExec
	listPanesExec = func() (string, error) {
		calls++
		if calls < tmuxReadAttempts {
			return "", errors.New("exit status 1")
		}
		return retryRow, nil
	}
	t.Cleanup(func() { listPanesExec = prev })

	panes, err := DefaultPaneLister()
	if err != nil {
		t.Fatalf("a transient scan failure then success must succeed: %v", err)
	}
	if len(panes) != 1 || panes[0].PaneID != "%265" {
		t.Fatalf("expected 1 parsed pane, got %+v", panes)
	}
	if calls != tmuxReadAttempts {
		t.Fatalf("expected %d attempts, got %d", tmuxReadAttempts, calls)
	}
}

func TestDefaultPaneListerGivesUpAtBound(t *testing.T) {
	zeroReadBackoff(t)
	calls := 0
	prev := listPanesExec
	listPanesExec = func() (string, error) {
		calls++
		return "", errors.New("no server running")
	}
	t.Cleanup(func() { listPanesExec = prev })

	if _, err := DefaultPaneLister(); err == nil {
		t.Fatal("a persistent scan failure must return an error")
	}
	if calls != tmuxReadAttempts {
		t.Fatalf("expected %d attempts, got %d", tmuxReadAttempts, calls)
	}
}

// An empty list with no error is a genuine "no panes", not a -CC stutter, so it
// must return immediately without burning the retry budget.
func TestDefaultPaneListerEmptyNoErrorDoesNotRetry(t *testing.T) {
	zeroReadBackoff(t)
	calls := 0
	prev := listPanesExec
	listPanesExec = func() (string, error) {
		calls++
		return "", nil
	}
	t.Cleanup(func() { listPanesExec = prev })

	panes, err := DefaultPaneLister()
	if err != nil || len(panes) != 0 {
		t.Fatalf("empty-no-error must return no panes and no error; got panes=%+v err=%v", panes, err)
	}
	if calls != 1 {
		t.Fatalf("empty-no-error must not retry; got %d calls", calls)
	}
}
