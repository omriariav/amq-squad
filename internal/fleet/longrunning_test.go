package fleet

import "testing"

// 2b-2 follow-up: the mayor marks its launch requests long-running only in
// the running operator session (Session); the one-shot CLI verb leaves the
// flag unset, so its launcher waits out the seats' proof window.
func TestLaunchRequestIsLongRunningOnlyInASession(t *testing.T) {
	for _, session := range []bool{true, false} {
		f := newMayor(t, fleetGrant(2, "owner/app"))
		f.m.Session = session
		mustStart(t, f.m, f.request(t, "app", false))
		if len(f.launcher.launched) != 1 || f.launcher.launched[0].LongRunning != session {
			t.Fatalf("session %v: launched %+v", session, f.launcher.launched)
		}
	}
}
