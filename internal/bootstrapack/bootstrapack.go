// Package bootstrapack implements the dependency-light bootstrap attestation
// marker shared by launch, status, doctor, and the bootstrap ack command.
package bootstrapack

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	PromptVersion = "1"
	MarkerFile    = "ack.json"
	GracePeriod   = 90 * time.Second
)

var RequiredSteps = []string{"startup-files", "initial-drain", "context-review"}

type Expectation struct {
	LaunchID          string    `json:"launch_id"`
	PromptVersion     string    `json:"prompt_version"`
	IssuedAt          time.Time `json:"issued_at"`
	Required          bool      `json:"required"`
	NotRequiredReason string    `json:"not_required_reason,omitempty"`
}

type Marker struct {
	LaunchID       string    `json:"launch_id"`
	PromptVersion  string    `json:"prompt_version"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
	Handle         string    `json:"handle"`
	Role           string    `json:"role"`
	Profile        string    `json:"profile,omitempty"`
	Session        string    `json:"session"`
	Root           string    `json:"root"`
	SkillVersion   string    `json:"skill_version"`
	Steps          []string  `json:"steps"`
}

type Identity struct {
	Handle  string
	Role    string
	Profile string
	Session string
	Root    string
}

type Result struct {
	State          string     `json:"state"`
	Required       bool       `json:"required"`
	LaunchID       string     `json:"launch_id,omitempty"`
	PromptVersion  string     `json:"prompt_version,omitempty"`
	IssuedAt       *time.Time `json:"issued_at,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	SkillVersion   string     `json:"skill_version,omitempty"`
	Detail         string     `json:"detail,omitempty"`
}

func NewExpectation(required bool, now time.Time) (Expectation, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Expectation{}, fmt.Errorf("generate bootstrap launch id: %w", err)
	}
	e := Expectation{LaunchID: hex.EncodeToString(id), PromptVersion: PromptVersion, IssuedAt: now.UTC(), Required: required}
	if !required {
		e.NotRequiredReason = "bootstrap acknowledgement not required"
	}
	return e, nil
}

func Path(agentDir string) string {
	return filepath.Join(agentDir, "extensions", "io.github.omriariav.amq-squad", "bootstrap", MarkerFile)
}

func Write(agentDir string, marker Marker) error {
	if err := validateMarkerShape(marker); err != nil {
		return err
	}
	dir := filepath.Dir(Path(agentDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ensure bootstrap marker dir: %w", err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bootstrap marker directory must be a directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod bootstrap marker dir: %w", err)
	}
	b, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bootstrap marker: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".bootstrap-ack-*")
	if err != nil {
		return fmt.Errorf("create bootstrap marker temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod bootstrap marker temp: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write bootstrap marker temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync bootstrap marker temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close bootstrap marker temp: %w", err)
	}
	if err := os.Rename(tmpName, Path(agentDir)); err != nil {
		return fmt.Errorf("rename bootstrap marker: %w", err)
	}
	if err := os.Chmod(Path(agentDir), 0o600); err != nil {
		return fmt.Errorf("chmod bootstrap marker: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open bootstrap marker dir: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync bootstrap marker dir: %w", err)
	}
	return nil
}

func CompleteSteps(steps []string) bool {
	if len(steps) != len(RequiredSteps) {
		return false
	}
	for i := range steps {
		if strings.TrimSpace(steps[i]) != RequiredSteps[i] {
			return false
		}
	}
	return true
}

func Read(agentDir string) (Marker, error) {
	info, err := os.Lstat(Path(agentDir))
	if err != nil {
		return Marker{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return Marker{}, fmt.Errorf("bootstrap marker must be a regular 0600 file")
	}
	b, err := os.ReadFile(Path(agentDir))
	if err != nil {
		return Marker{}, err
	}
	var m Marker
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Marker{}, fmt.Errorf("parse bootstrap marker: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return Marker{}, fmt.Errorf("parse bootstrap marker: trailing JSON value")
	}
	if err := validateMarkerShape(m); err != nil {
		return Marker{}, err
	}
	return m, nil
}

func Evaluate(expect *Expectation, identity Identity, agentDir string, now time.Time) Result {
	if expect == nil {
		return Result{State: "legacy_unknown", Detail: "launch record predates bootstrap attestation"}
	}
	issued := expect.IssuedAt
	base := Result{Required: expect.Required, LaunchID: expect.LaunchID, PromptVersion: expect.PromptVersion, IssuedAt: &issued}
	if err := validateExpectation(*expect); err != nil {
		base.State = "malformed"
		base.Detail = err.Error()
		return base
	}
	if !expect.Required {
		base.State = "not_required"
		base.Detail = expect.NotRequiredReason
		return base
	}
	m, err := Read(agentDir)
	if err != nil {
		if os.IsNotExist(err) {
			if now.Before(expect.IssuedAt.Add(GracePeriod)) {
				base.State = "pending"
				base.Detail = "bootstrap acknowledgement grace period"
				return base
			}
			base.State = "unverified"
			base.Detail = "required bootstrap acknowledgement is missing"
		} else {
			base.State = "malformed"
			base.Detail = err.Error()
		}
		return base
	}
	if m.LaunchID != expect.LaunchID || m.PromptVersion != expect.PromptVersion || m.Handle != identity.Handle || m.Role != identity.Role || m.Profile != identity.Profile || m.Session != identity.Session || filepath.Clean(m.Root) != filepath.Clean(identity.Root) || m.AcknowledgedAt.Before(expect.IssuedAt.Add(-5*time.Second)) {
		base.State = "mismatch"
		base.Detail = "bootstrap acknowledgement does not match the current launch identity"
		return base
	}
	base.State = "verified"
	base.AcknowledgedAt = &m.AcknowledgedAt
	base.SkillVersion = m.SkillVersion
	return base
}

func validateExpectation(e Expectation) error {
	if len(e.LaunchID) != 32 {
		return fmt.Errorf("malformed bootstrap expectation launch id")
	}
	if _, err := hex.DecodeString(e.LaunchID); err != nil {
		return fmt.Errorf("malformed bootstrap expectation launch id")
	}
	if strings.TrimSpace(e.PromptVersion) == "" || e.IssuedAt.IsZero() {
		return fmt.Errorf("malformed bootstrap expectation")
	}
	if !e.Required && strings.TrimSpace(e.NotRequiredReason) == "" {
		return fmt.Errorf("malformed bootstrap expectation reason")
	}
	return nil
}

func validateMarkerShape(m Marker) error {
	if strings.TrimSpace(m.LaunchID) == "" || strings.TrimSpace(m.PromptVersion) == "" || m.AcknowledgedAt.IsZero() || strings.TrimSpace(m.Handle) == "" || strings.TrimSpace(m.Role) == "" || strings.TrimSpace(m.Session) == "" || !filepath.IsAbs(m.Root) || strings.TrimSpace(m.SkillVersion) == "" || !CompleteSteps(m.Steps) {
		return fmt.Errorf("malformed bootstrap acknowledgement")
	}
	for _, step := range m.Steps {
		if strings.TrimSpace(step) == "" {
			return fmt.Errorf("malformed bootstrap acknowledgement steps")
		}
	}
	return nil
}
