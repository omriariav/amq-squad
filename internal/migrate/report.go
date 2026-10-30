package migrate

import (
	"fmt"
	"strings"
)

// Report is the text migrate prints: what was read, the files it would write
// (or wrote), every unmapped v2 field, the v2 files that stay, the ones dead
// under decision 6, and the owner's next steps. It never carries a value
// from a v2 file.
func (p *Plan) Report(wrote bool) string {
	var b strings.Builder
	if wrote {
		b.WriteString("amq-squad migrate: wrote the files below. The v2 files are untouched.\n")
	} else {
		b.WriteString("amq-squad migrate: dry run. Nothing was written; pass --write to write the files below.\n")
	}
	fmt.Fprintf(&b, "\nv2 profile: %s (profile %q, project %s)\n", p.ProfilePath, p.ProfileName, p.Project)
	b.WriteString("\nread (read-only):\n")
	for _, f := range p.Read {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	verb := "would write"
	if wrote {
		verb = "wrote"
	}
	fmt.Fprintf(&b, "\n%s:\n", verb)
	for _, w := range p.Writes {
		fmt.Fprintf(&b, "  %s (%04o): %s\n", w.Path, uint32(w.Mode), w.Note)
	}
	fmt.Fprintf(&b, "\nunmapped v2 fields (no 3.0 meaning), %d:\n", len(p.Unmapped))
	if len(p.Unmapped) == 0 {
		b.WriteString("  none\n")
	}
	for _, u := range p.Unmapped {
		fmt.Fprintf(&b, "  %s: %s\n", u.Key, u.Reason)
	}
	if len(p.Kept) > 0 {
		b.WriteString("\nv2 files 3.0 keeps reading at the same path:\n")
		for _, f := range p.Kept {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	b.WriteString("\ndead under decision 6 (left in place; nothing is deleted):\n")
	for _, f := range p.Dead {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	fmt.Fprintf(&b, "  remove them, after squad.yaml is in place, with: amq-squad migrate --remove-v2 --write %s\n", p.ProfilePath)
	b.WriteString("\nowner steps (migrate runs none of them):\n")
	fmt.Fprintf(&b, "  1. read %s and move the lines you accept into grants.yaml\n", proposedName)
	if p.TrustCmd != "" {
		fmt.Fprintf(&b, "  2. the role prompts are not trusted yet; run: %s\n", p.TrustCmd)
	}
	b.WriteString("  next: amq-squad team check\n")
	return b.String()
}

// Report is the text of `migrate --remove-v2`.
func (r *RemovePlan) Report(wrote bool) string {
	var b strings.Builder
	if wrote {
		b.WriteString("amq-squad migrate --remove-v2: removed the v2 files below.\n")
	} else {
		b.WriteString("amq-squad migrate --remove-v2: dry run. Nothing was removed; pass --write to remove the files below.\n")
	}
	for _, f := range r.Files {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	return b.String()
}
