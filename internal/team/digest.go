package team

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/rules"
)

// The digest flow (R8, plan 3.3): the lead proposes the squad as one digest
// over every file it would write, nothing is written, and the files are
// written only when apply presents the same digest. The digest format:
// "sha256:" + hex(sha256(concat(path NUL content NUL))) over the planned
// files in plan order: squad.yaml, .amq-squad/team-rules.md, then CLAUDE.md
// and AGENTS.md for the repository root and each seat directory inside the
// repository that exists, sorted. 4-1's pi lead proposes through the same
// function.

// PlannedFile is one planned write.
type PlannedFile struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // create, replace, unchanged
	SHA256  string `json:"sha256"`
	content string
}

// Plan is a proposal: the planned files and their digest.
type Plan struct {
	ProjectDir string        `json:"project_dir"`
	Digest     string        `json:"digest"`
	Seats      int           `json:"seats"`
	Files      []PlannedFile `json:"files"`
	pointers   []rules.SyncPlan
}

// NewPlan plans squad.yaml (squadText, already parsed into t), team-rules.md
// (rulesText) and the CLAUDE.md / AGENTS.md pointer stubs. It writes nothing.
func NewPlan(projectDir, squadText string, t Team, rulesText string) (Plan, error) {
	p := Plan{ProjectDir: projectDir, Seats: len(t.Members)}
	p.add(SquadPath(projectDir), squadText)
	p.add(rules.Path(projectDir), rulesText)
	for _, dir := range pointerDirs(projectDir, t.Members) {
		plans, err := rules.Plan(dir, rulesText)
		if err != nil {
			return Plan{}, err
		}
		for _, plan := range plans {
			p.add(plan.Target, plan.After)
		}
		p.pointers = append(p.pointers, plans...)
	}
	sum := sha256.New()
	for _, f := range p.Files {
		fmt.Fprintf(sum, "%s\x00%s\x00", f.Path, f.content)
	}
	p.Digest = "sha256:" + hex.EncodeToString(sum.Sum(nil))
	return p, nil
}

func (p *Plan) add(path, content string) {
	action := "create"
	if before, err := os.ReadFile(path); err == nil {
		action = map[bool]string{true: "unchanged", false: "replace"}[string(before) == content]
	}
	sum := sha256.Sum256([]byte(content))
	p.Files = append(p.Files, PlannedFile{Path: path, Action: action, SHA256: hex.EncodeToString(sum[:]), content: content})
}

// pointerDirs are the existing seat directories inside the repository, the
// repository root first.
func pointerDirs(projectDir string, members []Member) []string {
	seen, out := map[string]bool{projectDir: true}, []string{projectDir}
	for _, m := range members {
		dir := m.EffectiveCWD(projectDir)
		rel, err := filepath.Rel(projectDir, dir)
		if info, statErr := os.Stat(dir); seen[dir] || err != nil || strings.HasPrefix(rel, "..") || statErr != nil || !info.IsDir() {
			continue
		}
		seen[dir], out = true, append(out, dir)
	}
	sort.Strings(out[1:])
	return out
}

// Apply writes the plan when digest matches it; pointer stubs that changed
// since the plan are refused by rules.Apply.
func (p Plan) Apply(digest string) error {
	if strings.TrimSpace(digest) != p.Digest {
		return fmt.Errorf("digest mismatch: the planned files changed since the proposal (now %s); run team propose again", p.Digest)
	}
	return WithLock(p.ProjectDir, func() error {
		for _, f := range p.Files[:2] {
			if f.Action != "unchanged" {
				if err := WriteFileAtomic(f.Path, []byte(f.content)); err != nil {
					return err
				}
			}
		}
		_, err := rules.Apply(p.pointers)
		return err
	})
}
