package uninstall

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// groupOrder is the print order.
var groupOrder = []struct{ id, title string }{
	{GroupSession, "session state under the AMQ root's extensions/amq-squad"},
	{GroupSeatCfg, "per-seat config dirs"},
	{GroupTools, "the core tools dir (the gh and claude copies)"},
	{GroupWorktree, "worktrees the core created under .claude/worktrees"},
	{GroupOwner, "owner files under ~/.amq-squad"},
	{GroupSealed, "sealed ledgers (commits ledger, exits queue, outcome log, hook ledger, session seed)"},
	{GroupEvidence, "evidence refs under refs/amq-squad/evidence in the seat repositories"},
	{GroupGate, "clear records and the gate store"},
}

// runOrder: evidence refs go before the worktrees and ledgers that locate
// them, the records (session state, ledgers, gate store) after everything
// they describe.
var runOrder = []string{GroupEvidence, GroupWorktree, GroupSeatCfg, GroupTools, GroupSession, GroupSealed, GroupGate, GroupOwner}

func applies(group string, o Options) bool {
	switch group {
	case GroupOwner:
		return o.OwnerFiles
	case GroupSealed, GroupEvidence, GroupGate:
		return o.Purge
	}
	return true
}

func status(group string, o Options) string {
	switch {
	case !applies(group, o) && group == GroupOwner:
		return "kept: removed only with --write --owner-files"
	case !applies(group, o):
		return "kept by default: removed only with --write --purge"
	case o.Write:
		return "removing"
	}
	return "dry run: removed with --write"
}

// fill turns the plan into the printed groups.
func (r *Result) fill(p *plan, o Options) {
	r.ByHand, r.Left, r.NotRemoved = p.byHand, p.left, p.kept
	for _, g := range groupOrder {
		grp := Group{ID: g.id, Title: g.title, Status: status(g.id, o)}
		for _, op := range p.ops {
			if op.group != g.id {
				continue
			}
			if len(op.lines) > 0 {
				for _, l := range op.lines {
					grp.Entries = append(grp.Entries, Entry{Path: l})
				}
				continue
			}
			grp.Entries = append(grp.Entries, Entry{Path: op.display, Note: op.note, Secret: op.secret})
		}
		if len(grp.Entries) > 0 {
			r.Groups = append(r.Groups, grp)
		}
	}
	r.NothingToDo = true
	for _, op := range p.ops {
		if applies(op.group, o) {
			r.NothingToDo = false
		}
	}
}

// execute runs the plan: the secrets first and unconditionally, then each
// group in run order. A failed entry is recorded and the rest of its group
// still runs, but a failed group stops the removal of the records that name
// it: a failed evidence group keeps the worktrees, ledgers and staged records
// (the next run rediscovers the refs from them), a failed worktree group
// keeps the records. Any failure is the partial error with the report of what
// remains.
func execute(p *plan, o Options, r *Result) error {
	var failed bool
	var symlink error
	failedGroup := map[string]bool{}
	fail := func(op op, why string, err error) {
		failed = true
		failedGroup[op.group] = true
		d := op.display
		if len(op.lines) > 0 {
			d = strings.Join(op.lines, ", ")
		}
		r.Remaining = append(r.Remaining, d)
		r.Failures = append(r.Failures, d+": "+why)
		var ce *Error
		if errors.As(err, &ce) && ce.Code == CodeSymlink && symlink == nil {
			symlink = ce
		}
	}
	run := func(op op) {
		var err error
		switch {
		case op.ev != nil && op.ev.err != nil:
			err = op.ev.err
		case op.ev != nil:
			err = seatcommit.DeleteEvidenceRefs(o.git(), op.ev.checkouts, op.ev.refs)
		default:
			var note string
			note, err = remove(op)
			if note != "" {
				r.Notes = append(r.Notes, note)
			}
		}
		if err != nil {
			fail(op, err.Error(), err)
			return
		}
		r.Removed = append(r.Removed, op.display)
	}
	if o.OwnerFiles {
		for _, op := range p.ops {
			if op.group == GroupOwner && op.secret {
				run(op)
			}
		}
	}
	for _, g := range runOrder {
		if !applies(g, o) {
			continue
		}
		for _, op := range p.ops {
			if op.group != g || op.secret && o.OwnerFiles {
				continue
			}
			if why := blockedBy(g, failedGroup); why != "" {
				fail(op, "kept because "+why+"; run again after fixing it", nil)
				continue
			}
			run(op)
		}
	}
	finishEmpty(o)
	if symlink != nil {
		return symlink
	}
	if failed {
		sort.Strings(r.Remaining)
		return &Error{Code: CodePartial, Detail: fmt.Sprintf("%d of the planned removals failed or were held back; what remains is listed", len(r.Remaining))}
	}
	return nil
}

// blockedBy: the records name what the failed groups were to remove.
func blockedBy(group string, failed map[string]bool) string {
	switch group {
	case GroupWorktree:
		if failed[GroupEvidence] {
			return "the evidence refs were not all deleted (the worktrees locate their repositories)"
		}
	case GroupSession, GroupSealed, GroupGate:
		if failed[GroupEvidence] {
			return "the evidence refs were not all deleted (these records name them)"
		}
		if failed[GroupWorktree] {
			return "a worktree was not removed (these records name it)"
		}
	}
	return ""
}

func (o Options) git() string {
	if o.Git != "" {
		return o.Git
	}
	return "git"
}

// finishEmpty removes the tools directory and the extension directory when
// nothing is left in them.
func finishEmpty(o Options) {
	for _, rel := range [][]string{{extDir, amqDir, "tools"}, {extDir, amqDir}} {
		d, err := openDir(o.Root, rel[:len(rel)-1])
		if err != nil {
			continue
		}
		if sub, ok := subOf(d, rel[len(rel)-1]); ok {
			names, err := sub.Names()
			sub.Close()
			if err == nil && len(names) == 0 {
				d.StageRemoveAll(rel[len(rel)-1])
			}
		}
		d.Close()
	}
}

// Render writes the report. It prints paths, never contents.
func (r Result) Render(w io.Writer) {
	mode := "dry run: nothing is changed; pass --write to remove"
	if r.Written {
		mode = "--write: removing"
	}
	fmt.Fprintf(w, "amq-squad uninstall (%s)\nAMQ root: %s\n", mode, r.Root)
	if r.NothingToDo {
		fmt.Fprintln(w, "\nnothing to do")
	}
	for _, g := range r.Groups {
		fmt.Fprintf(w, "\n%s [%s]\n", g.Title, g.Status)
		for _, e := range g.Entries {
			line := "  " + e.Path
			if e.Note != "" {
				line += "   (" + e.Note + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(r.NotRemoved) > 0 {
		fmt.Fprintln(w, "\nlisted, not removed (the task store, git and the checkout do not vouch for them)")
		for _, l := range r.NotRemoved {
			fmt.Fprintln(w, "  "+l)
		}
	}
	if len(r.Left) > 0 {
		fmt.Fprintln(w, "\nleft in place (not owned by this verb; remove by hand if you want them gone)")
		for _, l := range r.Left {
			fmt.Fprintln(w, "  "+l)
		}
	}
	fmt.Fprintln(w, "\ncommands and edits this verb will not run (do them by hand)")
	for _, l := range r.ByHand {
		fmt.Fprintln(w, "  "+l)
	}
	if r.Written {
		fmt.Fprintf(w, "\nremoved %d, remaining %d\n", len(r.Removed), len(r.Remaining))
		for _, n := range r.Notes {
			fmt.Fprintln(w, "  note: "+n)
		}
		for _, f := range r.Failures {
			fmt.Fprintln(w, "  failed: "+f)
		}
		if len(r.Remaining) > 0 {
			fmt.Fprintln(w, "what remains:")
			for _, p := range r.Remaining {
				fmt.Fprintln(w, "  "+p)
			}
		}
	}
}

// remove deletes one entry below its anchored parent: a symlink there is the
// coded refusal (never followed); only a missing entry counts as already
// gone, any other open error is a failed entry. A secret is overwritten once
// with zeros first (best effort: a copy-on-write file system may keep the old
// blocks), unless it has another hard link, which would be zeroed too: then
// it is unlinked as it is and the returned note says why.
func remove(o op) (string, error) {
	d, err := openDir(o.base, o.rel)
	if errors.Is(err, errAbsent) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer d.Close()
	if isLink(d, o.name) {
		return "", symlinkErr(o.display)
	}
	f, kind, _, _, err := d.OpenEntry(o.name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var note string
	zeroIt := false
	if f != nil {
		if o.secret && kind == fsanchor.KindRegular {
			if linkCount(f) > 1 {
				note = o.display + " has another hard link: unlinked without zeroing, so the other name keeps its contents"
			} else {
				zeroIt = true
			}
		}
		f.Close()
	}
	if zeroIt {
		if err := zero(d, o.name); err != nil {
			return note, fmt.Errorf("overwrite: %w", err)
		}
	}
	return note, d.StageRemoveAll(o.name)
}

func zero(d *fsanchor.AnchoredDir, name string) error {
	f, err := d.OpenLock(name)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for left := st.Size(); left > 0; {
		n := int64(len(buf))
		if left < n {
			n = left
		}
		if _, err := f.WriteAt(buf[:n], st.Size()-left); err != nil {
			return err
		}
		left -= n
	}
	return f.Sync()
}

// trustedHere is the codex trust entries that name this squad's checkouts.
func trustedHere(o Options, worktrees []string) ([]string, bool) {
	ct, err := hostcheck.ReadCodexTrust(codexConfig(o))
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if ct.Trusted[p] && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, w := range worktrees {
		add(w)
		if wt := parentOf(w); wt != "" {
			add(wt)
		}
	}
	sort.Strings(out)
	return out, ct.HasNotifyFanout()
}

func parentOf(w string) string {
	i := strings.LastIndex(w, "/.claude/worktrees/")
	if i <= 0 {
		return ""
	}
	return w[:i]
}

func codexConfig(o Options) string { return hostcheck.CodexConfigPath(o.Home, o.CodexHome) }
