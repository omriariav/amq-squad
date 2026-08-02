package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// memberFieldChange is one field-level before/after pair in a `team member
// update --dry-run` preview (#616).
type memberFieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// memberFieldUnset is what an empty field renders as. An empty string in a
// diff column is ambiguous — it reads as "unchanged" or as "the renderer had
// nothing to say" — and clearing a field is one of the edits an operator most
// wants confirmed before it happens.
const memberFieldUnset = "(unset)"

// memberDiffFields is the operator-visible surface of `team member update`, in
// a fixed display order. It is deliberately a curated list rather than
// reflection over team.Member: the struct also carries derived and internal
// bookkeeping (spawn origin/depth, tool policy drift) that this command cannot
// change and that would be noise in a review of a risky lead edit.
//
// Values are read from the ACTUAL before/after members rather than from the
// flags that were set. That matters for --effort, which the operator passes as
// one flag but which lands in claude_args/codex_args: a flag-driven diff would
// report "effort" and hide what was really written to the profile.
var memberDiffFields = []struct {
	name  string
	value func(team.Member) string
}{
	{"binary", func(m team.Member) string { return displayMemberScalar(m.Binary) }},
	{"handle", func(m team.Member) string { return displayMemberScalar(m.Handle) }},
	{"session", func(m team.Member) string { return displayMemberScalar(m.Session) }},
	{"model", func(m team.Member) string { return displayMemberScalar(m.Model) }},
	{"cwd", func(m team.Member) string { return displayMemberScalar(m.CWD) }},
	{"actor_mode", func(m team.Member) string { return displayMemberScalar(m.ActorMode) }},
	{"claude_args", func(m team.Member) string { return displayMemberArgs(m.ClaudeArgs) }},
	{"codex_args", func(m team.Member) string { return displayMemberArgs(m.CodexArgs) }},
}

// memberFieldChanges returns only the fields that actually differ. An update
// that changes nothing returns an empty slice, which callers must report as
// such rather than printing an empty table.
func memberFieldChanges(before, after team.Member) []memberFieldChange {
	var changes []memberFieldChange
	for _, field := range memberDiffFields {
		was, now := field.value(before), field.value(after)
		if was == now {
			continue
		}
		changes = append(changes, memberFieldChange{
			Field:  field.name,
			Before: displayMemberFieldValue(was),
			After:  displayMemberFieldValue(now),
		})
	}
	return changes
}

// displayMemberArgs renders native argv so that token boundaries are visible.
//
// A plain strings.Join is ambiguous in a way that silently loses edits: the
// two-token ["--settings", "/a b/x.json"] and the three-token
// ["--settings", "/a", "b/x.json"] join to the identical string, so replacing
// one with the other compares equal and the field disappears from the diff
// entirely. The operator sees "no field would change" for a real change, with
// exit 0 and well-formed output — the same silent-wrong-answer class as
// capturing the before-member too late.
//
// Quoting each token per shell rules keeps genuinely different argv genuinely
// different, and has the side benefit that the rendered value is pasteable.
func displayMemberArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// displayMemberScalar renders a single-valued field so that no set value can
// ever render as the unset sentinel.
//
// Returning set values verbatim was not injective: memberFieldUnset is the
// literal "(unset)", so a genuine edit from model="" to model="(unset)" rendered
// Before and After identically. The preview reported one change and hid what it
// was — the same silent-wrong-answer class as collapsing argv boundaries, one
// field over.
//
// shellQuote supplies the injectivity for free and matches how argv is already
// rendered: ordinary values stay bare (gpt-5), and anything containing shell
// metacharacters — parentheses included — comes back quoted, so a literal
// "(unset)" displays as '(unset)' and cannot be confused with the sentinel.
func displayMemberScalar(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return shellQuote(v)
}

func displayMemberFieldValue(v string) string {
	if strings.TrimSpace(v) == "" {
		return memberFieldUnset
	}
	return v
}

// writeMemberUpdatePreview renders the dry-run preview: a header naming the
// member and profile, then one aligned row per changed field.
//
// Column widths are computed from the actual values because member cwd is an
// absolute worktree path and model/args are unbounded; a fixed-width table
// would wrap exactly on the rows an operator most needs to read.
func writeMemberUpdatePreview(w io.Writer, role, profile string, changes []memberFieldChange) {
	if len(changes) == 0 {
		fmt.Fprintf(w, "# preview: %s in profile %s is already in the requested state — no field would change, nothing would be written.\n", role, profile)
		return
	}
	noun := "fields"
	if len(changes) == 1 {
		noun = "field"
	}
	fmt.Fprintf(w, "# preview: update %s in profile %s — %d %s would change, nothing written yet.\n\n", role, profile, len(changes), noun)

	fieldWidth, beforeWidth := len("FIELD"), len("BEFORE")
	for _, c := range changes {
		if len(c.Field) > fieldWidth {
			fieldWidth = len(c.Field)
		}
		if len(c.Before) > beforeWidth {
			beforeWidth = len(c.Before)
		}
	}
	fmt.Fprintf(w, "  %-*s  %-*s  %s\n", fieldWidth, "FIELD", beforeWidth, "BEFORE", "AFTER")
	for _, c := range changes {
		fmt.Fprintf(w, "  %-*s  %-*s  %s\n", fieldWidth, c.Field, beforeWidth, c.Before, c.After)
	}
	fmt.Fprintf(w, "\nRe-run without --dry-run to apply.\n")
}
