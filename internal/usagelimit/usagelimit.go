// Package usagelimit is the usage-limit detector of brief 2b-3: it reads the
// provider session files and reports usage-limit events, which the
// scheduler (2b-1, internal/scheduler/usage_limit.go) polls once per tick
// and answers by starting no new seat (plan 5, R16, amendment 22).
//
// Sources, by provider:
//
//	claude  ~/.claude/projects/<encoded cwd>/**/*.jsonl, the Claude Code
//	        session transcripts (the S6 scanner's source). An assistant line
//	        with isApiErrorMessage whose text says a session, weekly, spend or
//	        usage limit was hit is a usage_limit; other 429s are http_429; a
//	        system api_error line that mentions 429 or rate_limit is retry_429.
//	codex   $CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl (the name AMQ
//	        v0.107.0's capture uses). An event_msg error whose message says the
//	        usage limit was hit, or a token_count whose rate_limits show a
//	        window at 100 percent, is a usage_limit.
//
// Which of these files carry the signal on the owner's host is measured by
// the owner with `amq-squad doctor --usage-scan <duration>` (lead ruling 1 of
// brief 2b-3); the TUI banner "You have N usage limit resets available" is
// not a limit and never counts.
package usagelimit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kinds. Only KindUsageLimit stops the scheduler; the others are reported
// by the owner scan.
const (
	KindUsageLimit  = "usage_limit"
	KindHTTP429     = "http_429"
	KindRetry429    = "retry_429"
	KindServerError = "server_error"
	KindOtherError  = "other_error"
)

// Event has the shape of 2b-1's scheduler.UsageLimitEvent plus the kind.
type Event struct {
	At       time.Time `json:"at"`
	Provider string    `json:"provider"`
	Seat     string    `json:"seat,omitempty"`
	ResetAt  time.Time `json:"reset_at,omitempty"`
	Source   string    `json:"source"` // file:line
	Kind     string    `json:"kind"`
	// SessionID is the provider's session id (Claude sessionId, Codex
	// session_meta id); with the event identity it deduplicates copies.
	SessionID string `json:"session_id,omitempty"`
	// identity is the provider's event id (Claude's line uuid) when present,
	// else the SHA-256 of the whole line.
	identity string
	Text     string `json:"text,omitempty"` // the first 160 characters of the provider's message
}

// Detector reads the session files under one home.
type Detector struct {
	ClaudeProjects string // ~/.claude/projects, or $CLAUDE_CONFIG_DIR/projects
	CodexSessions  string // ~/.codex/sessions, or $CODEX_HOME/sessions
	// Seats maps a seat's working directory to its handle. When set, only
	// files of those directories are read and each event names its seat.
	Seats map[string]string
}

// ForHome is the detector for the default provider locations under home.
func ForHome(home, claudeConfigDir, codexHome string) Detector {
	d := Detector{
		ClaudeProjects: filepath.Join(home, ".claude", "projects"),
		CodexSessions:  filepath.Join(home, ".codex", "sessions"),
	}
	if strings.TrimSpace(claudeConfigDir) != "" {
		d.ClaudeProjects = filepath.Join(claudeConfigDir, "projects")
	}
	if strings.TrimSpace(codexHome) != "" {
		d.CodexSessions = filepath.Join(codexHome, "sessions")
	}
	return d
}

// UsageLimitEvents returns the usage_limit events at or after since, oldest
// first: the scheduler's UsageLimitSource. After 2b-1 merges it returns
// []scheduler.UsageLimitEvent (same fields).
func (d Detector) UsageLimitEvents(since time.Time) ([]Event, error) {
	all, err := d.Scan(since)
	var out []Event
	for _, e := range all {
		if e.Kind == KindUsageLimit {
			out = append(out, e)
		}
	}
	return out, err
}

// Scan returns every limit, 429 and API error event at or after since,
// oldest first. Missing provider directories are not an error.
func (d Detector) Scan(since time.Time) ([]Event, error) {
	var out []Event
	for _, f := range d.files(d.ClaudeProjects, "*.jsonl", since, d.claudeDirMatch) {
		out = append(out, scanFile(f, since, func(line []byte) *Event { return classifyClaude(line) }, d.Seats)...)
	}
	for _, f := range d.files(d.CodexSessions, "rollout-*.jsonl", since, nil) {
		out = append(out, scanCodex(f, since, d.Seats)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return dedupe(out), nil
}

// dedupe drops copies of one provider event across files (a rotated or
// copied rollout or transcript carries the same lines): the key is the
// provider, the event identity (the provider's event id, else the full
// line's SHA-256) and the session id; an event without a session id is
// never merged with another. The first source wins.
func dedupe(events []Event) []Event {
	seen := map[string]bool{}
	out := events[:0]
	for _, e := range events {
		key := e.Provider + "\x00" + e.identity + "\x00" + e.SessionID
		if e.SessionID == "" {
			key += "\x00" + e.Source // a missing session id never merges
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

// files lists the matching files modified at or after since.
func (d Detector) files(root, pattern string, since time.Time, dirOK func(string) bool) []string {
	var out []string
	if root == "" {
		return nil
	}
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if p != root && dirOK != nil && filepath.Dir(p) == root && !dirOK(e.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if ok, _ := filepath.Match(pattern, e.Name()); !ok {
			return nil
		}
		if info, err := e.Info(); err == nil && !info.ModTime().Before(since) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeDirMatch keeps the project directories of the seat working
// directories (Claude Code encodes a cwd by replacing every non-alphanumeric
// character with '-'); with no seats every directory is read.
func (d Detector) claudeDirMatch(name string) bool {
	if len(d.Seats) == 0 {
		return true
	}
	for cwd := range d.Seats {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(nonAlnum.ReplaceAllString(cwd, "-"))) {
			return true
		}
	}
	return false
}

func seatFor(seats map[string]string, cwd string) string {
	if cwd == "" {
		return ""
	}
	best, handle := "", ""
	for dir, h := range seats {
		if (cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))) && len(dir) > len(best) {
			best, handle = dir, h
		}
	}
	return handle
}

func scanFile(path string, since time.Time, classify func([]byte) *Event, seats map[string]string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Bytes()
		if !mayCarry(line) {
			continue
		}
		e := classify(line)
		if e == nil || e.At.Before(since) {
			continue
		}
		e.Source = path + ":" + strconv.Itoa(n)
		if e.identity == "" {
			e.identity = lineIdentity(line)
		}
		e.Seat = seatFor(seats, e.Seat)
		out = append(out, *e)
	}
	return out
}

func mayCarry(line []byte) bool {
	s := string(line)
	return strings.Contains(s, "429") || strings.Contains(s, "rate_limit") || strings.Contains(s, "isApiErrorMessage") ||
		strings.Contains(s, "api_error") || strings.Contains(s, "limit") || strings.Contains(s, `"error"`)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// The S6 scanner's limit phrase, minus the reset banner.
var (
	usageRE  = regexp.MustCompile(`(?i)hit your .{0,40}limit|spend limit|session limit|usage limit|weekly limit|limit reached|usage_limit`)
	retryRE  = regexp.MustCompile(`(?i)429|rate_limit|rate limit`)
	bannerRE = regexp.MustCompile(`(?i)usage limit resets? available`)
)

func limitText(s string) bool { return usageRE.MatchString(s) && !bannerRE.MatchString(s) }

type claudeLine struct {
	Type              string          `json:"type"`
	Subtype           string          `json:"subtype"`
	Timestamp         string          `json:"timestamp"`
	SessionID         string          `json:"sessionId"`
	UUID              string          `json:"uuid"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	APIErrorStatus    int             `json:"apiErrorStatus"`
	Error             json.RawMessage `json:"error"`
	Cwd               string          `json:"cwd"`
	Message           struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func classifyClaude(line []byte) *Event {
	var l claudeLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	e := &Event{At: parseTime(l.Timestamp), Provider: "claude", Seat: l.Cwd, SessionID: l.SessionID}
	if l.UUID != "" {
		e.identity = "uuid:" + l.UUID
	}
	if e.At.IsZero() {
		return nil
	}
	switch {
	case l.Type == "assistant" && l.IsAPIErrorMessage:
		text := contentText(l.Message.Content)
		errName := strings.Trim(string(l.Error), `"`)
		switch {
		case limitText(text):
			e.Kind = KindUsageLimit
			e.ResetAt = claudeReset(text, e.At)
		case l.APIErrorStatus == 429 || errName == "rate_limit":
			e.Kind = KindHTTP429
		case l.APIErrorStatus >= 500 || errName == "server_error" || strings.Contains(strings.ToLower(text), "overloaded"):
			e.Kind = KindServerError
		default:
			e.Kind = KindOtherError
		}
		e.Text = clip(text)
	case l.Type == "system" && l.Subtype == "api_error":
		blob := string(l.Error)
		if !retryRE.MatchString(blob) {
			return nil
		}
		e.Kind, e.Text = KindRetry429, clip(blob)
	default:
		return nil
	}
	return e
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text + " ")
	}
	return b.String()
}

var claudeResetRE = regexp.MustCompile(`(?i)resets\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*(?:\(([^)]+)\))?`)

// claudeReset reads "resets 5:00am (Asia/Jerusalem)" as the next such time
// after the event; zero when the text names no time.
func claudeReset(text string, at time.Time) time.Time {
	m := claudeResetRE.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}
	}
	hour, minute := atoiOr(m[1]), atoiOr(m[2])
	if strings.EqualFold(m[3], "pm") && hour < 12 {
		hour += 12
	}
	if strings.EqualFold(m[3], "am") && hour == 12 {
		hour = 0
	}
	loc := time.Local
	if m[4] != "" {
		if l, err := time.LoadLocation(strings.TrimSpace(m[4])); err == nil {
			loc = l
		}
	}
	local := at.In(loc)
	reset := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !reset.After(at) {
		reset = reset.Add(24 * time.Hour)
	}
	return reset.UTC()
}

func atoiOr(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// lineIdentity is the SHA-256 of one session-file line.
func lineIdentity(line []byte) string {
	sum := sha256.Sum256(line)
	return "line:" + hex.EncodeToString(sum[:])
}
