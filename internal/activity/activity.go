// Package activity owns amq-squad's per-agent activity heartbeat file.
package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SchemaVersion = 1
	Filename      = "activity.json"
	FilePerm      = 0o600
	DirPerm       = 0o700

	StateFresh   = "fresh"
	StateStale   = "stale"
	StateUnknown = "unknown"

	SourceHeartbeat = "heartbeat-file"
	SourceSymphony  = "symphony-hook"
	SourceTaskStore = "task-store"
	SourceUnknown   = "unknown"
)

// DefaultStaleAfter is the freshness window for a written activity heartbeat.
// It intentionally matches the coordination heartbeat window: after 90 seconds
// without a new write, the signal is still visible but no longer implies active
// progress.
const DefaultStaleAfter = 90 * time.Second

// File is the on-disk JSON schema written by agents under
// <amq-root>/agents/<handle>/activity.json.
type File struct {
	Schema    int       `json:"schema"`
	Handle    string    `json:"handle"`
	TaskID    string    `json:"task_id,omitempty"`
	Phase     string    `json:"phase,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	WrittenAt time.Time `json:"written_at"`
}

// Snapshot is the normalized read model surfaced by status/console. Source and
// Quality are explicit so consumers never confuse task-store ownership with a
// fresh agent-written heartbeat.
type Snapshot struct {
	Source    string    `json:"source"`
	Quality   string    `json:"quality"`
	Handle    string    `json:"handle,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	Phase     string    `json:"phase,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	WrittenAt time.Time `json:"written_at,omitempty"`
	Stale     bool      `json:"stale,omitempty"`
}

func Path(agentDir string) string {
	return filepath.Join(agentDir, Filename)
}

func Normalize(file File, now time.Time, staleAfter time.Duration) Snapshot {
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	writtenAt := file.WrittenAt
	quality := StateUnknown
	stale := true
	if !writtenAt.IsZero() {
		stale = now.Sub(writtenAt) > staleAfter
		if stale {
			quality = StateStale
		} else {
			quality = StateFresh
		}
	}
	return Snapshot{
		Source:    SourceHeartbeat,
		Quality:   quality,
		Handle:    strings.TrimSpace(file.Handle),
		TaskID:    strings.TrimSpace(file.TaskID),
		Phase:     strings.TrimSpace(file.Phase),
		Detail:    strings.TrimSpace(file.Detail),
		WrittenAt: writtenAt,
		Stale:     stale,
	}
}

func TaskStoreSnapshot(handle, taskID, detail string, updatedAt, now time.Time, staleAfter time.Duration) Snapshot {
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stale := true
	quality := StateUnknown
	if !updatedAt.IsZero() {
		stale = now.Sub(updatedAt) > staleAfter
		if stale {
			quality = StateStale
		}
	}
	return Snapshot{
		Source:    SourceTaskStore,
		Quality:   quality,
		Handle:    strings.TrimSpace(handle),
		TaskID:    strings.TrimSpace(taskID),
		Phase:     "task_in_progress",
		Detail:    strings.TrimSpace(detail),
		WrittenAt: updatedAt,
		Stale:     stale,
	}
}

func SymphonySnapshot(handle, event, taskID, detail string, observedAt, now time.Time, staleAfter time.Duration) Snapshot {
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	event = strings.TrimSpace(event)
	stale := true
	quality := StateUnknown
	if !observedAt.IsZero() && symphonyEventClaimsActivity(event) {
		stale = now.Sub(observedAt) > staleAfter
		if stale {
			quality = StateStale
		} else {
			quality = StateFresh
		}
	}
	return Snapshot{
		Source:    SourceSymphony,
		Quality:   quality,
		Handle:    strings.TrimSpace(handle),
		TaskID:    strings.TrimSpace(taskID),
		Phase:     "symphony_" + event,
		Detail:    strings.TrimSpace(detail),
		WrittenAt: observedAt,
		Stale:     stale,
	}
}

func symphonyEventClaimsActivity(event string) bool {
	switch event {
	case "after_create", "before_remove":
		return false
	default:
		return true
	}
}

func UnknownSnapshot(handle, detail string) Snapshot {
	return Snapshot{
		Source:  SourceUnknown,
		Quality: StateUnknown,
		Handle:  strings.TrimSpace(handle),
		Detail:  strings.TrimSpace(detail),
		Stale:   true,
	}
}

func Read(agentDir string, now time.Time, staleAfter time.Duration) (Snapshot, bool, error) {
	data, err := os.ReadFile(Path(agentDir))
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, false, nil
		}
		return Snapshot{}, false, err
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return Snapshot{}, false, err
	}
	if file.Schema != 0 && file.Schema != SchemaVersion {
		return Snapshot{}, false, fmt.Errorf("unsupported activity schema %d", file.Schema)
	}
	return Normalize(file, now, staleAfter), true, nil
}

func Write(agentDir string, file File) error {
	file.Schema = SchemaVersion
	file.Handle = strings.TrimSpace(file.Handle)
	file.TaskID = strings.TrimSpace(file.TaskID)
	file.Phase = strings.TrimSpace(file.Phase)
	file.Detail = strings.TrimSpace(file.Detail)
	if file.Handle == "" {
		return fmt.Errorf("activity handle is required")
	}
	if file.WrittenAt.IsZero() {
		file.WrittenAt = time.Now().UTC()
	}
	return writeAtomic(Path(agentDir), file)
}

func Clear(agentDir string) error {
	err := os.Remove(Path(agentDir))
	if err == nil || os.IsNotExist(err) {
		if syncErr := syncDir(agentDir); syncErr != nil {
			return fmt.Errorf("sync activity dir: %w", syncErr)
		}
		return nil
	}
	return err
}

func writeAtomic(path string, file File) error {
	if err := os.MkdirAll(filepath.Dir(path), DirPerm); err != nil {
		return fmt.Errorf("ensure activity dir: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal activity: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-activity-*.json")
	if err != nil {
		return fmt.Errorf("create activity temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(FilePerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod activity temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write activity temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync activity temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close activity temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit activity: %w", err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync activity dir: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return f.Sync()
}
