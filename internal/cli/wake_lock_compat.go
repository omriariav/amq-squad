package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// wakeLockFile is the subset of AMQ 0.60's wake-lock wire format used by
// amq-squad. Generation values stay raw because AMQ owns their representation;
// amq-squad only verifies that the state binding repeats the same value.
type wakeLockFile struct {
	PID             int             `json:"pid"`
	TTY             string          `json:"tty,omitempty"`
	Root            string          `json:"root,omitempty"`
	Agent           string          `json:"agent,omitempty"`
	Started         time.Time       `json:"started"`
	WakeMode        string          `json:"wake_mode,omitempty"`
	TargetDigest    string          `json:"target_digest,omitempty"`
	Generation      json.RawMessage `json:"generation,omitempty"`
	StateGeneration json.RawMessage `json:"state_generation,omitempty"`
	StateDigest     string          `json:"state_digest,omitempty"`
	Owner           json.RawMessage `json:"owner,omitempty"`
	ResumeOwner     json.RawMessage `json:"resume_owner,omitempty"`
	// MachineID and Hostname are AMQ's same-machine evidence (AMQ 0.60.4+,
	// #488): a lock naming a different or unverifiable machine means local
	// PID inspection says nothing about the actual writer. amq-squad does
	// not reimplement AMQ's own platform-identity reader to compute "our"
	// machine_id, so any recorded MachineID makes wakeWriterDead defer
	// rather than conclude dead; Hostname is the legacy last-resort signal
	// AMQ itself uses when no MachineID is present.
	MachineID string `json:"machine_id,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
}

var knownWakeLockKeys = map[string]struct{}{
	"pid": {}, "tty": {}, "root": {}, "agent": {}, "hostname": {}, "machine_id": {},
	"started": {}, "process_start": {}, "boot_id": {}, "executable": {},
	"args": {}, "image_path": {}, "image_version": {}, "wake_mode": {},
	"target_digest": {}, "generation": {}, "state_generation": {},
	"state_digest": {}, "source_generation": {}, "source_floor_digest": {},
	"control_socket": {}, "owner_schema": {}, "owner": {}, "resume_schema": {},
	"resume_owner": {}, "resume_signal": {}, "running_image_evidence": {},
}

// decodeWakeLockFile mirrors AMQ 0.53+'s fail-closed lock reader for the known
// fields amq-squad consumes. encoding/json normally accepts duplicate and
// case-folded keys; doing that for lifecycle state would let an ambiguous file
// drive local mutation, so known duplicates and nulls are rejected first.
func decodeWakeLockFile(raw []byte) (wakeLockFile, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil {
		return wakeLockFile{}, fmt.Errorf("decode wake lock: %w", err)
	}
	if delim, ok := first.(json.Delim); !ok || delim != '{' {
		return wakeLockFile{}, fmt.Errorf("decode wake lock: top-level value must be an object")
	}
	seen := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return wakeLockFile{}, fmt.Errorf("decode wake lock key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return wakeLockFile{}, fmt.Errorf("decode wake lock: object key is not a string")
		}
		folded := strings.ToLower(key)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return wakeLockFile{}, fmt.Errorf("decode wake lock field %q: %w", key, err)
		}
		if _, known := knownWakeLockKeys[folded]; !known {
			continue
		}
		if previous, duplicate := seen[folded]; duplicate {
			return wakeLockFile{}, fmt.Errorf("decode wake lock: duplicate known field %q conflicts with %q", key, previous)
		}
		seen[folded] = key
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return wakeLockFile{}, fmt.Errorf("decode wake lock: known field %q must not be null", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return wakeLockFile{}, fmt.Errorf("decode wake lock object: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return wakeLockFile{}, fmt.Errorf("decode wake lock trailer: %w", err)
		}
		return wakeLockFile{}, fmt.Errorf("decode wake lock: unexpected trailing value %v", token)
	}

	var lock wakeLockFile
	if err := json.Unmarshal(raw, &lock); err != nil {
		return wakeLockFile{}, fmt.Errorf("decode wake lock fields: %w", err)
	}
	if err := validateWakeLockStateBinding(lock); err != nil {
		return wakeLockFile{}, err
	}
	return lock, nil
}

func validateWakeLockStateBinding(lock wakeLockFile) error {
	stateGenerationPresent := len(bytes.TrimSpace(lock.StateGeneration)) > 0
	stateDigestPresent := strings.TrimSpace(lock.StateDigest) != ""
	if !stateGenerationPresent && !stateDigestPresent {
		return nil
	}
	if !stateGenerationPresent || !stateDigestPresent {
		return fmt.Errorf("decode wake lock: incomplete state binding")
	}
	if len(bytes.TrimSpace(lock.Generation)) == 0 || !bytes.Equal(bytes.TrimSpace(lock.StateGeneration), bytes.TrimSpace(lock.Generation)) {
		return fmt.Errorf("decode wake lock: state_generation does not match generation")
	}
	if strings.TrimSpace(lock.TargetDigest) == "" || lock.StateDigest != lock.TargetDigest {
		return fmt.Errorf("decode wake lock: state_digest does not match target_digest")
	}
	switch strings.ToLower(strings.TrimSpace(lock.WakeMode)) {
	case "inject-via", "inject-via-v1", "owner-inject-via-v1":
		return nil
	default:
		return fmt.Errorf("decode wake lock: state binding is invalid for wake_mode %q", lock.WakeMode)
	}
}

// currentWakeLockHostname is a seam so tests can pin "this machine's"
// hostname deterministically instead of depending on the real host.
var currentWakeLockHostname = os.Hostname

// wakeLockSameMachine reports whether lock was written on this machine, per
// AMQ 0.60.4+'s same-machine evidence (#488). It never reads or reimplements
// AMQ's own platform-identity source (IOPlatformUUID / /etc/machine-id /
// boot session id) -- amq-squad only knows its own hostname. A MachineID on
// the lock is therefore always "unverified" here: any recorded MachineID
// means the caller must not draw a local dead-writer conclusion from PID
// state, since amq-squad cannot corroborate it. A legacy lock with no
// MachineID falls back to a plain hostname comparison, matching AMQ's own
// last-resort tier.
func wakeLockSameMachine(lock wakeLockFile) bool {
	if strings.TrimSpace(lock.MachineID) != "" {
		return false
	}
	if strings.TrimSpace(lock.Hostname) == "" {
		return true
	}
	current, err := currentWakeLockHostname()
	if err != nil || current == "" {
		return false
	}
	return lock.Hostname == current
}

func wakeLockHasStateBinding(lock wakeLockFile) bool {
	return len(bytes.TrimSpace(lock.StateGeneration)) > 0 && strings.TrimSpace(lock.StateDigest) != ""
}

func wakeLockHasOwnerBinding(lock wakeLockFile) bool {
	for _, raw := range []json.RawMessage{lock.Owner, lock.ResumeOwner} {
		owner := bytes.TrimSpace(raw)
		if len(owner) > 0 && !bytes.Equal(owner, []byte("{}")) {
			return true
		}
	}
	return false
}
