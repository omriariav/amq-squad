package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// The journal commit protocol (docs/task-store-design.md, kept from v2):
// write and fsync the journal temp file, rename it to the journal, fsync the
// directory (the commit point), write, rename and fsync each after-image,
// remove the journal, fsync the directory. A locked reader or writer replays
// a committed journal first; a lock-free reader overlays it (Snapshot).

const (
	journalName = ".transaction.json"
	journalTmp  = ".transaction.json.tmp"
)

// Fault phases for the crash tests.
const (
	phaseBeforeCommit = "before_commit"
	phaseAfterCommit  = "after_commit"
	phaseMidApply     = "mid_apply"
)

// fault is a deterministic crash seam; nil in production.
var fault func(phase string) error

type journal struct {
	Schema      int       `json:"schema"`
	ID          string    `json:"id"`
	CommittedAt time.Time `json:"committed_at"`
	Tasks       []Task    `json:"tasks"`
}

// CommittedError means the commit point was crossed but applying the
// after-images failed; the next locked access replays the journal.
type CommittedError struct {
	ID    string
	Cause error
}

func (e *CommittedError) Error() string {
	return fmt.Sprintf("task transaction %s committed but needs replay: %v", e.ID, e.Cause)
}

func (e *CommittedError) Unwrap() error { return e.Cause }

func commit(dir string, changed []Task, now time.Time) error {
	if len(changed) == 0 {
		return nil
	}
	sortTasks(changed)
	ids := make([]string, len(changed))
	for i, t := range changed {
		ids[i] = t.ID
	}
	j := journal{Schema: Schema, ID: fmt.Sprintf("txn-%d-%s", now.UnixNano(), strings.Join(ids, "-")), CommittedAt: now, Tasks: changed}
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task transaction: %w", err)
	}
	if err := writeSynced(filepath.Join(dir, journalTmp), raw); err != nil {
		return fmt.Errorf("write task journal: %w", err)
	}
	if err := callFault(phaseBeforeCommit); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dir, journalTmp), filepath.Join(dir, journalName)); err != nil {
		return fmt.Errorf("publish task journal: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync task journal: %w", err)
	}
	if err := callFault(phaseAfterCommit); err != nil {
		return &CommittedError{ID: j.ID, Cause: err}
	}
	if err := apply(dir, j, true); err != nil {
		return &CommittedError{ID: j.ID, Cause: err}
	}
	return nil
}

func recoverJournal(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, journalName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read task journal: %w", err)
	}
	var j journal
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("parse committed task journal: %w", err)
	}
	if j.ID == "" || len(j.Tasks) == 0 {
		return fmt.Errorf("invalid committed task journal")
	}
	return apply(dir, j, false)
}

func apply(dir string, j journal, faults bool) error {
	for i, t := range j.Tasks {
		raw, err := json.MarshalIndent(t, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal task %s: %w", t.ID, err)
		}
		path := filepath.Join(dir, t.ID+".json")
		if err := writeSynced(path+".tmp", raw); err != nil {
			return fmt.Errorf("write task %s: %w", t.ID, err)
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			return fmt.Errorf("publish task %s: %w", t.ID, err)
		}
		if faults && i < len(j.Tasks)-1 {
			if err := callFault(phaseMidApply); err != nil {
				return err
			}
		}
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync task files: %w", err)
	}
	// The generation moves before the journal goes, so a lock-free reader
	// that saw neither the journal nor the new generation saw no part of this
	// transaction (Snapshot).
	if err := bumpGeneration(dir); err != nil {
		return fmt.Errorf("bump task store generation: %w", err)
	}
	if err := os.Remove(filepath.Join(dir, journalName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove task journal: %w", err)
	}
	return syncDir(dir)
}

func callFault(phase string) error {
	if fault == nil {
		return nil
	}
	if err := fault(phase); err != nil {
		return fmt.Errorf("injected task transaction failure at %s: %w", phase, err)
	}
	return nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir makes renames durable where directory fsync exists (Unix).
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}
