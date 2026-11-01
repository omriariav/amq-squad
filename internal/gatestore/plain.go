package gatestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// The gate question in plain words (brief 3.1.1-1, the owner's ruling of
// 2026-11-01: "simple language, not these hash and unclear language"). The
// question leads with three plain lines: what is asked, why now, and what
// yes and no do. A rule follows, then the identifiers for the record (the
// gate id, the squad, the class, the target line and the full digest),
// unchanged for the verifier. The owner answers with a bare yes or no
// replied in the question's thread; the thread and the signature under the
// pinned owner key are the binding, so no text asks for a typed tuple.
//
// Every word of the three lines comes from a field the composer read: the
// record's action, target and raiser, and the Facts a raiser read from its
// own records (the merge train's refused rules are in the target; the
// release prepare's commit check gives the branch; a widen's refusal is its
// target's limit). A question never asserts what nobody recorded.

// Rule separates the plain lines from the identifiers. It is shorter than
// the relay's 16-character question-line match (buzz.MinQuestionLine), so a
// lead's reply with a markdown rule is never refused for it.
const Rule = "----------"

// TitleMax is the length a quoted pull request or task title is cut at; a
// longer title ends with an ellipsis.
const TitleMax = 80

// Facts are what a raiser read from its own records for the question's plain
// lines, beyond the target: the pull request's or the task's title (shown
// quoted on its own line), and the branch: a merge's base branch, or the
// branch the release prepare found the tag's commit on.
type Facts struct {
	Title  string `json:"title,omitempty"`
	Branch string `json:"branch,omitempty"`
}

var branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

// normalize is nil for empty facts, so a record holds facts only when it
// has one.
// The title is stored redacted (redact.Block): the record, `gate status`
// and every reader see only the redacted text.
func (f *Facts) normalize() *Facts {
	if f == nil || (f.Title == "" && f.Branch == "") {
		return nil
	}
	c := *f
	c.Title = redact.Block(c.Title)
	return &c
}

func (f *Facts) validate() error {
	if f == nil {
		return nil
	}
	if f.Title == "" && f.Branch == "" {
		return errors.New("gate facts are empty (omit them)")
	}
	if f.Title != "" {
		if f.Title != redact.Block(f.Title) {
			return errors.New("gate facts title is not redacted")
		}
		if err := operatorauth.ValidateCanonicalSingleLineField("title", f.Title, true); err != nil {
			return err
		}
		if len(f.Title) > 512 {
			return errors.New("gate facts title is longer than 512 bytes")
		}
	}
	if f.Branch != "" && (!branchRe.MatchString(f.Branch) || strings.Contains(f.Branch, "..")) {
		return fmt.Errorf("gate facts branch %q is not a branch name", f.Branch)
	}
	return nil
}

// SafeFacts are a raiser's facts with every value that would not validate
// left out (runs of blanks in a title made one space): what a raiser read
// never stops its gate. nil when none is left.
func SafeFacts(title, branch string) *Facts {
	f := Facts{Title: redact.Block(strings.Join(strings.Fields(title), " ")), Branch: branch}
	if (&Facts{Title: f.Title}).validate() != nil {
		f.Title = ""
	}
	if (&Facts{Branch: f.Branch}).validate() != nil {
		f.Branch = ""
	}
	return f.normalize()
}

// ClassWords are the classes in words, the one fixed list the plain lines
// name a class from (never a typed-class token above the rule).
// The check reads them case-insensitively anywhere above the rule.
var ClassWords = []string{"tag", "GitHub release", "package release", "message outside the squad", "credential", "growth past the grant", "widen", "merge"}

// TextOf is the only renderer of the owner-facing gate text: the plain lines,
// a blank line, the rule, and the identifiers. The whole text passes
// redact.Block (Codex P1 on #838): a credential-shaped value in a title or a
// target never reaches the question, its record, status or either display.
// It is a function of the record's fields, so Validate's re-render agrees.
func TextOf(r Record) string {
	return redact.Block(strings.Join(append(PlainLines(r), "", Rule, identifiers(r.Squad, r.GateID, r.TypedClass, r.Class, r.Target, r.Digest)), "\n"))
}

// identifiers are the lines below the rule: the gate id, the squad, the
// class and action, the target line and the full digest (whose first 16 hex
// are the prefix the typed form names).
func identifiers(squad, id, typedClass, action, target, digest string) string {
	head := []string{"amq-squad gate " + id}
	if squad != "" {
		head = append(head, "squad: "+squad)
	}
	return strings.Join(append(head, "class: "+typedClass+" ("+action+")", "target: "+target, "digest: "+digest), "\n")
}

// Plain is the text above the rule of a gate text (its plain lines), "" for
// a text with no rule (a record raised before 3.1.1).
func Plain(text string) string {
	above, _, ok := strings.Cut(text, "\n\n"+Rule+"\n")
	if !ok {
		return ""
	}
	return above
}

// PlainLines are the question's plain lines: what is asked (a question),
// the quoted title when the raiser read one, why now, and what yes and no
// do.
func PlainLines(r Record) []string {
	q := compose(r)
	lines := []string{q.ask}
	if q.title != "" {
		lines = append(lines, q.title)
	}
	lines = append(lines, q.why, q.answer)
	for i, l := range lines {
		lines[i] = redact.Block(l)
	}
	return lines
}

type plainQ struct{ ask, title, why, answer string }

const (
	answerRun = "Yes lets it run. No stops it."
)

// compose reads the record's action, target, raiser and facts. Each shape is
// a gatetarget convention (internal/gatetarget's table).
func compose(r Record) plainQ {
	f := r.Facts
	if f == nil {
		f = &Facts{}
	}
	asked := "Asked by " + plain(r.RaisedBy) + "."
	prepared := asked
	if f.Branch != "" {
		prepared = "The prepare found its commit on " + plain(f.Branch) + "."
	}
	title := ""
	if f.Title != "" {
		title = quote(f.Title)
	}
	t := r.Target
	w := strings.Fields(t)
	q := plainQ{why: asked, answer: answerRun, title: title}
	switch r.Class {
	case "tag":
		switch {
		case len(w) == 6 && w[0] == "git" && w[1] == "tag" && w[4] == "on": // the release flow
			q.ask, q.why, q.answer = "Create a tag for "+plain(w[2])+" in "+repoName(w[5])+"?", prepared, "Yes creates the tag. No does nothing."
		case len(w) == 3 && w[0] == "git" && w[1] == "tag":
			q.ask = "Create a tag named " + plain(w[2]) + "?"
		case t == "git push --tags":
			q.ask = "Push a tag for each local tag?"
		case len(w) == 3 && w[0] == "git" && w[1] == "push" && strings.HasPrefix(w[2], "refs/tags/"):
			q.ask = "Push a tag named " + plain(strings.TrimPrefix(w[2], "refs/tags/")) + "?"
		default:
			q.ask = "Allow a tag with " + safeWords(t) + "?"
		}
	case "github_release":
		switch {
		case len(w) == 6 && w[0] == "gh" && w[1] == "release" && w[2] == "create" && w[4] == "on": // the release flow
			q.ask, q.why, q.answer = "Publish a GitHub release for "+plain(w[3])+" in "+repoName(w[5])+"?", prepared, "Yes creates the release. No does nothing."
		case len(w) == 4 && w[0] == "gh" && w[1] == "release":
			switch w[2] {
			case "create":
				q.ask = "Create a GitHub release for " + plain(w[3]) + "?"
			case "upload":
				q.ask = "Upload files to a GitHub release for " + plain(w[3]) + "?"
			case "edit":
				q.ask = "Edit a GitHub release for " + plain(w[3]) + "?"
			case "delete":
				q.ask = "Delete a GitHub release for " + plain(w[3]) + "?"
			}
		}
		if q.ask == "" {
			q.ask = "Allow a GitHub release with " + safeWords(t) + "?"
		}
	case "package_publish":
		switch {
		case len(w) > 0: // the package spec (a scope, a path) stays below the rule
			q.ask = "Publish a package release with " + plain(w[0]) + "?"
		}
	case "external_send":
		// the service in words from the tool's family and the destination as
		// the tool names it; the tool's name stays below the rule
		q.ask = "Send a message outside the squad" + sendWhere(w) + "?"
	case "credential_change":
		// what changes in words; the setting, the path and the command stay
		// below the rule
		q.ask = credentialAsk(t, w)
		q.answer = "Yes makes the credential change. No stops it."
	case "spawn":
		q = composeGrow(t, w, f, q)
	}
	if q.ask == "" {
		q.ask = "Allow " + safeWords(t) + "?"
	}
	return q
}

var mergeTargetRe = regexp.MustCompile(`^grow merge ([^ #]+)#([0-9]+) head=[0-9a-f]+ rules=([a-z0-9_,]*)$`)

// composeGrow is the growth class: a merge outside the grant, a task's
// widen past a grant limit, or the squad past a grant limit.
func composeGrow(t string, w []string, f *Facts, q plainQ) plainQ {
	if m := mergeTargetRe.FindStringSubmatch(t); m != nil {
		where := " in " + repoName(m[1])
		if f.Branch != "" {
			where = " into " + plain(f.Branch)
		}
		q.ask = "Allow a merge of pull request #" + m[2] + where + "?"
		q.why = "The train refused it: " + rulePhrases(strings.Split(m[3], ",")) + "."
		q.answer = "Yes lets the train merge it. No leaves it open."
		return q
	}
	if len(w) == 4 && w[0] == "grow" && strings.HasPrefix(w[2], "task=") {
		id := plain(strings.TrimPrefix(w[2], "task="))
		limit, value, _ := strings.Cut(w[3], "=")
		q.answer = "Yes widens the task. No leaves it as it is."
		switch limit {
		case "protected_paths":
			// the globs stay below the rule (a path is a token)
			q.ask = "Let task " + id + " change protected files?"
			var globs []string
			if json.Unmarshal([]byte(value), &globs) == nil && len(globs) > 0 {
				what := "1 protected path"
				if len(globs) > 1 {
					what = fmt.Sprintf("%d protected paths", len(globs))
				}
				q.why = "The grant refused its widen onto " + what + "."
			}
		case "size_cap":
			q.ask = "Let task " + id + " grow past the size cap?"
			var v struct{ Lines, Files int }
			if json.Unmarshal([]byte(value), &v) == nil {
				q.why = fmt.Sprintf("The grant refused %d lines in %d files.", v.Lines, v.Files)
			}
		default:
			q.ask = "Let task " + id + " grow past its " + plain(strings.ReplaceAll(limit, "_", " ")) + " limit?"
		}
		return q
	}
	if len(w) == 3 && w[0] == "grow" {
		if limit, value, ok := strings.Cut(w[2], "="); ok {
			q.ask = "Allow growth past the grant to " + plain(value) + " " + plain(strings.ReplaceAll(limit, "_", " ")) + "?"
			q.answer = "Yes lets the squad grow. No keeps the grant."
		}
	}
	return q
}

// repoName is a repository's name without its owner (owner/name has a
// path separator, a token above the rule; the full name is in the target).
func repoName(repo string) string {
	_, name, ok := strings.Cut(repo, "/")
	if !ok {
		name = repo
	}
	return plain(name)
}

// ServiceOf is an external send's service in words, from the tool's family:
// Slack for mcp__slack__*, Google Workspace for gws, mail for the mail
// tools, an MCP server's family name otherwise.
func ServiceOf(target string) string {
	w := strings.Fields(target)
	if len(w) == 0 {
		return ""
	}
	switch {
	case w[0] == "gws":
		return "Google Workspace"
	case w[0] == "mail" || w[0] == "mailx" || w[0] == "sendmail" || w[0] == "msmtp":
		return "mail"
	case strings.HasPrefix(w[0], "mcp__"):
		family, _, _ := strings.Cut(strings.TrimPrefix(w[0], "mcp__"), "__")
		if family == "slack" {
			return "Slack"
		}
		family = strings.ReplaceAll(strings.ReplaceAll(family, "_", " "), "-", " ")
		if family != "" {
			return strings.ToUpper(family[:1]) + family[1:]
		}
	}
	return ""
}

// sendWhere is ", on <service>, to <destination>" for an external send: a
// Slack id names a channel (C, G) or a user (U); an email address stands as
// written; gws names its service and verb.
func sendWhere(w []string) string {
	svc := ServiceOf(strings.Join(w, " "))
	out := ""
	switch svc {
	case "":
	case "mail":
		out = ", by mail"
	default:
		out = ", on " + svc
	}
	switch {
	case len(w) == 3 && w[0] == "gws":
		out += ", as a " + plain(w[1]) + " " + plain(w[2])
	case len(w) == 2 && svc == "Slack" && (strings.HasPrefix(w[1], "C") || strings.HasPrefix(w[1], "G")):
		out += ", to channel " + plain(w[1])
	case len(w) == 2 && svc == "Slack" && strings.HasPrefix(w[1], "U"):
		out += ", to user " + plain(w[1])
	case len(w) == 2:
		out += ", to " + plain(w[1])
	}
	return out
}

// CredentialProgram is the program a credential file belongs to, by the
// guard's credential locations (internal/cli guardCredentialPaths), "" when
// none is known.
func CredentialProgram(path string) string {
	for _, c := range []struct{ part, program string }{
		{"/.ssh/", "SSH"}, {"/.aws/", "AWS"}, {"/.git-credentials", "git"}, {"/.config/gh/", "gh"},
		{"/.docker/", "Docker"}, {"/.npmrc", "npm"}, {"/.gnupg/", "GnuPG"}, {"/.kube/", "Kubernetes"},
	} {
		if strings.Contains(path, c.part) {
			return c.program
		}
	}
	return ""
}

// credentialAsk is a credential change's question in words.
func credentialAsk(t string, w []string) string {
	switch {
	case len(w) >= 2 && w[0] == "write":
		if p := CredentialProgram(strings.TrimPrefix(t, "write ")); p != "" {
			return "Write a credentials file for " + p + "?"
		}
		return "Write a credentials file?"
	case len(w) == 3 && w[0] == "git" && w[1] == "config":
		switch {
		case strings.HasPrefix(w[2], "credential.") && strings.HasSuffix(w[2], ".helper") || w[2] == "credential.helper":
			return "Change which program git uses for credentials?"
		case w[2] == "user.signingkey":
			return "Change the key git signs commits with?"
		}
		return "Change how git stores credentials?"
	case len(w) >= 2 && w[len(w)-1] == "login":
		return "Log " + plain(w[0]) + " in?"
	case len(w) >= 2 && w[len(w)-1] == "logout":
		return "Log " + plain(w[0]) + " out?"
	}
	return "Make a credential change with " + safeWords(t) + "?"
}

// safeWords are a target's leading words up to the first token (a word with
// a double underscore, a dot inside or a path separator): what a generic
// question names above the rule; the whole target is below it.
func safeWords(t string) string {
	var out []string
	for _, w := range strings.Fields(t) {
		if IsToken(w) {
			break
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		return "a command"
	}
	return plain(strings.Join(out, " "))
}

var (
	versionRe = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)+([-+][0-9A-Za-z.-]+)?$`)
	emailRe   = regexp.MustCompile(`^[^@\s/]+@[^@\s/]+\.[^@\s/]+$`)
)

// IsToken reports whether a word reads as a code token, not a word: it holds
// a double underscore, a path separator, or a dot inside (other than a
// version such as v3.1.0-rc.1, an email address, or a sentence's end).
func IsToken(w string) bool {
	w = strings.TrimRight(w, ".?!,:;\"")
	switch {
	case strings.Contains(w, "__") || strings.ContainsAny(w, "/\\"):
		return true
	case strings.Contains(w, "."):
		return !versionRe.MatchString(w) && !emailRe.MatchString(w)
	}
	return false
}

// ruleWords are the merge train's refusal rules in words
// (internal/mergetrain/checks.go); an unknown rule shows its code.
var ruleWords = map[string]string{
	"repository":         "the repository is outside the grant",
	"base_branch":        "the base branch is outside the grant",
	"ci_green":           "its checks are not green",
	"size_cap":           "it is over the size cap",
	"force_push":         "its branch was force-pushed",
	"protected_path":     "it touches a protected path",
	"task_scope":         "it changes files outside its task",
	"pr_state":           "it is not open",
	"task_unbound":       "it names no task",
	"pr_head_moved":      "its head moved",
	"history_incomplete": "its history could not be read",
}

// RuleWords is a train rule in words, the code itself when unknown.
func RuleWords(rule string) string {
	if p, ok := ruleWords[rule]; ok {
		return p
	}
	return plain(rule)
}

// rulePhrases names at most two rules in words and counts the rest, so the
// sentence stays under 20 words.
func rulePhrases(rules []string) string {
	var ps []string
	for _, r := range rules {
		if r != "" {
			ps = append(ps, RuleWords(r))
		}
	}
	switch len(ps) {
	case 0:
		return "no rule was named"
	case 1:
		return ps[0]
	case 2:
		return ps[0] + " and " + ps[1]
	}
	return fmt.Sprintf("%s, %s and %d more", ps[0], ps[1], len(ps)-2)
}

var hexRunRe = regexp.MustCompile(`(?i)[0-9a-f]{8,}`)

// plain is a field value as the plain lines show it: a hex run of 8 or more,
// in either case, is cut to its first 7 and an ellipsis (no hash above the
// rule).
func plain(s string) string {
	return hexRunRe.ReplaceAllStringFunc(s, func(h string) string { return h[:7] + "…" })
}

// quote is a title as its own line: plain, each code token (IsToken: a
// double underscore, a path separator, a dot inside) replaced by an
// ellipsis, cut at TitleMax characters with an ellipsis, in double quotes.
func quote(s string) string {
	words := strings.Fields(plain(redact.Block(s)))
	for i, w := range words {
		if IsToken(w) {
			words[i] = "…"
		}
	}
	s = strings.Join(words, " ")
	if utf8.RuneCountInString(s) > TitleMax {
		s = string([]rune(s)[:TitleMax]) + "…"
	}
	return `"` + s + `"`
}

// ParseBare reads a bare answer (brief 3.1.1-1): yes, no, approve or deny
// alone on its line, case and trailing punctuation ignored. It returns the
// verb (approve or deny). A bare answer names no gate: the thread it replies
// in binds it, and the signature under the pinned owner key proves its
// sender.
func ParseBare(content string) (string, bool) {
	s := strings.TrimSpace(content)
	if strings.ContainsAny(s, "\r\n") {
		return "", false
	}
	s = strings.ToLower(strings.TrimSpace(strings.TrimRightFunc(s, unicode.IsPunct)))
	switch s {
	case "yes", "approve":
		return "approve", true
	case "no", "deny":
		return "deny", true
	}
	return "", false
}

// BareAnswer is the answer a bare verb gives this record: its own gate id,
// class and digest prefix (the thread is what bound it to the record).
func (r Record) BareAnswer(verb string) Answer {
	return Answer{Verb: verb, GateID: r.GateID, Class: r.TypedClass, DigestPrefix: strings.TrimPrefix(r.Digest, "sha256:")[:PrefixLen]}
}
