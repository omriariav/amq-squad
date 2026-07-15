package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

const paneCleanupManifestSchema = 1

type paneCleanupManifestPhase string

const (
	paneCleanupManifestPrepared  paneCleanupManifestPhase = "prepared"
	paneCleanupManifestFinalized paneCleanupManifestPhase = "finalized"
)

type paneCleanupManifestEntry struct {
	Role        string              `json:"role"`
	Handle      string              `json:"handle"`
	Requested   bool                `json:"requested"`
	Identity    PaneCleanupIdentity `json:"identity"`
	AgentStatus string              `json:"agent_status,omitempty"`
	AgentDetail string              `json:"agent_detail,omitempty"`
	Pane        *PaneCleanupResult  `json:"pane,omitempty"`
}

type paneCleanupManifest struct {
	Schema            int                        `json:"schema"`
	OperationID       string                     `json:"operation_id"`
	Operation         string                     `json:"operation"`
	Phase             paneCleanupManifestPhase   `json:"phase"`
	Project           string                     `json:"project"`
	Profile           string                     `json:"profile"`
	Session           string                     `json:"session"`
	CreatedAt         time.Time                  `json:"created_at"`
	FinalizedAt       time.Time                  `json:"finalized_at,omitempty"`
	PreparedSHA256    string                     `json:"prepared_sha256,omitempty"`
	NamespaceMutation string                     `json:"namespace_mutation,omitempty"`
	Entries           []paneCleanupManifestEntry `json:"entries"`
}

type paneCleanupManifestHandle struct {
	Project          string
	Profile          string
	Session          string
	OperationID      string
	Operation        string
	PreparedSHA256   string
	PreparedManifest paneCleanupManifest
	Prepared         string
	Final            string
}

type paneCleanupManifestStore interface {
	Prepare(projectDir string, manifest paneCleanupManifest) (paneCleanupManifestHandle, error)
	Finalize(handle paneCleanupManifestHandle, manifest paneCleanupManifest) error
}

type filesystemPaneCleanupManifestStore struct{}

func newPaneCleanupOperationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate pane cleanup operation id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func normalizePaneCleanupProfile(profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	if err := team.ValidateProfileName(profile); err != nil {
		return "", err
	}
	return profile, nil
}

func validatePaneCleanupManifestIdentity(m paneCleanupManifest) error {
	if m.Schema != paneCleanupManifestSchema {
		return fmt.Errorf("pane cleanup manifest schema %d is not supported", m.Schema)
	}
	if strings.TrimSpace(m.OperationID) == "" || !isSafePaneCleanupComponent(m.OperationID) {
		return fmt.Errorf("invalid pane cleanup operation id %q", m.OperationID)
	}
	if m.Operation != "rm" && m.Operation != "archive" {
		return fmt.Errorf("invalid pane cleanup operation %q", m.Operation)
	}
	if strings.TrimSpace(m.Project) == "" {
		return fmt.Errorf("pane cleanup manifest project is required")
	}
	if m.Phase != paneCleanupManifestPrepared && m.Phase != paneCleanupManifestFinalized {
		return fmt.Errorf("invalid pane cleanup manifest phase %q", m.Phase)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("pane cleanup manifest created_at is required")
	}
	if m.Phase == paneCleanupManifestPrepared && !m.FinalizedAt.IsZero() {
		return fmt.Errorf("prepared pane cleanup manifest cannot have finalized_at")
	}
	if m.Phase == paneCleanupManifestFinalized {
		if strings.TrimSpace(m.PreparedSHA256) == "" {
			return fmt.Errorf("final pane cleanup manifest lacks prepared digest binding")
		}
		if strings.TrimSpace(m.NamespaceMutation) == "" {
			return fmt.Errorf("final pane cleanup manifest lacks namespace mutation status")
		}
		if m.FinalizedAt.IsZero() || m.FinalizedAt.Before(m.CreatedAt) {
			return fmt.Errorf("final pane cleanup manifest has invalid finalized_at")
		}
	}
	if _, err := normalizePaneCleanupProfile(m.Profile); err != nil {
		return err
	}
	if err := validateWorkstreamName(m.Session); err != nil {
		return err
	}
	return nil
}

func isSafePaneCleanupComponent(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}
