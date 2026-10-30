package seatlaunch

import (
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// writeStaged writes a seat's staged launch record (Stage) under the
// record's sidecar lock, launch.json.lock, the one launch.UpdateStaged
// holds for its read-modify-write (3.0.1 t16, round 6). A restage of a
// handle (a later launch, a roster relaunch) therefore never lands between
// another launch's read and write: the proof server's compare-and-set on
// the launch id (markToken) reads, compares and writes under the same lock,
// so it either sees the restaged record and writes nothing, or finishes
// before the restage replaces it whole.
func writeStaged(path string, rec launch.Record) error {
	return flock.WithLock(path+".lock", func() error { return launch.WriteFile(path, rec) })
}
