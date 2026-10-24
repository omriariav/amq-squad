package fleet

import "context"

// Rollback and Close on the test fakes (SquadWriter and RosterLauncher
// gained them with the Codex re-pass of d02dd2fc).

func (w *fakeWriter) Rollback(projectPath, _ string) error {
	w.rolledBack = append(w.rolledBack, projectPath)
	return nil
}

func (l *fakeLauncher) Close(_ context.Context, req LaunchRequest) error {
	l.closed = append(l.closed, req)
	return nil
}

func (w slowWriter) Rollback(string, string) error                 { return nil }
func (l *slowLauncher) Close(context.Context, LaunchRequest) error { return nil }
func (l *blockingLauncher) Close(context.Context, LaunchRequest) error {
	return nil
}
func (partialLauncher) Close(context.Context, LaunchRequest) error { return nil }
func (l slowSeats) Close(context.Context, LaunchRequest) error     { return nil }
