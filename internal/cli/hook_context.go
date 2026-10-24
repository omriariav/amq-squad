package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/role"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

const contextUsage = `amq-squad context - the seat's role, rules digest, current task and open gates

Usage:
  amq-squad context --me HANDLE [--root DIR] [--session S] [--project DIR] [--profile P] [--json]

Reads only; identity comes from AM_ME, AM_ROOT and AM_SESSION (a flag may fill
an unset variable, never contradict a set one). Rules: $AMQ_SQUAD_RULES_PATH,
else team-rules.md under the launch record's team home. Task store: the launch
record's team home and profile unless --project/--profile. Exit 2 on a usage
or identity error.

Examples:
  amq-squad context --me cto
  amq-squad context --me cto --json
`

type hookFileDigest struct {
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type hookTaskView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"lease_expires_at"`
	Expired   bool      `json:"lease_expired,omitempty"`
}

type hookContextData struct {
	Seat      hookIdentity   `json:"seat"`
	Role      hookFileDigest `json:"role"`
	RoleText  string         `json:"role_text"`
	Rules     hookFileDigest `json:"rules"`
	Task      *hookTaskView  `json:"task"`
	OpenGates []hookGate     `json:"open_gates"`
	Warnings  []string       `json:"warnings,omitempty"`
}

func runContext(args []string) error {
	h := newHookFlags("context", contextUsage)
	project := h.fs.String("project", "", "team home of the task store (default: launch record)")
	profile := h.fs.String("profile", "", "team profile of the task store (default: launch record)")
	if err := h.parse(args); err != nil {
		return err
	}
	id, err := h.identity()
	if err != nil {
		return err
	}
	d := buildHookContext(id, *project, *profile, hookNow())
	if *h.jsonOut {
		return printJSONEnvelope("context", d)
	}
	fmt.Printf("amq-squad seat %s (session %s, root %s)\nrules: %s %s\n", d.Seat.Handle, d.Seat.Session, d.Seat.Root, d.Rules.Path, d.Rules.SHA256)
	if t := d.Task; t != nil {
		fmt.Printf("task: %s %s %q lease until %s\n", t.ID, t.Status, t.Title, t.ExpiresAt.UTC().Format(time.RFC3339))
	}
	for _, g := range d.OpenGates {
		fmt.Printf("open gate: %s class %s digest %s\n", g.GateID, g.Class, g.Digest)
	}
	for _, w := range d.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	fmt.Printf("role: %s %s\n%s", d.Role.Path, d.Role.SHA256, d.RoleText)
	return nil
}

func buildHookContext(id hookIdentity, project, profile string, now time.Time) hookContextData {
	d := hookContextData{Seat: id, OpenGates: []hookGate{}}
	warn := func(what string, err error) { d.Warnings = append(d.Warnings, what+": "+err.Error()) }
	rec, err := launch.Read(id.AgentDir)
	if err != nil && !os.IsNotExist(err) {
		warn("launch record", err)
	}
	project, profile, session := firstOf(project, rec.TeamHome), firstOf(profile, rec.TeamProfile), firstOf(id.Session, rec.Session)
	rolePath := role.ExistingPath(id.AgentDir)
	if text, sum, err := readHookFile(rolePath); err == nil {
		d.Role, d.RoleText = hookFileDigest{Path: rolePath, SHA256: sum}, text
	} else {
		warn("role", err)
	}
	rulesPath := strings.TrimSpace(os.Getenv("AMQ_SQUAD_RULES_PATH"))
	if rulesPath == "" && project != "" {
		rulesPath = rules.Path(project)
	}
	if _, sum, err := readHookFile(rulesPath); err == nil {
		d.Rules = hookFileDigest{Path: rulesPath, SHA256: sum}
	} else if rulesPath != "" {
		warn("rules", err)
	}
	if project != "" && session != "" {
		if d.Task, err = leasedHookTask(taskstore.DirForProfile(project, profile, session), id.Handle, now); err != nil {
			warn("task store", err)
		}
	}
	gates, err := readHookGates(id.Root, now)
	if err != nil {
		warn("gate store", err)
	}
	for _, g := range gates {
		if g.State == "open" && (g.RaisedBy == "" || g.RaisedBy == id.Handle) {
			d.OpenGates = append(d.OpenGates, g)
		}
	}
	return d
}

func readHookFile(path string) (string, string, error) {
	raw, err := os.ReadFile(path)
	sum := sha256.Sum256(raw)
	return string(raw), "sha256:" + hex.EncodeToString(sum[:]), err
}

// leasedHookTask returns the in-progress task whose lease the handle holds,
// the most recently renewed if several. It reads the task files without the
// store lock (they are replaced by rename, never written in place), so a hook
// never blocks behind a writer and never writes.
func leasedHookTask(dir, handle string, now time.Time) (*hookTaskView, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	var best *taskstore.Task
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var t taskstore.Task
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil {
			err = json.Unmarshal(raw, &t)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if t.Status == taskstore.StatusInProgress && t.Lease != nil && t.Lease.Owner == handle && (best == nil || t.Lease.RenewedAt.After(best.Lease.RenewedAt)) {
			best = &t
		}
	}
	if best == nil {
		return nil, err
	}
	return &hookTaskView{ID: best.ID, Title: best.Title, Status: best.Status, ExpiresAt: best.Lease.ExpiresAt, Expired: now.After(best.Lease.ExpiresAt)}, nil
}
