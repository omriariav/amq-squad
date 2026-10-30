package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/catalog"
	"github.com/omriariav/amq-squad/v3/internal/drafter"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// role draft (brief 2b-5): a 3.0 role is a prompt (plan principle 5) for a
// squad.yaml seat; runtime, model and effort live in squad.yaml, not here.
var (
	roleDraftLineLimit      = 45
	roleDraftTaskIDPattern  = regexp.MustCompile(`(?i)(^|[^a-z0-9_-])t[0-9]+([^a-z0-9_-]|$)`)
	roleDraftVersionPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])v?[0-9]+\.[0-9]+\.[0-9]+([^a-z0-9]|$)`)
	roleDraftSections       = []string{"## Mission", "## Boundaries", "## Protocol"}
)

type roleDraftEnvelopeData struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Path    string `json:"path"`
	Staged  bool   `json:"staged"`
	cliDraft
}

const roleUsage = `amq-squad role draft <id> --purpose TEXT [--project DIR] [--json]

Drafts a reusable role prompt (shared drafter: global config, else in-session),
validates it and stages .amq-squad/roles/<id>.md for a squad.yaml seat. It never
overwrites a file or adds a seat; without a backend it prints the prompt only.
Example: amq-squad role draft researcher --purpose "Investigate ambiguous behavior"`

func runRole(args []string) error {
	if len(args) > 0 && args[0] == "draft" {
		return runRoleDraft(args[1:])
	} else if fmt.Fprintln(os.Stderr, roleUsage); len(args) == 0 {
		return usageErrorf("role requires a subcommand: draft")
	} else if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return flag.ErrHelp
	}
	return unknownSubcommandError("role", args[0], "draft")
}

func runRoleDraft(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return runRole(args[:min(1, len(args))]) // usage, help, or an unknown flag
	}
	id := strings.ToLower(strings.TrimSpace(args[0]))
	fs := flag.NewFlagSet("role draft", flag.ContinueOnError)
	purpose, project, jsonOut := fs.String("purpose", "", "one-line durable purpose"), fs.String("project", "", "repository root"), fs.Bool("json", false, "JSON")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, roleUsage) }
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if err := team.ValidateRoleID(id); err != nil || fs.NArg() != 0 || catalog.Lookup(id) != nil {
		return usageErrorf("role draft takes one custom role id (not a built-in persona) before its flags (%v)", err)
	}
	if p := strings.TrimSpace(*purpose); p == "" || len(p) > 500 || strings.ContainsAny(p, "\r\n") {
		return usageErrorf("--purpose is required, one line, at most 500 bytes")
	}
	projectDir, err := filepath.Abs(map[bool]string{true: ".", false: *project}[*project == ""])
	if err != nil {
		return err
	}
	data := roleDraftEnvelopeData{ID: id, Project: projectDir, Path: team.CustomRolePath(projectDir, id)}
	if _, err := os.Stat(data.Path); !os.IsNotExist(err) {
		return fmt.Errorf("role draft refuses to overwrite existing %s; review or move that file first (%v)", data.Path, err)
	}
	prompt := fmt.Sprintf(roleDraftPrompt, id, strings.Join(roleDraftSections, ", "), strings.TrimSpace(*purpose), roleDraftLineLimit)
	draft, err := runCLIDraft(projectDir, prompt, "role draft", "no file was staged", func(raw string) (string, error) {
		return validateRoleDraftDocument(raw, id)
	})
	if err != nil {
		return fmt.Errorf("draft role %q: %w", id, err)
	}
	if data.cliDraft, data.Staged = draft, !draft.Manual; data.Staged {
		if err := stageRoleDraft(data.Path, draft.Text); err != nil {
			return err
		}
	}
	switch {
	case *jsonOut:
		return printJSONEnvelope("role_draft", data)
	case draft.Manual:
		writeManualDraft(os.Stdout, "No role file was staged.", draft)
		fmt.Printf("\nReview the result and save it as %s.\n", data.Path)
	default:
		fmt.Printf("Wrote %s.\nDrafter config source: %s\n%s", data.Path, draft.ConfigSource, cliDrafterAttemptsText(draft.Attempts, draft.Evidence))
	}
	return nil
}

const roleDraftPrompt = `Draft one reusable amq-squad role prompt. Return only Markdown, no code fence, no commentary.
The first line must be exactly: # Role: %s
Then exactly these sections, in order: %s. Durable purpose: %s
Hard rules: under %d lines; version- and session-neutral (no release, session, task id, branch, issue or PR); live scope comes
from the active brief and the dispatched task, say so; concrete boundaries granting no merge, release, external-send, destructive-file,
approval or delegation authority; protocol: report ACK, progress, blockers and DONE to the task sender, touch only declared files.`

// validateRoleDraftDocument checks the drafted prompt's shape and neutrality.
func validateRoleDraftDocument(raw, id string) (string, error) {
	document, err := drafter.NormalizeText(raw, 64<<10, "document")
	if err != nil {
		return "", err
	}
	lines, lower := strings.Split(document, "\n"), strings.ToLower(document)
	var headings []string
	for _, line := range lines[1:] {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "#") {
			headings = append(headings, line)
		}
	}
	switch {
	case len(lines) >= roleDraftLineLimit:
		return "", fmt.Errorf("document has %d lines; must be under %d", len(lines), roleDraftLineLimit)
	case strings.TrimSpace(lines[0]) != "# Role: "+id:
		return "", fmt.Errorf("first line must be %q", "# Role: "+id)
	case strings.Join(headings, "|") != strings.Join(roleDraftSections, "|"):
		return "", fmt.Errorf("headings %v, want exactly %v in order", headings, roleDraftSections)
	case roleDraftTaskIDPattern.MatchString(document) || roleDraftVersionPattern.MatchString(document):
		return "", fmt.Errorf("document contains a task id or a version and is not reusable")
	case !strings.Contains(lower, "brief") || !strings.Contains(lower, "task"):
		return "", fmt.Errorf("document must defer runtime scope to the active brief and dispatched task")
	}
	return document + "\n", nil
}

// stageRoleDraft publishes the document with an exclusive create (0600).
func stageRoleDraft(path, document string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("role draft refuses to overwrite existing %s: %w", path, err)
	}
	_, err = f.WriteString(document)
	if err = errors.Join(err, f.Close()); err != nil {
		os.Remove(path)
	}
	return err
}
