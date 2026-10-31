package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/role"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

const contextUsage = `amq-squad context - the seat's role, rules digest, current task and open gates

Usage:
  amq-squad context --me HANDLE [--root DIR] [--session S] [--project DIR] [--json]

Reads only and never renews a lease; identity comes from AM_ME, AM_ROOT and
AM_SESSION (a flag may fill an unset variable, never contradict a set one).
Rules: $AMQ_SQUAD_RULES_PATH, else team-rules.md under the launch record's team
home (or --project). Task: the task whose lease the seat holds in the store
under the session root (<root>/extensions/amq-squad/tasks/). Exit 2 on a usage
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
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	Class         string    `json:"class"`
	DeclaredFiles []string  `json:"declared_files"`
	ExpiresAt     time.Time `json:"lease_expires_at"`
	Expired       bool      `json:"lease_expired,omitempty"`
	Claimed       bool      `json:"lease_claimed"`
	BlockReason   string    `json:"block_reason,omitempty"`
}

type hookContextData struct {
	Seat      hookIdentity   `json:"seat"`
	Role      hookFileDigest `json:"role"`
	RoleText  string         `json:"role_text"`
	Rules     hookFileDigest `json:"rules"`
	Task      *hookTaskView  `json:"task"`
	OpenGates []hookGate     `json:"open_gates"`
	// Worktree is the seat's own working tree from its launch record (2b-2
	// milestone B): the seat works only there; a task's project_path names
	// the repository, not the seat's tree.
	Worktree string   `json:"worktree,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// recordHookReport is the hook's evidence of its own run (P1-10): every
// hook call of a Claude seat appends a report, so a seat that ran with no
// report since its launch shows as a silent hook in status (a warning since
// brief 31-1, never a quarantine). It reads only the core's staged launch
// record (never the seat's published copy) and never blocks.
func recordHookReport(id hookIdentity, now time.Time) {
	raw, err := os.ReadFile(filepath.Join(launch.StagedDir(id.Root, id.Handle), launch.FileName))
	if err != nil {
		return
	}
	staged, err := launch.Decode(raw)
	if err != nil || staged.Runtime != "claude" || staged.Handle != id.Handle {
		return
	}
	_ = claudeseat.RecordReport(id.Root, staged.Handle, claudeseat.Report{At: now.UTC()})
}

func runContext(args []string) error {
	h := newHookFlags("context", contextUsage)
	project := h.fs.String("project", "", "team home of the rules file (default: launch record)")
	if err := h.parse(args); err != nil {
		return err
	}
	id, err := h.identity()
	if err != nil {
		return err
	}
	d := buildHookContext(id, *project, hookNow())
	if *h.jsonOut {
		return printJSONEnvelope("context", d)
	}
	fmt.Printf("amq-squad seat %s (session %s, root %s)\n", d.Seat.Handle, orNone(d.Seat.Session), d.Seat.Root)
	fmt.Printf("rules: %s %s\n", orNone(d.Rules.Path), d.Rules.SHA256)
	if d.Worktree != "" {
		fmt.Printf("worktree: %s (run every command here; a task's project_path names the repository, not your working tree)\n", d.Worktree)
	}
	if t := d.Task; t != nil {
		if t.Status == taskstore.StateBlocked {
			fmt.Printf("task: %s blocked %q (%s); waiting for the lead\n", t.ID, t.Title, t.BlockReason)
		} else if !t.Claimed {
			fmt.Printf("task: %s %s %q leased to you, not claimed: run amq-squad task claim --me %s before %s\n", t.ID, t.Status, t.Title, d.Seat.Handle, t.ExpiresAt.UTC().Format(time.RFC3339))
		} else {
			fmt.Printf("task: %s %s %q lease until %s\n", t.ID, t.Status, t.Title, t.ExpiresAt.UTC().Format(time.RFC3339))
		}
	} else {
		fmt.Println("task: none")
	}
	if len(d.OpenGates) == 0 {
		fmt.Println("open gates: none")
	}
	for _, g := range d.OpenGates {
		fmt.Printf("open gate: %s class %s digest %s\n", g.GateID, g.Class, g.Digest)
	}
	for _, w := range d.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	fmt.Printf("role: %s %s\n%s", orNone(d.Role.Path), d.Role.SHA256, d.RoleText)
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func buildHookContext(id hookIdentity, project string, now time.Time) hookContextData {
	d := hookContextData{Seat: id, OpenGates: []hookGate{}}
	warn := func(what string, err error) { d.Warnings = append(d.Warnings, what+": "+err.Error()) }
	rec, err := launch.Read(id.AgentDir)
	if err != nil && !os.IsNotExist(err) {
		warn("launch record", err)
	}
	if os.IsNotExist(err) {
		// Before seat-exec publishes (or on a launcher that does not run it),
		// the core's staged record names the seat's tree.
		if raw, serr := os.ReadFile(filepath.Join(launch.StagedDir(id.Root, id.Handle), launch.FileName)); serr == nil {
			rec, _ = launch.Decode(raw)
		}
	}
	d.Worktree = firstOf(rec.Worktree, rec.CWD)
	recordHookReport(id, now)
	project = firstOf(project, rec.TeamHome)
	rolePath := role.ExistingPath(id.AgentDir)
	if text, sum, err := readHookFile(rolePath); err == nil {
		d.Role, d.RoleText = hookFileDigest{Path: rolePath, SHA256: sum}, text
	} else {
		warn("role", err)
	}
	rulesPath := firstOf(strings.TrimSpace(os.Getenv("AMQ_SQUAD_RULES_PATH")), rec.RulesPath)
	if rulesPath == "" && project != "" {
		rulesPath = rules.Path(project)
	}
	if _, sum, err := readHookFile(rulesPath); err == nil {
		d.Rules = hookFileDigest{Path: rulesPath, SHA256: sum}
	} else if rulesPath != "" {
		warn("rules", err)
	}
	// 2b-2: the launch records the rules digest; context flags drift from it.
	if at := firstOf(strings.TrimSpace(os.Getenv("AMQ_SQUAD_RULES_SHA256")), rec.RulesSHA256); at != "" && d.Rules.SHA256 != "" && at != d.Rules.SHA256 {
		d.Warnings = append(d.Warnings, "rules changed since launch: recorded "+at+", now "+d.Rules.SHA256)
	}
	// The seat reads a 0400 snapshot; the source file can still drift.
	if src := rec.RulesPath; src != "" && src != rulesPath && rec.RulesSHA256 != "" {
		if _, sum, err := readHookFile(src); err == nil && sum != rec.RulesSHA256 {
			d.Warnings = append(d.Warnings, "rules changed since launch: the source "+src+" is now "+sum+", the seat holds the launch snapshot "+rec.RulesSHA256)
		}
	}
	if d.Task, err = leasedHookTask(taskstore.DirFor(id.Root), id.Handle, now); err != nil {
		warn("task store", err)
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
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return string(raw), "sha256:" + hex.EncodeToString(sum[:]), nil
}

// leasedHookTask returns the task whose lease the handle holds (one seat
// holds at most one), or the blocked task whose done report it filed. It
// reads the store lock-free (task.Snapshot: files replaced by rename, a
// committed journal overlaid), so a hook never blocks behind a writer and
// never writes.
func leasedHookTask(dir, handle string, now time.Time) (*hookTaskView, error) {
	list, err := taskstore.Snapshot(dir)
	if err != nil {
		return nil, err
	}
	t := taskstore.HeldBy(handle, list)
	if t == nil {
		return nil, nil
	}
	v := &hookTaskView{ID: t.ID, Title: t.Title, Status: t.State, Class: t.Class, DeclaredFiles: t.DeclaredFiles, BlockReason: t.BlockReason}
	if t.Lease != nil {
		v.ExpiresAt, v.Expired, v.Claimed = t.Lease.ExpiresAt, !now.Before(t.Lease.ExpiresAt), t.Lease.ClaimedAt != nil
	}
	return v, nil
}
