package uninstall

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// groupOrder is the print order and, but for the secrets, the run order:
// evidence refs go before the worktrees and ledgers that locate them, the
// sealed ledgers after the session state they seal.
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
	r.ByHand, r.Left = p.byHand, p.left
	for _, g := range groupOrder {
		grp := Group{ID: g.id, Title: g.title, Status: status(g.id, o)}
		for _, op := range p.ops {
			if op.group == g.id {
				grp.Entries = append(grp.Entries, Entry{Path: op.display, Note: op.note, Secret: op.secret})
			}
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
// group in run order. A failed entry is recorded and the rest still run;
// any failure is the partial error with the report of what remains.
func execute(p *plan, o Options, r *Result) error {
	var failed bool
	var symlink error
	run := func(op op) {
		var err error
		if op.ev != nil {
			err = seatcommit.DeleteEvidenceRefs(o.git(), op.ev.checkouts, op.ev.refs)
		} else {
			err = remove(op)
		}
		if err != nil {
			var ce *Error
			if errors.As(err, &ce) && ce.Code == CodeSymlink && symlink == nil {
				symlink = ce
			}
			failed = true
			r.Remaining = append(r.Remaining, op.display)
			r.Failures = append(r.Failures, op.display+": "+err.Error())
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
			run(op)
		}
	}
	finishEmpty(o)
	if symlink != nil {
		return symlink
	}
	if failed {
		sort.Strings(r.Remaining)
		return &Error{Code: CodePartial, Detail: fmt.Sprintf("%d of the planned removals failed; what remains is listed", len(r.Remaining))}
	}
	return nil
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
			names, _ := sub.Names()
			sub.Close()
			if len(names) == 0 {
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
// coded refusal (never followed); a secret is overwritten once with zeros
// first (best effort: a copy-on-write file system may keep the old blocks).
func remove(o op) error {
	d, err := openDir(o.base, o.rel)
	if errors.Is(err, errAbsent) {
		return nil
	}
	if err != nil {
		return err
	}
	defer d.Close()
	if isLink(d, o.name) {
		return symlinkErr(o.display)
	}
	if _, ok := kindOf(d, o.name); !ok {
		return nil
	}
	if o.secret {
		if err := zero(d, o.name); err != nil {
			return fmt.Errorf("overwrite: %w", err)
		}
	}
	return d.StageRemoveAll(o.name)
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
