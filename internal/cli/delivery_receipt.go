package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type deliveryReceiptData struct {
	AttemptID    string                 `json:"attempt_id"`
	Kind         string                 `json:"kind"`
	Method       string                 `json:"method,omitempty"`
	Status       string                 `json:"status"`
	Target       deliveryReceiptTarget  `json:"target"`
	MessageID    string                 `json:"message_id,omitempty"`
	TaskID       string                 `json:"task_id,omitempty"`
	Root         string                 `json:"root,omitempty"`
	Thread       string                 `json:"thread,omitempty"`
	PaneID       string                 `json:"pane_id,omitempty"`
	Fallback     bool                   `json:"fallback"`
	Acknowledged bool                   `json:"acknowledged"`
	Stages       []deliveryReceiptStage `json:"stages"`
	Detail       string                 `json:"detail,omitempty"`
	Path         string                 `json:"path,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
}

type deliveryReceiptTarget struct {
	ProjectDir    string `json:"project_dir,omitempty"`
	Profile       string `json:"profile"`
	Session       string `json:"session"`
	NamespaceID   string `json:"namespace_id"`
	Role          string `json:"role,omitempty"`
	Handle        string `json:"handle,omitempty"`
	ExecutionMode string `json:"execution_mode,omitempty"`
}

type deliveryReceiptStage struct {
	State  string    `json:"state"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
}

func newDeliveryReceipt(projectDir, profile, session, role, handle, executionMode, kind string) deliveryReceiptData {
	now := time.Now().UTC()
	profile = squadnamespace.NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	return deliveryReceiptData{
		AttemptID: deliveryAttemptID(now, kind, role, handle),
		Kind:      kind,
		Status:    "queued",
		Target: deliveryReceiptTarget{
			ProjectDir:    projectDir,
			Profile:       profile,
			Session:       session,
			NamespaceID:   squadnamespace.ID(profile, session),
			Role:          role,
			Handle:        handle,
			ExecutionMode: executionMode,
		},
		CreatedAt: now,
	}
}

func deliveryAttemptID(now time.Time, kind, role, handle string) string {
	seed := strings.Join([]string{kind, role, handle}, "-")
	seed = sanitizeWorkstreamName(seed)
	if seed == "" {
		seed = "delivery"
	}
	return fmt.Sprintf("%s-%s", now.Format("20060102T150405.000000000Z"), seed)
}

func (r *deliveryReceiptData) addStage(state, detail string) {
	if r == nil {
		return
	}
	r.Stages = append(r.Stages, deliveryReceiptStage{
		State:  state,
		At:     time.Now().UTC(),
		Detail: detail,
	})
}

func writeDeliveryReceipt(projectDir, profile, session string, receipt *deliveryReceiptData) error {
	if receipt == nil {
		return nil
	}
	dir := deliveryReceiptDir(projectDir, profile, session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure delivery receipt dir: %w", err)
	}
	path := filepath.Join(dir, receipt.AttemptID+".json")
	receipt.Path = path
	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal delivery receipt: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write delivery receipt: %w", err)
	}
	return nil
}

func deliveryReceiptDir(projectDir, profile, session string) string {
	base := filepath.Join(projectDir, team.DirName, "receipts")
	if squadnamespace.NormalizeProfile(profile) != team.DefaultProfile {
		base = filepath.Join(base, squadnamespace.NormalizeProfile(profile))
	}
	return filepath.Join(base, strings.TrimSpace(session))
}
