package task

import "time"

// doneForTest completes a task the way the cut DoneForProfile wrapper did:
// DoneAtomicForProfile with the default completion notice.
func doneForTest(projectDir, profile, session, id, actor, evidence string, now time.Time) (Task, error) {
	result, err := DoneAtomicForProfile(projectDir, profile, session, id, DoneOptions{Actor: actor, Evidence: evidence, Notify: true, Now: now})
	return result.Task, err
}
