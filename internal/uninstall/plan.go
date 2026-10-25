package uninstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
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
	lines   []string // display lines of an evidence op, one per ref
	note    string
	secret  bool
	base    string
	rel     []string
	name    string
	ev      *evidenceOp
}

// evidenceOp deletes the evidence refs git lists in one repository.
type evidenceOp struct {
	checkouts []string
	refs      []seatcommit.EvidenceRefInfo
	err       error // the refs could not be listed: the entry fails
}

// wtCand is a worktree a ledger or a staged record names for a seat.
type wtCand struct{ handle, path string }

type plan struct {
	ops     []op
	byHand  []string
	left    []string
	kept    []string // worktrees listed but not removed, with the reason
	handles map[string]bool
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

// kindOf reports what name is below d without following a link. Only a
// missing entry is absent (exists false, err nil); any other open error is
// returned.
func kindOf(d *fsanchor.AnchoredDir, name string) (fsanchor.EntryKind, bool, error) {
	f, kind, _, _, err := d.OpenEntry(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fsanchor.KindOther, false, nil
		}
		return fsanchor.KindOther, false, err
	}
	if f != nil {
		f.Close()
	}
	return kind, true, nil
}

// subOf opens a child directory; ok is false when it is not a directory or
// cannot be opened (the caller decides whether that matters).
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

// noLinks opens every directory below d, to depth levels, through the
// descriptor walk: a symlink at any of them refuses the whole plan.
func noLinks(d *fsanchor.AnchoredDir, disp string, depth int) error {
	for _, n := range sortedNames(d) {
		if isLink(d, n) {
			return symlinkErr(filepath.Join(disp, n))
		}
		if depth <= 1 {
			continue
		}
		if s, ok := subOf(d, n); ok {
			err := noLinks(s, filepath.Join(disp, n), depth-1)
			s.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// discover reads the layout (read only) and returns the removal plan. A
// symlink where a directory is expected, anywhere in the layout, nested ones
// included, refuses the whole plan before anything is removed.
func discover(o Options) (*plan, error) {
	p := &plan{handles: map[string]bool{}}
	var checkouts []string
	var cands []wtCand
	var seeds []evidenceSeed
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
			disp := filepath.Join(xpath, name)
			if isLink(x, name) {
				return nil, symlinkErr(disp)
			}
			if sub, ok := subOf(x, name); ok {
				err := noLinks(sub, disp, 3)
				sub.Close()
				if err != nil {
					return nil, err
				}
			}
			switch {
			case name == "launch":
				w, err := p.launch(o.Root, x, xrel, disp)
				if err != nil {
					return nil, err
				}
				cands = append(cands, w...)
			case name == "scheduler":
				p.scheduler(o.Root, x, xrel, disp)
			case name == "tools":
				p.children(o.Root, x, xrel, name, GroupTools)
			case sealedNames[name]:
				if name == "commits" {
					ws, sd := p.commits(x, o.Root, xrel, checkouts)
					cands = append(cands, ws...)
					seeds = append(seeds, sd...)
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
	p.evidence(o, seeds)
	if err := p.worktreeOps(o, cands); err != nil {
		return nil, err
	}
	if err := p.ownerOps(o); err != nil {
		return nil, err
	}
	p.manual(o, p.removableWorktrees())
	return p, nil
}

var sealNote = map[string]string{
	"commits": "the commits ledger (sealed)",
	"ledger":  "the hook ledger",
	"routing": "the outcome log and the session seed that seals the ledgers",
}

// children adds one op per entry of x/name.
func (p *plan) children(root string, x *fsanchor.AnchoredDir, xrel []string, name, group string) {
	sd, ok := subOf(x, name)
	if !ok {
		p.ops = append(p.ops, op{group: group, display: filepath.Join(root, extDir, amqDir, name), base: root, rel: xrel, name: name})
		return
	}
	defer sd.Close()
	rel := append(append([]string{}, xrel...), name)
	for _, c := range sortedNames(sd) {
		p.ops = append(p.ops, op{group: group, display: filepath.Join(root, extDir, amqDir, name, c), base: root, rel: rel, name: c})
	}
}

// launch lists the staged state of the launch directory: the per-seat
// config dirs of every launch as their own group, the directory itself as
// session state, and returns the worktrees the staged records name.
func (p *plan) launch(root string, x *fsanchor.AnchoredDir, xrel []string, disp string) ([]wtCand, error) {
	var cands []wtCand
	ld, ok := subOf(x, "launch")
	if ok {
		defer ld.Close()
		for _, h := range sortedNames(ld) {
			hd, ok := subOf(ld, h)
			if !ok {
				continue
			}
			var rec launch.Record
			if readJSON(hd, launch.FileName, &rec) && rec.Worktree != "" {
				cands = append(cands, wtCand{h, rec.Worktree})
			}
			if cd, ok := subOf(hd, "claude-config"); ok {
				rel := append(append([]string{}, xrel...), "launch", h, "claude-config")
				for _, id := range sortedNames(cd) {
					p.ops = append(p.ops, op{group: GroupSeatCfg, display: filepath.Join(disp, h, "claude-config", id), base: root, rel: rel, name: id})
				}
				cd.Close()
			}
			hd.Close()
		}
	}
	p.ops = append(p.ops, op{group: GroupSession, display: disp, note: "staged launch records, proofs, alarms", base: root, rel: xrel, name: "launch"})
	return cands, nil
}

// scheduler lists the scheduler directory: the exits queue is a sealed
// ledger, everything else session state.
func (p *plan) scheduler(root string, x *fsanchor.AnchoredDir, xrel []string, disp string) {
	sd, ok := subOf(x, "scheduler")
	if !ok {
		p.ops = append(p.ops, op{group: GroupSession, display: disp, base: root, rel: xrel, name: "scheduler"})
		return
	}
	defer sd.Close()
	rel := append(append([]string{}, xrel...), "scheduler")
	for _, c := range sortedNames(sd) {
		o := op{group: GroupSession, display: filepath.Join(disp, c), base: root, rel: rel, name: c}
		if c == "exits.json" {
			o.group, o.note = GroupSealed, "the exits queue (sealed)"
		}
		p.ops = append(p.ops, o)
	}
}

// evidenceSeed is what one commits ledger contributes to the evidence
// enumeration: the checkouts that can reach its repository.
type evidenceSeed struct {
	handle    string
	checkouts []string
	err       error
}

// commits reads the commit ledgers for the worktrees they name. The refs
// themselves are never taken from a ledger: git is asked (evidence).
func (p *plan) commits(x *fsanchor.AnchoredDir, root string, xrel, checkouts []string) ([]wtCand, []evidenceSeed) {
	cd, ok := subOf(x, "commits")
	if !ok {
		return nil, nil
	}
	defer cd.Close()
	var cands []wtCand
	var seeds []evidenceSeed
	for _, n := range sortedNames(cd) {
		var l seatcommit.Ledger
		if !readJSON(cd, n, &l) {
			seeds = append(seeds, evidenceSeed{handle: n, err: fmt.Errorf("the commits ledger %s cannot be read, so its evidence refs cannot be located", n)})
			continue
		}
		p.handles[l.Handle] = true
		if l.Worktree != "" {
			cands = append(cands, wtCand{l.Handle, l.Worktree})
		}
		seeds = append(seeds, evidenceSeed{handle: l.Handle, checkouts: append([]string{l.Worktree}, checkouts...)})
	}
	return cands, seeds
}

// evidence lists the evidence refs of the seats' repositories from git itself
// (for-each-ref), one op per repository. A symbolic ref is shown as refused.
func (p *plan) evidence(o Options, seeds []evidenceSeed) {
	seen := map[string]bool{}
	for _, s := range seeds {
		if s.err != nil {
			p.ops = append(p.ops, op{group: GroupEvidence, display: s.err.Error(), ev: &evidenceOp{err: s.err}})
			continue
		}
		common, refs, err := seatcommit.ListEvidenceRefs(o.git(), s.checkouts)
		if err != nil {
			p.ops = append(p.ops, op{group: GroupEvidence, display: "evidence refs of " + s.handle + ": " + err.Error(), ev: &evidenceOp{err: err}})
			continue
		}
		if seen[common] {
			continue
		}
		seen[common] = true
		var keep []seatcommit.EvidenceRefInfo
		var lines []string
		for _, r := range refs {
			if !p.ownsRef(r.Name) {
				continue
			}
			keep = append(keep, r)
			line := r.Name + " (" + common + ")"
			if r.Symref != "" {
				line += "   REFUSED: a symbolic ref to " + r.Symref
			}
			lines = append(lines, line)
		}
		if len(keep) > 0 {
			p.ops = append(p.ops, op{group: GroupEvidence, display: common, lines: lines, ev: &evidenceOp{checkouts: s.checkouts, refs: keep}})
		}
	}
}

// ownsRef: the ref's seat segment is one of this squad's seats.
func (p *plan) ownsRef(name string) bool {
	seg, _, _ := strings.Cut(strings.TrimPrefix(name, seatcommit.EvidencePrefix), "/")
	for h := range p.handles {
		if seatcommit.EvidenceSeatSegment(h) == seg {
			return true
		}
	}
	return false
}

// seatProjects are the project paths the task store records for a seat: the
// only projects whose worktrees the seat may have had.
func seatProjects(tasksDir string) (func(handle string) map[string]bool, error) {
	list, err := task.Snapshot(tasksDir)
	if err != nil {
		return nil, err
	}
	return func(h string) map[string]bool {
		out := map[string]bool{}
		for _, t := range list {
			involved := t.Lease != nil && t.Lease.Holder == h || t.Report != nil && t.Report.By == h
			for _, a := range t.Attempts {
				involved = involved || a.Holder == h
			}
			if involved && t.ProjectPath != "" {
				out[filepath.Clean(t.ProjectPath)] = true
			}
		}
		return out
	}, nil
}

// worktreeOps plans the worktrees the core created. A candidate is removed
// only when its project is a project_path the task store records for the
// seat, the project's `git worktree list` names it, and its .git resolves
// back into that project; anything else is listed as not removed.
func (p *plan) worktreeOps(o Options, cands []wtCand) error {
	projectsOf, perr := seatProjects(filepath.Join(o.Root, extDir, amqDir, "tasks"))
	seen := map[string]bool{}
	sort.Slice(cands, func(i, j int) bool { return cands[i].path < cands[j].path })
	for _, c := range cands {
		w := c.path
		if !filepath.IsAbs(w) || filepath.Clean(w) != w || seen[w] {
			continue
		}
		seen[w] = true
		wt := filepath.Dir(w)
		if filepath.Base(wt) != "worktrees" || filepath.Base(filepath.Dir(wt)) != ".claude" {
			continue // not one the core created under .claude/worktrees
		}
		proj := filepath.Dir(filepath.Dir(wt))
		skip := func(why string) { p.kept = append(p.kept, w+": not removed, "+why) }
		if perr != nil {
			skip("the task store cannot be read: " + perr.Error())
			continue
		}
		if !projectsOf(c.handle)[proj] {
			skip("its project is not a project_path the task store records for seat " + c.handle)
			continue
		}
		rel := []string{".claude", "worktrees"}
		d, err := openDir(proj, rel)
		if errors.Is(err, errAbsent) {
			continue
		}
		if err != nil {
			return err
		}
		name := filepath.Base(w)
		if isLink(d, name) {
			d.Close()
			return symlinkErr(w)
		}
		kind, ok, kerr := kindOf(d, name)
		d.Close()
		switch {
		case kerr != nil:
			skip("it cannot be opened: " + kerr.Error())
		case !ok || kind != fsanchor.KindDir:
		default:
			if err := seatcommit.VerifyWorktree(o.git(), proj, w); err != nil {
				skip(err.Error())
				continue
			}
			p.ops = append(p.ops, op{group: GroupWorktree, display: w, note: "uncommitted work in it is lost", base: proj, rel: rel, name: name})
		}
	}
	return nil
}

func (p *plan) removableWorktrees() []string {
	var out []string
	for _, o := range p.ops {
		if o.group == GroupWorktree {
			out = append(out, o.display)
		}
	}
	return out
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
		kind, ok, kerr := kindOf(d, f.name)
		if kerr == nil && (!ok || kind != fsanchor.KindRegular) {
			continue
		}
		note := ""
		if f.secret {
			note = "secret: overwritten once with zeros (unless hard-linked), then unlinked; never printed"
		}
		// an entry that cannot be opened is still planned: removing it fails
		// and is reported, never assumed gone
		p.ops = append(p.ops, op{group: GroupOwner, display: disp, secret: f.secret, note: note, base: o.Home, rel: rel, name: f.name})
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
		repos[filepath.Dir(filepath.Dir(wt))] = true
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
