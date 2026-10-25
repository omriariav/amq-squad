package uninstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

const (
	extDir = "extensions"
	amqDir = "amq-squad"
)

var errAbsent = errors.New("absent")

// op is one removal: the entry name below an anchored directory, opened one
// component at a time from base with no symlink followed.
type op struct {
	group   string
	display string
	note    string
	secret  bool
	base    string
	rel     []string
	name    string
	ev      *evidenceOp
}

// evidenceOp deletes the evidence refs one ledger records.
type evidenceOp struct {
	checkouts []string
	refs      []string
}

type plan struct {
	ops    []op
	byHand []string
	left   []string
}

var (
	sealedNames = map[string]bool{"commits": true, "ledger": true, "routing": true}
	gateNames   = map[string]bool{"gates": true, "answers": true, "denials": true, "reasks": true, "consumed": true, "clears": true}
	// ownerFiles are the files of ~/.amq-squad this verb removes with
	// --owner-files; the first two are secrets.
	ownerFiles = []struct {
		name   string
		secret bool
	}{{"claude-token", true}, {gateBindingFile, true}, {"registry.yaml", false}, {"grants.yaml", false}}
)

const gateBindingFile = "gate-binding.json"

func symlinkErr(path string) error {
	return &Error{Code: CodeSymlink, Detail: path + ": a symlink (or not a directory) stands where a directory is expected; nothing was removed"}
}

// openDir opens base/rel through the descriptor walk. A missing directory is
// errAbsent; a symlink or a non-directory in the way is the coded refusal.
func openDir(base string, rel []string) (*fsanchor.AnchoredDir, error) {
	d, err := fsanchor.OpenAnchored(base, rel, false)
	if err == nil {
		return d, nil
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, errAbsent
	case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENOTDIR):
		return nil, symlinkErr(filepath.Join(append([]string{base}, rel...)...))
	}
	return nil, err
}

func isLink(d *fsanchor.AnchoredDir, name string) bool {
	_, err := d.Readlink(name)
	return err == nil
}

// kindOf reports what name is below d without following a link.
func kindOf(d *fsanchor.AnchoredDir, name string) (fsanchor.EntryKind, bool) {
	f, kind, _, _, err := d.OpenEntry(name)
	if err != nil {
		return fsanchor.KindOther, false
	}
	if f != nil {
		f.Close()
	}
	return kind, true
}

// subOf opens a child directory; ok is false when it is not a directory.
func subOf(d *fsanchor.AnchoredDir, name string) (*fsanchor.AnchoredDir, bool) {
	s, err := d.Sub(name)
	if err != nil {
		return nil, false
	}
	return s, true
}

func readJSON(d *fsanchor.AnchoredDir, name string, v any) bool {
	raw, err := fsanchor.ReadEntry(d, name)
	return err == nil && json.Unmarshal(raw, v) == nil
}

func sortedNames(d *fsanchor.AnchoredDir) []string {
	names, _ := d.Names()
	sort.Strings(names)
	return names
}

// discover reads the layout (read only) and returns the removal plan. A
// symlink where a directory is expected, anywhere in the layout, refuses the
// whole plan.
func discover(o Options) (*plan, error) {
	p := &plan{}
	var checkouts []string
	var worktrees []string
	xrel := []string{extDir, amqDir}
	xpath := filepath.Join(o.Root, extDir, amqDir)
	x, err := openDir(o.Root, xrel)
	switch {
	case errors.Is(err, errAbsent):
	case err != nil:
		return nil, err
	default:
		defer x.Close()
		var sch struct {
			ProjectRoot string `json:"project_root"`
		}
		if sd, ok := subOf(x, "scheduler"); ok {
			readJSON(sd, "launch.json", &sch)
			sd.Close()
		}
		if sch.ProjectRoot != "" {
			checkouts = append(checkouts, sch.ProjectRoot)
		}
		for _, name := range sortedNames(x) {
			if isLink(x, name) {
				return nil, symlinkErr(filepath.Join(xpath, name))
			}
			disp := filepath.Join(xpath, name)
			switch {
			case name == "launch":
				w, err := p.launch(o.Root, x, xrel, disp)
				if err != nil {
					return nil, err
				}
				worktrees = append(worktrees, w...)
			case name == "scheduler":
				if err := p.scheduler(o.Root, x, xrel, disp); err != nil {
					return nil, err
				}
			case name == "tools":
				p.children(o.Root, x, xrel, name, GroupTools, "")
			case sealedNames[name]:
				if name == "commits" {
					ws, evs := p.commits(x, o.Root, xrel, checkouts)
					worktrees = append(worktrees, ws...)
					p.ops = append(p.ops, evs...)
				}
				p.ops = append(p.ops, op{group: GroupSealed, display: disp, base: o.Root, rel: xrel, name: name, note: sealNote[name]})
			case gateNames[name]:
				p.ops = append(p.ops, op{group: GroupGate, display: disp, base: o.Root, rel: xrel, name: name})
			default:
				note := ""
				if name == "verifiers" {
					note = "includes the verifier module cache (verifiers/gomodcache)"
				}
				p.ops = append(p.ops, op{group: GroupSession, display: disp, base: o.Root, rel: xrel, name: name, note: note})
			}
		}
	}
	if err := p.worktreeOps(worktrees); err != nil {
		return nil, err
	}
	if err := p.ownerOps(o); err != nil {
		return nil, err
	}
	p.manual(o, worktrees)
	return p, nil
}

var sealNote = map[string]string{
	"commits": "the commits ledger (sealed)",
	"ledger":  "the hook ledger",
	"routing": "the outcome log and the session seed that seals the ledgers",
}

// children adds one op per entry of x/name (a refusal-free listing: the
// caller already knows name is not a link).
func (p *plan) children(root string, x *fsanchor.AnchoredDir, xrel []string, name, group, note string) {
	sd, ok := subOf(x, name)
	if !ok {
		p.ops = append(p.ops, op{group: group, display: filepath.Join(root, extDir, amqDir, name), base: root, rel: xrel, name: name})
		return
	}
	defer sd.Close()
	rel := append(append([]string{}, xrel...), name)
	for _, c := range sortedNames(sd) {
		p.ops = append(p.ops, op{group: group, display: filepath.Join(root, extDir, amqDir, name, c), base: root, rel: rel, name: c, note: note})
	}
}

// launch lists the staged state of the launch directory: the per-seat
// config dirs of every launch as their own group, the directory itself as
// session state, and returns the worktrees the staged records name.
func (p *plan) launch(root string, x *fsanchor.AnchoredDir, xrel []string, disp string) ([]string, error) {
	var worktrees []string
	ld, ok := subOf(x, "launch")
	if ok {
		defer ld.Close()
		for _, h := range sortedNames(ld) {
			if isLink(ld, h) {
				return nil, symlinkErr(filepath.Join(disp, h))
			}
			hd, ok := subOf(ld, h)
			if !ok {
				continue
			}
			var rec launch.Record
			if readJSON(hd, launch.FileName, &rec) && rec.Worktree != "" {
				worktrees = append(worktrees, rec.Worktree)
			}
			if isLink(hd, "claude-config") {
				hd.Close()
				return nil, symlinkErr(filepath.Join(disp, h, "claude-config"))
			}
			if cd, ok := subOf(hd, "claude-config"); ok {
				rel := append(append([]string{}, xrel...), "launch", h, "claude-config")
				for _, id := range sortedNames(cd) {
					if isLink(cd, id) {
						cd.Close()
						hd.Close()
						return nil, symlinkErr(filepath.Join(disp, h, "claude-config", id))
					}
					p.ops = append(p.ops, op{group: GroupSeatCfg, display: filepath.Join(disp, h, "claude-config", id), base: root, rel: rel, name: id})
				}
				cd.Close()
			}
			hd.Close()
		}
	}
	p.ops = append(p.ops, op{group: GroupSession, display: disp, note: "staged launch records, proofs, alarms", base: root, rel: xrel, name: "launch"})
	return worktrees, nil
}

// scheduler lists the scheduler directory: the exits queue is a sealed
// ledger, everything else session state.
func (p *plan) scheduler(root string, x *fsanchor.AnchoredDir, xrel []string, disp string) error {
	sd, ok := subOf(x, "scheduler")
	if !ok {
		p.ops = append(p.ops, op{group: GroupSession, display: disp, base: root, rel: xrel, name: "scheduler"})
		return nil
	}
	defer sd.Close()
	rel := append(append([]string{}, xrel...), "scheduler")
	for _, c := range sortedNames(sd) {
		if isLink(sd, c) {
			return symlinkErr(filepath.Join(disp, c))
		}
		o := op{group: GroupSession, display: filepath.Join(disp, c), base: root, rel: rel, name: c}
		if c == "exits.json" {
			o.group, o.note = GroupSealed, "the exits queue (sealed)"
		}
		p.ops = append(p.ops, o)
	}
	return nil
}

// commits reads the commit ledgers for the worktrees and evidence refs they
// record.
func (p *plan) commits(x *fsanchor.AnchoredDir, root string, xrel, checkouts []string) ([]string, []op) {
	cd, ok := subOf(x, "commits")
	if !ok {
		return nil, nil
	}
	defer cd.Close()
	var worktrees []string
	var evs []op
	for _, n := range sortedNames(cd) {
		var l seatcommit.Ledger
		if !readJSON(cd, n, &l) {
			continue
		}
		cos := append([]string{l.Worktree}, checkouts...)
		if l.Worktree != "" {
			worktrees = append(worktrees, l.Worktree)
		}
		for _, ref := range l.EvidenceRefs() {
			evs = append(evs, op{group: GroupEvidence, display: ref + " (" + l.Handle + ")", ev: &evidenceOp{checkouts: cos, refs: []string{ref}}})
		}
	}
	return worktrees, evs
}

// worktreeOps plans the worktrees the core created: a seat worktree is
// <project>/.claude/worktrees/<name>, opened from the project root.
func (p *plan) worktreeOps(paths []string) error {
	seen := map[string]bool{}
	byProject := map[string][]string{}
	for _, w := range paths {
		if !filepath.IsAbs(w) || filepath.Clean(w) != w || seen[w] {
			continue
		}
		seen[w] = true
		wt := filepath.Dir(w)
		if filepath.Base(wt) != "worktrees" || filepath.Base(filepath.Dir(wt)) != ".claude" {
			continue // not one the core created under .claude/worktrees
		}
		proj := filepath.Dir(filepath.Dir(wt))
		byProject[proj] = append(byProject[proj], filepath.Base(w))
	}
	projs := make([]string, 0, len(byProject))
	for k := range byProject {
		projs = append(projs, k)
	}
	sort.Strings(projs)
	for _, proj := range projs {
		rel := []string{".claude", "worktrees"}
		d, err := openDir(proj, rel)
		if errors.Is(err, errAbsent) {
			continue
		}
		if err != nil {
			return err
		}
		names := byProject[proj]
		sort.Strings(names)
		for _, n := range names {
			disp := filepath.Join(proj, ".claude", "worktrees", n)
			if isLink(d, n) {
				d.Close()
				return symlinkErr(disp)
			}
			if kind, ok := kindOf(d, n); ok && kind == fsanchor.KindDir {
				p.ops = append(p.ops, op{group: GroupWorktree, display: disp, note: "uncommitted work in it is lost", base: proj, rel: rel, name: n})
			}
		}
		d.Close()
	}
	return nil
}

// ownerOps plans the owner files under ~/.amq-squad and names what else is
// left there.
func (p *plan) ownerOps(o Options) error {
	rel := []string{".amq-squad"}
	d, err := openDir(o.Home, rel)
	if errors.Is(err, errAbsent) {
		return nil
	}
	if err != nil {
		return err
	}
	defer d.Close()
	mine := map[string]bool{}
	for _, f := range ownerFiles {
		mine[f.name] = true
		disp := filepath.Join(o.Home, ".amq-squad", f.name)
		if isLink(d, f.name) {
			return symlinkErr(disp)
		}
		if kind, ok := kindOf(d, f.name); ok && kind == fsanchor.KindRegular {
			note := ""
			if f.secret {
				note = "secret: overwritten once with zeros, then unlinked; never printed"
			}
			p.ops = append(p.ops, op{group: GroupOwner, display: disp, secret: f.secret, note: note, base: o.Home, rel: rel, name: f.name})
		}
	}
	for _, n := range sortedNames(d) {
		if !mine[n] {
			p.left = append(p.left, filepath.Join(o.Home, ".amq-squad", n))
		}
	}
	return nil
}

// manual is what the owner does by hand (R16: this verb never writes the
// owner's global agent config).
func (p *plan) manual(o Options, worktrees []string) {
	p.byHand = append(p.byHand,
		"claude plugin uninstall "+claudeseat.PluginKey+"   (Claude's plugin cache and ~/.claude/settings.json are Claude's; not touched)")
	trusted, notify := trustedHere(o, worktrees)
	for _, t := range trusted {
		p.byHand = append(p.byHand, fmt.Sprintf("remove the codex trust entry [projects.%q] from %s (codex-cli owns it; not touched)", t, codexConfig(o)))
	}
	if notify {
		p.byHand = append(p.byHand, fmt.Sprintf("if you no longer want it, remove the notify line that names the amq-squad fan-out script from %s (not touched)", codexConfig(o)))
	}
	repos := map[string]bool{}
	for _, w := range worktrees {
		wt := filepath.Dir(w)
		if filepath.Base(wt) == "worktrees" && filepath.Base(filepath.Dir(wt)) == ".claude" {
			repos[filepath.Dir(filepath.Dir(wt))] = true
		}
	}
	list := make([]string, 0, len(repos))
	for r := range repos {
		list = append(list, r)
	}
	sort.Strings(list)
	for _, r := range list {
		p.byHand = append(p.byHand, fmt.Sprintf("git -C %q worktree prune   (drops the removed worktrees' git bookkeeping; not run)", r))
	}
}
