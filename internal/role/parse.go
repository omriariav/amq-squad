package role

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Definition is a custom role loaded from an external file. It carries the
// metadata amq-squad needs to treat the role as a first-class team member plus
// an optional Markdown Body that becomes the agent's role.md verbatim.
//
// Files come in two shapes:
//
//   - Markdown (.md/.markdown): optional YAML frontmatter for metadata, with
//     the remaining document used as the role.md Body. With no frontmatter the
//     whole file is the Body and the ID is derived from a "# Role: X" heading
//     or the filename.
//   - Metadata-only (.yaml/.yml/.json): structured fields only; Body is empty
//     and role.md is rendered from the stub at seed time.
type Definition struct {
	ID          string
	Label       string
	Binary      string
	Description string
	Skills      []string
	Peers       []string
	Body        string
	Source      string
}

// Stub converts the definition's metadata into a render Stub, used when there
// is no Body to write verbatim.
func (d Definition) Stub() Stub {
	return Stub{
		Label:       d.Label,
		RoleID:      d.ID,
		Description: d.Description,
		Skills:      d.Skills,
		Peers:       d.Peers,
	}
}

// Document returns the role.md content to persist for this definition: the
// verbatim Body when present, otherwise a rendered stub from the metadata.
func (d Definition) Document() string {
	if strings.TrimSpace(d.Body) != "" {
		return d.Body
	}
	return render(d.Stub())
}

// LooksLikeRoleFile reports whether a --roles/--personas token should be
// treated as a path to a role file rather than a catalog ID or custom slug.
// It is deliberately conservative: a bare slug like "researcher" is never a
// file, but "./researcher.md" or "roles/sre.yaml" is.
func LooksLikeRoleFile(token string) bool {
	t := strings.TrimSpace(token)
	if t == "" {
		return false
	}
	if strings.ContainsAny(t, "/\\") || strings.HasPrefix(t, "~") {
		return true
	}
	switch strings.ToLower(filepath.Ext(t)) {
	case ".md", ".markdown", ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// ParseFile loads a custom role definition from path. The ID is not validated
// here; callers apply their own slug rules (e.g. team.ValidateRoleID).
func ParseFile(path string) (Definition, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read role file: %w", err)
	}
	d, err := parse(string(raw), path)
	if err != nil {
		return Definition{}, fmt.Errorf("%s: %w", path, err)
	}
	d.Source = path
	if d.ID == "" {
		return Definition{}, fmt.Errorf("%s: could not determine a role id; add 'id:' or a '# Role: <name>' heading", path)
	}
	return d, nil
}

func parse(content, path string) (Definition, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		d, err := parseJSON(content)
		if err != nil {
			return Definition{}, err
		}
		if d.ID == "" {
			d.ID = slugify(baseName(path))
		}
		return d, nil
	case ".yaml", ".yml":
		fields, err := parseYAMLish(content)
		if err != nil {
			return Definition{}, err
		}
		d := definitionFromFields(fields)
		if d.ID == "" {
			d.ID = slugify(baseName(path))
		}
		return d, nil
	default: // .md, .markdown, or extensionless: frontmatter + body
		front, body := splitFrontmatter(content)
		var d Definition
		if front != "" {
			fields, err := parseYAMLish(front)
			if err != nil {
				return Definition{}, err
			}
			d = definitionFromFields(fields)
		}
		d.Body = strings.TrimRight(body, "\n") + "\n"
		if strings.TrimSpace(body) == "" {
			d.Body = ""
		}
		if d.ID == "" {
			d.ID = slugify(headingName(body))
		}
		if d.ID == "" {
			d.ID = slugify(baseName(path))
		}
		if d.Label == "" {
			if h := headingName(body); h != "" {
				d.Label = h
			}
		}
		return d, nil
	}
}

type jsonRole struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Binary      string   `json:"binary"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
	Peers       []string `json:"peers"`
	Body        string   `json:"body"`
}

func parseJSON(content string) (Definition, error) {
	var jr jsonRole
	if err := json.Unmarshal([]byte(content), &jr); err != nil {
		return Definition{}, fmt.Errorf("parse json role: %w", err)
	}
	return Definition{
		ID:          strings.TrimSpace(strings.ToLower(jr.ID)),
		Label:       strings.TrimSpace(jr.Label),
		Binary:      strings.TrimSpace(jr.Binary),
		Description: strings.TrimSpace(jr.Description),
		Skills:      trimAll(jr.Skills),
		Peers:       trimAll(jr.Peers),
		Body:        jr.Body,
	}, nil
}

func definitionFromFields(f map[string][]string) Definition {
	d := Definition{
		ID:          firstLower(f["id"]),
		Label:       first(f["label"]),
		Binary:      strings.TrimSpace(first(f["binary"])),
		Description: first(f["description"]),
		Skills:      f["skills"],
		Peers:       f["peers"],
	}
	if d.ID == "" {
		d.ID = firstLower(f["role"]) // tolerate `role:` as an alias for `id:`
	}
	return d
}

// splitFrontmatter separates a leading `---`-delimited YAML block from the
// document body. With no frontmatter it returns ("", content).
func splitFrontmatter(content string) (front, body string) {
	s := strings.TrimPrefix(content, "\ufeff") // tolerate a UTF-8 BOM
	if !strings.HasPrefix(s, "---") {
		return "", s
	}
	// First line must be exactly the opening fence.
	nl := strings.IndexByte(s, '\n')
	if nl < 0 || strings.TrimSpace(s[:nl]) != "---" {
		return "", s
	}
	rest := s[nl+1:]
	// Find the closing fence on its own line.
	lines := strings.Split(rest, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "---" || strings.TrimSpace(ln) == "..." {
			front = strings.Join(lines[:i], "\n")
			body = strings.Join(lines[i+1:], "\n")
			return front, body
		}
	}
	// Unterminated frontmatter: treat the whole thing as body.
	return "", s
}

// parseYAMLish parses the flat subset of YAML amq-squad role files use:
// `key: scalar`, `key: [a, b]` inline lists, and block lists of `- item`.
// Values may be quoted. Full-line `#` comments and blank lines are ignored.
// Each key maps to its values as a slice (scalars are single-element slices).
func parseYAMLish(text string) (map[string][]string, error) {
	out := map[string][]string{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Block list continuation handled by lookahead below; a stray "- x"
		// without a preceding key is ignored.
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			continue
		}
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			return nil, fmt.Errorf("invalid metadata line %q (expected key: value)", trimmed)
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
		val := strings.TrimSpace(trimmed[colon+1:])
		if key == "" {
			return nil, fmt.Errorf("invalid metadata line %q (empty key)", trimmed)
		}
		if val == "" {
			// Possible block list: consume following indented "- item" lines.
			var items []string
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				if strings.HasPrefix(next, "- ") || next == "-" {
					items = append(items, unquote(strings.TrimSpace(strings.TrimPrefix(next, "-"))))
					i = j
					continue
				}
				break
			}
			out[key] = trimAll(items)
			continue
		}
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			out[key] = trimAll(splitInlineList(val[1 : len(val)-1]))
			continue
		}
		out[key] = []string{unquote(val)}
	}
	return out, nil
}

func splitInlineList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = unquote(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func first(in []string) string {
	if len(in) == 0 {
		return ""
	}
	return strings.TrimSpace(in[0])
}

func firstLower(in []string) string {
	return strings.ToLower(first(in))
}

// headingName extracts the role name from a leading Markdown heading, honoring
// the "# Role: X" convention render() emits and falling back to a plain "# X".
func headingName(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "#") {
			return ""
		}
		t = strings.TrimLeft(t, "#")
		t = strings.TrimSpace(t)
		if i := strings.Index(strings.ToLower(t), "role:"); i == 0 {
			return strings.TrimSpace(t[len("role:"):])
		}
		return t
	}
	return ""
}

func baseName(path string) string {
	b := filepath.Base(path)
	return strings.TrimSuffix(b, filepath.Ext(b))
}

// slugify turns a human label into a role slug: lowercase, spaces and runs of
// punctuation collapsed to single hyphens, restricted to [a-z0-9_-].
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			prevHyphen = false
		default:
			// Any other character (hyphen, spaces, punctuation, symbols)
			// collapses to a single hyphen separator.
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
