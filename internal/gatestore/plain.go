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
func (f *Facts) normalize() *Facts {
	if f == nil || (f.Title == "" && f.Branch == "") {
		return nil
	}
	c := *f
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
	f := Facts{Title: strings.Join(strings.Fields(title), " "), Branch: branch}
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
var ClassWords = []string{"a tag", "a GitHub release", "a package release", "an outside message", "a credential change", "growth past the grant", "a task widen", "a merge"}

// TextOf is the only renderer of the owner-facing gate text: the plain lines,
// a blank line, the rule, and the identifiers.
func TextOf(r Record) string {
	return strings.Join(append(PlainLines(r), "", Rule, identifiers(r.Squad, r.GateID, r.TypedClass, r.Class, r.Target, r.Digest)), "\n")
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
	return append(lines, q.why, q.answer)
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
			q.ask, q.why, q.answer = "Create a tag for "+plain(w[2])+" in "+plain(w[5])+"?", prepared, "Yes creates the tag. No does nothing."
		case len(w) == 3 && w[0] == "git" && w[1] == "tag":
			q.ask = "Create a tag named " + plain(w[2]) + "?"
		case t == "git push --tags":
			q.ask = "Push a tag for each local tag?"
		case len(w) == 3 && w[0] == "git" && w[1] == "push" && strings.HasPrefix(w[2], "refs/tags/"):
			q.ask = "Push a tag named " + plain(strings.TrimPrefix(w[2], "refs/tags/")) + "?"
		default:
			q.ask = "Allow a tag with " + plain(t) + "?"
		}
	case "github_release":
		switch {
		case len(w) == 6 && w[0] == "gh" && w[1] == "release" && w[2] == "create" && w[4] == "on": // the release flow
			q.ask, q.why, q.answer = "Publish a GitHub release for "+plain(w[3])+" in "+plain(w[5])+"?", prepared, "Yes creates the release. No does nothing."
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
			q.ask = "Allow a GitHub release with " + plain(t) + "?"
		}
	case "package_publish":
		switch {
		case len(w) > 2:
			q.ask = "Publish a package release of " + plain(strings.Join(w[2:], " ")) + " with " + plain(w[0]) + "?"
		case len(w) > 0:
			q.ask = "Publish a package release with " + plain(w[0]) + "?"
		}
	case "external_send":
		switch {
		case len(w) == 3 && w[0] == "gws":
			q.ask = "Send an outside message with gws " + plain(w[1]) + " " + plain(w[2]) + "?"
		case len(w) == 2:
			q.ask = "Send an outside message with " + plain(w[0]) + " to " + plain(w[1]) + "?"
		case len(w) == 1:
			q.ask = "Send an outside message with " + plain(w[0]) + "?"
		default:
			q.ask = "Send an outside message with " + plain(t) + "?"
		}
	case "credential_change":
		switch {
		case len(w) >= 2 && w[0] == "write":
			q.ask = "Make a credential change by writing " + plain(strings.TrimPrefix(t, "write ")) + "?"
		case len(w) == 3 && w[0] == "git" && w[1] == "config":
			q.ask = "Make a credential change to git setting " + plain(w[2]) + "?"
		default:
			q.ask = "Make a credential change with " + plain(t) + "?"
		}
	case "spawn":
		q = composeGrow(t, w, f, q)
	}
	if q.ask == "" {
		q.ask = "Allow " + plain(t) + "?"
	}
	return q
}

var mergeTargetRe = regexp.MustCompile(`^grow merge ([^ #]+)#([0-9]+) head=[0-9a-f]+ rules=([a-z0-9_,]*)$`)

// composeGrow is the growth class: a merge outside the grant, a task's
// widen past a grant limit, or the squad past a grant limit.
func composeGrow(t string, w []string, f *Facts, q plainQ) plainQ {
	if m := mergeTargetRe.FindStringSubmatch(t); m != nil {
		where := " in " + plain(m[1])
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
			q.ask = "Allow a task widen of " + id + " onto protected paths?"
			var globs []string
			if json.Unmarshal([]byte(value), &globs) == nil && len(globs) > 0 {
				more := ""
				if len(globs) > 1 {
					more = fmt.Sprintf(" and %d more", len(globs)-1)
				}
				q.why = "The grant refused the widen at " + plain(globs[0]) + more + "."
			}
		case "size_cap":
			q.ask = "Allow a task widen of " + id + " past the size cap?"
			var v struct{ Lines, Files int }
			if json.Unmarshal([]byte(value), &v) == nil {
				q.why = fmt.Sprintf("The grant refused %d lines in %d files.", v.Lines, v.Files)
			}
		default:
			q.ask = "Allow a task widen of " + id + " past its " + plain(strings.ReplaceAll(limit, "_", " ")) + " limit?"
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

var hexRunRe = regexp.MustCompile(`[0-9a-f]{8,}`)

// plain is a field value as the plain lines show it: a hex run of 8 or more
// is cut to its first 7 and an ellipsis (no hash above the rule).
func plain(s string) string {
	return hexRunRe.ReplaceAllStringFunc(s, func(h string) string { return h[:7] + "…" })
}

// quote is a title as its own line: plain, cut at TitleMax characters with
// an ellipsis, in double quotes.
func quote(s string) string {
	s = plain(s)
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
