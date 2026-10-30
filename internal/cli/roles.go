package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v3/internal/catalog"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// roleEnvelope is one role a squad.yaml seat may name: a catalog role, or a
// custom role staged at .amq-squad/roles/<id>.md (amq-squad role draft).
type roleEnvelope struct {
	ID              string `json:"id"`
	Label           string `json:"label,omitempty"`
	PreferredBinary string `json:"preferred_binary,omitempty"`
	Description     string `json:"description,omitempty"`
	Source          string `json:"source"` // catalog or the staged file
}

func runRoles(args []string) error {
	fs := flag.NewFlagSet("roles", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope instead of the table")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "amq-squad roles [--json] - list the roles a squad.yaml seat may name (catalog and staged custom roles)\n\nExamples:\n  amq-squad roles\n  amq-squad roles --json\n")
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("roles takes no positional arguments")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	roles := rolesData(cwd)
	if *jsonOut {
		return printJSONEnvelope("roles", struct {
			Roles []roleEnvelope `json:"roles"`
		}{roles})
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tDEFAULT RUNTIME\tSOURCE")
	for _, r := range roles {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.ID, r.PreferredBinary, r.Source)
	}
	return w.Flush()
}

func rolesData(projectDir string) []roleEnvelope {
	var out []roleEnvelope
	for _, r := range catalog.All() {
		out = append(out, roleEnvelope{ID: r.ID, Label: r.Label, PreferredBinary: r.PreferredBinary, Description: r.Description, Source: "catalog"})
	}
	staged, _ := filepath.Glob(filepath.Join(team.RolesDir(projectDir), "*.md"))
	sort.Strings(staged)
	for _, path := range staged {
		if id := strings.TrimSuffix(filepath.Base(path), ".md"); team.ValidateRoleID(id) == nil && catalog.Lookup(id) == nil {
			out = append(out, roleEnvelope{ID: id, Source: path})
		}
	}
	return out
}
