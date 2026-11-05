//go:build unix

package seatcommit

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// uidInfo is a FileInfo whose Sys is a Stat_t with the given uid (the
// mismatch cannot be made on disk without root; the check is a function
// of the fstat result, so the result is built here).
type uidInfo struct {
	uid uint32
}

func (uidInfo) Name() string       { return "quarantine" }
func (uidInfo) Size() int64        { return 0 }
func (uidInfo) Mode() os.FileMode  { return os.ModeDir | 0o700 }
func (uidInfo) ModTime() time.Time { return time.Time{} }
func (uidInfo) IsDir() bool        { return true }
func (i uidInfo) Sys() any         { return &syscall.Stat_t{Uid: i.uid} }

// The owner check: a quarantine directory owned by another uid is refused
// by name; the caller's own uid passes.
func TestQuarantineDirectoryOwnedByAnotherUIDIsRefused(t *testing.T) {
	me := uint32(os.Getuid())
	if err := ownedByCaller("/q", uidInfo{uid: me}); err != nil {
		t.Fatalf("the caller's own directory: %v", err)
	}
	err := ownedByCaller("/q", uidInfo{uid: me + 1})
	if err == nil || !strings.Contains(err.Error(), "the quarantine directory /q is owned by uid") {
		t.Fatalf("another uid's directory: %v", err)
	}
}
