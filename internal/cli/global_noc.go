package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
)

const (
	globalNOCRegistrySchema   = 1
	globalNOCBootstrapVersion = 1
	globalNOCRegistryDir      = "noc"
	globalNOCRegistryFile     = "registry.json"
	globalNOCLaunchPrepared   = "prepared"
	globalNOCLaunchActive     = "active"
	globalNOCLaunchFailed     = "failed"
	globalNOCLaunchStopped    = "stopped"

	globalNOCRunPlanned      = "registration_planned"
	globalNOCRunRegistered   = "wake_registered"
	globalNOCRunPollRequired = "poll_required"
	globalNOCRunOptOut       = "polling_opt_out"
)

type globalNOCBackstop struct {
	IntervalSeconds int `json:"interval_seconds"`
	TimeoutSeconds  int `json:"timeout_seconds"`
	MaxTicks        int `json:"max_ticks"`
}

func (b globalNOCBackstop) validate() error {
	if b.IntervalSeconds <= 0 || b.TimeoutSeconds <= 0 || b.MaxTicks <= 0 {
		return fmt.Errorf("NOC stall backstop must have positive interval, timeout, and max_ticks")
	}
	return nil
}

type globalNOCLaunch struct {
	ID               string            `json:"id"`
	Generation       uint64            `json:"generation"`
	State            string            `json:"state"`
	Record           launch.Record     `json:"launch_record"`
	BootstrapVersion int               `json:"bootstrap_version"`
	BootstrapDigest  string            `json:"bootstrap_digest"`
	Backstop         globalNOCBackstop `json:"stall_backstop"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Detail           string            `json:"detail,omitempty"`
}

type globalNOCRun struct {
	ID                   string                           `json:"id"`
	NOCLaunchID          string                           `json:"noc_launch_id"`
	NOCGeneration        uint64                           `json:"noc_generation"`
	Namespace            squadnamespace.Ref               `json:"namespace"`
	LeadRole             string                           `json:"lead_role"`
	Policy               string                           `json:"policy"`
	State                string                           `json:"state"`
	ExternalRegistration *launch.OrchestratorRegistration `json:"external_registration,omitempty"`
	CreatedAt            time.Time                        `json:"created_at"`
	UpdatedAt            time.Time                        `json:"updated_at"`
	Detail               string                           `json:"detail,omitempty"`
}

type globalNOCRegistry struct {
	SchemaVersion     int               `json:"schema_version"`
	ControlRoot       string            `json:"control_root"`
	CurrentGeneration uint64            `json:"current_generation"`
	UpdatedAt         time.Time         `json:"updated_at"`
	Launches          []globalNOCLaunch `json:"launches"`
	Runs              []globalNOCRun    `json:"runs,omitempty"`
}

func globalNOCRegistryPath(root string) string {
	return filepath.Join(root, ".amq-squad", globalNOCRegistryDir, globalNOCRegistryFile)
}

func canonicalGlobalNOCControlRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("NOC control root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve NOC control root: %w", err)
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve NOC control root identity: %w", err)
	}
	info, err := os.Lstat(evaluated)
	if err != nil {
		return "", fmt.Errorf("stat NOC control root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("NOC control root must be a non-symlink directory")
	}
	return filepath.Clean(evaluated), nil
}

func ensureGlobalNOCRegistryDirectory(root string) (string, error) {
	current := root
	for _, name := range []string{".amq-squad", globalNOCRegistryDir} {
		current = filepath.Join(current, name)
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("NOC registry component %s must be a non-symlink directory", current)
			}
		case os.IsNotExist(err):
			if err := os.Mkdir(current, 0o700); err != nil {
				return "", fmt.Errorf("create NOC registry directory %s: %w", current, err)
			}
		default:
			return "", fmt.Errorf("stat NOC registry directory %s: %w", current, err)
		}
	}
	return current, nil
}

func readGlobalNOCRegistry(root string) (globalNOCRegistry, error) {
	canonical, err := canonicalGlobalNOCControlRoot(root)
	if err != nil {
		return globalNOCRegistry{}, err
	}
	path := globalNOCRegistryPath(canonical)
	info, err := os.Lstat(path)
	if err != nil {
		return globalNOCRegistry{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return globalNOCRegistry{}, fmt.Errorf("NOC registry must be a non-symlink regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return globalNOCRegistry{}, err
	}
	var registry globalNOCRegistry
	if err := json.Unmarshal(body, &registry); err != nil {
		return globalNOCRegistry{}, fmt.Errorf("parse NOC registry: %w", err)
	}
	if err := validateGlobalNOCRegistry(registry, canonical); err != nil {
		return globalNOCRegistry{}, err
	}
	return registry, nil
}

func writeGlobalNOCRegistry(root string, mutate func(*globalNOCRegistry) error) error {
	canonical, err := canonicalGlobalNOCControlRoot(root)
	if err != nil {
		return err
	}
	dir, err := ensureGlobalNOCRegistryDirectory(canonical)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, globalNOCRegistryFile)
	lockPath := path + ".lock"
	if info, statErr := os.Lstat(lockPath); statErr == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("NOC registry lock must be a non-symlink regular file")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	return flock.WithLock(lockPath, func() error {
		registry := globalNOCRegistry{SchemaVersion: globalNOCRegistrySchema, ControlRoot: canonical}
		if existing, readErr := readGlobalNOCRegistry(canonical); readErr == nil {
			registry = existing
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		if err := mutate(&registry); err != nil {
			return err
		}
		if err := validateGlobalNOCRegistry(registry, canonical); err != nil {
			return err
		}
		body, err := json.MarshalIndent(registry, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal NOC registry: %w", err)
		}
		return atomicWriteJSONBytes(path, append(body, '\n'))
	})
}

func validateGlobalNOCRegistry(registry globalNOCRegistry, expectedRoot string) error {
	if registry.SchemaVersion != globalNOCRegistrySchema {
		return fmt.Errorf("unsupported NOC registry schema %d", registry.SchemaVersion)
	}
	// ControlRoot is canonicalized exactly once when the registry is created.
	// Treat the stored value as immutable authority bytes after that: resolving
	// it again would let a later symlink swap transfer authority to a different
	// directory.
	if registry.ControlRoot == "" || !filepath.IsAbs(registry.ControlRoot) ||
		registry.ControlRoot != filepath.Clean(registry.ControlRoot) ||
		registry.ControlRoot != expectedRoot {
		return fmt.Errorf("NOC registry control root mismatch")
	}
	if registry.CurrentGeneration != uint64(len(registry.Launches)) {
		return fmt.Errorf("NOC registry generation sequence is invalid")
	}
	for i, item := range registry.Launches {
		wantGeneration := uint64(i + 1)
		if item.Generation != wantGeneration || strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("NOC launch generation %d identity is invalid", wantGeneration)
		}
		switch item.State {
		case globalNOCLaunchPrepared, globalNOCLaunchActive, globalNOCLaunchFailed, globalNOCLaunchStopped:
		default:
			return fmt.Errorf("NOC launch generation %d has invalid state %q", wantGeneration, item.State)
		}
		if item.Record.Tmux == nil || strings.TrimSpace(item.Record.Tmux.PaneID) == "" ||
			strings.TrimSpace(item.Record.Session) == "" || strings.TrimSpace(item.Record.Role) == "" {
			return fmt.Errorf("NOC launch generation %d has incomplete stamped runtime identity", wantGeneration)
		}
		if item.Record.CWD != registry.ControlRoot {
			return fmt.Errorf("NOC launch generation %d control root binding mismatch", wantGeneration)
		}
		if item.Record.AgentPID <= 0 {
			return fmt.Errorf("NOC launch generation %d has no positive agent PID", wantGeneration)
		}
		if item.BootstrapVersion != globalNOCBootstrapVersion {
			return fmt.Errorf("NOC launch generation %d has unsupported bootstrap version %d", wantGeneration, item.BootstrapVersion)
		}
		if err := item.Backstop.validate(); err != nil {
			return fmt.Errorf("NOC launch generation %d: %w", wantGeneration, err)
		}
		if item.CreatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
			return fmt.Errorf("NOC launch generation %d timestamps are invalid", wantGeneration)
		}
		if i+1 < len(registry.Launches) && item.State == globalNOCLaunchActive {
			return fmt.Errorf("NOC launch generation %d remained active after replacement", wantGeneration)
		}
	}
	seenRunIDs := make(map[string]struct{}, len(registry.Runs))
	for _, item := range registry.Runs {
		runID := strings.TrimSpace(item.ID)
		if runID == "" || runID != item.ID || item.NOCGeneration == 0 || item.NOCGeneration > registry.CurrentGeneration {
			return fmt.Errorf("NOC run registration identity is invalid")
		}
		if _, exists := seenRunIDs[runID]; exists {
			return fmt.Errorf("duplicate run registrations share ID %q", runID)
		}
		seenRunIDs[runID] = struct{}{}
		if registry.Launches[item.NOCGeneration-1].ID != item.NOCLaunchID {
			return fmt.Errorf("NOC run registration launch binding mismatch")
		}
		switch item.State {
		case globalNOCRunPlanned, globalNOCRunRegistered, globalNOCRunPollRequired, globalNOCRunOptOut:
		default:
			return fmt.Errorf("NOC run registration %s has invalid state %q", item.ID, item.State)
		}
		if strings.TrimSpace(item.Namespace.ID) == "" || item.CreatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
			return fmt.Errorf("NOC run registration %s is incomplete", item.ID)
		}
	}
	return nil
}
