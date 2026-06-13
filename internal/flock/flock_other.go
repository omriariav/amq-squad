//go:build !unix

package flock

import "os"

// Non-unix fallback: no advisory lock available, so concurrent
// read-modify-write cycles are NOT serialized and can race (TOCTOU) — two
// processes may both read, both write, and the last writer wins, silently
// losing the other's update. The target file stays structurally valid (the
// writer's atomic rename guarantees that), but its content may reflect only
// one of the concurrent writers. amq-squad targets unix; this platform is
// unsuitable for concurrent team mutations.
func lockExclusive(_ *os.File) error { return nil }

func unlock(_ *os.File) {}
