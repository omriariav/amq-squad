package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	transactionJournalName = ".transaction.json"
	transactionJournalTmp  = ".transaction.json.tmp"

	transactionPhaseBeforeJournalRename = "before_journal_rename"
	transactionPhaseAfterJournalCommit  = "after_journal_commit"
	transactionPhaseMidApply            = "mid_apply"
	transactionPhaseAfterAllApply       = "after_all_apply"
	transactionPhaseBeforeJournalRemove = "before_journal_remove"
	transactionPhaseAfterJournalRemove  = "after_journal_remove"
)

// transactionFault is a deterministic crash/failure seam. Production leaves
// it nil. Tests inject failures at the named durable boundaries above.
var transactionFault func(phase string, index int) error

type transactionJournal struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	CommittedAt   time.Time `json:"committed_at"`
	Tasks         []Task    `json:"tasks"`
}

// CommittedTransactionError means the journal publication commit point was
// crossed, but applying or clearing its after-images failed. The next official
// read/mutation replays the committed journal under the same store lock.
type CommittedTransactionError struct {
	TransactionID string
	Cause         error
}

func (e *CommittedTransactionError) Error() string {
	return fmt.Sprintf("task transaction %s committed but needs recovery: %v", e.TransactionID, e.Cause)
}

func (e *CommittedTransactionError) Unwrap() error { return e.Cause }

func commitTasks(dir string, changed []Task, now time.Time) error {
	if len(changed) == 0 {
		return nil
	}
	changed = append([]Task(nil), changed...)
	sortTasks(changed)
	j := transactionJournal{
		SchemaVersion: 1,
		ID:            transactionID(changed, now),
		CommittedAt:   now,
		Tasks:         changed,
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task transaction: %w", err)
	}
	tmp := filepath.Join(dir, transactionJournalTmp)
	journal := filepath.Join(dir, transactionJournalName)
	if err := writeSyncedFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write task transaction journal: %w", err)
	}
	if err := callTransactionFault(transactionPhaseBeforeJournalRename, -1); err != nil {
		return err
	}
	if err := os.Rename(tmp, journal); err != nil {
		return fmt.Errorf("publish task transaction journal: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync published task transaction journal: %w", err)
	}
	if err := callTransactionFault(transactionPhaseAfterJournalCommit, -1); err != nil {
		return &CommittedTransactionError{TransactionID: j.ID, Cause: err}
	}
	if err := applyCommittedJournal(dir, j, true); err != nil {
		return &CommittedTransactionError{TransactionID: j.ID, Cause: err}
	}
	return nil
}

func transactionID(tasks []Task, now time.Time) string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	return fmt.Sprintf("txn-%d-%s", now.UnixNano(), strings.Join(ids, "-"))
}

func recoverCommittedTransaction(dir string) (string, error) {
	path := filepath.Join(dir, transactionJournalName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read committed task transaction: %w", err)
	}
	var j transactionJournal
	if err := json.Unmarshal(b, &j); err != nil {
		return "", fmt.Errorf("parse committed task transaction: %w", err)
	}
	if j.SchemaVersion != 1 || strings.TrimSpace(j.ID) == "" || len(j.Tasks) == 0 {
		return "", fmt.Errorf("invalid committed task transaction journal")
	}
	if err := applyCommittedJournal(dir, j, false); err != nil {
		return j.ID, err
	}
	return j.ID, nil
}

func applyCommittedJournal(dir string, j transactionJournal, injectFaults bool) error {
	for i, t := range j.Tasks {
		if err := writeTaskAfterImage(dir, t); err != nil {
			return err
		}
		if injectFaults && i < len(j.Tasks)-1 {
			if err := callTransactionFault(transactionPhaseMidApply, i); err != nil {
				return err
			}
		}
	}
	if injectFaults {
		if err := callTransactionFault(transactionPhaseAfterAllApply, len(j.Tasks)); err != nil {
			return err
		}
		if err := callTransactionFault(transactionPhaseBeforeJournalRemove, len(j.Tasks)); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, transactionJournalName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove committed task transaction journal: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync cleared task transaction journal: %w", err)
	}
	if injectFaults {
		if err := callTransactionFault(transactionPhaseAfterJournalRemove, len(j.Tasks)); err != nil {
			return err
		}
	}
	return nil
}

func callTransactionFault(phase string, index int) error {
	if transactionFault == nil {
		return nil
	}
	if err := transactionFault(phase, index); err != nil {
		return fmt.Errorf("injected task transaction failure at %s: %w", phase, err)
	}
	return nil
}

func writeTaskAfterImage(dir string, t Task) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	path := filepath.Join(dir, t.ID+".json")
	tmp := path + ".tmp"
	if err := writeSyncedFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write task after-image: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("publish task after-image: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync task after-image: %w", err)
	}
	return nil
}

func writeSyncedFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
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

// syncDirectory makes rename/remove durability explicit on platforms that
// support directory fsync (Unix, including Linux and macOS). Windows does not
// expose the same portable contract through os.File.Sync, so it is a documented
// best-effort no-op there. EINVAL/ENOTSUP are likewise treated as unsupported,
// not as evidence that the file contents themselves failed to sync.
func syncDirectory(dir string) error {
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
