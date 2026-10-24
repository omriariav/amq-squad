package usagelimit

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// codexLine is one line of a codex-cli rollout file.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type       string          `json:"type"`
		ID         string          `json:"id"`
		Cwd        string          `json:"cwd"`
		Message    string          `json:"message"`
		RateLimits *codexRateLimit `json:"rate_limits"`
		Info       json.RawMessage `json:"info"`
	} `json:"payload"`
}

type codexWindow struct {
	UsedPercent     float64 `json:"used_percent"`
	WindowMinutes   int     `json:"window_minutes"`
	ResetsInSeconds int64   `json:"resets_in_seconds"`
	ResetsAt        int64   `json:"resets_at"`
}

type codexRateLimit struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
}

var (
	codexHitRE   = regexp.MustCompile(`(?i)hit your usage limit|usage limit (reached|exceeded)|usage_limit_(reached|exceeded)|quota exceeded`)
	codex429RE   = regexp.MustCompile(`(?i)\b429\b|rate.?limit|too many requests`)
	tryAgainInRE = regexp.MustCompile(`(?i)try again in\s+(?:(\d+)\s*(?:days?|d)\s*)?(?:(\d+)\s*(?:hours?|h)\s*)?(?:(\d+)\s*(?:minutes?|min|m)\b)?`)
)

// scanCodex reads one rollout file. The first line (session_meta) names the
// cwd, which picks the seat; with seats set, a file of another cwd is skipped.
func scanCodex(path string, since time.Time, seats map[string]string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var out []Event
	seat, session := "", ""
	for n := 1; sc.Scan(); n++ {
		var l codexLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Type == "session_meta" && session == "" {
			session = l.Payload.ID
		}
		if n == 1 && l.Payload.Cwd != "" {
			seat = seatFor(seats, l.Payload.Cwd)
			if len(seats) > 0 && seat == "" {
				return nil
			}
		}
		e := classifyCodex(l)
		if e == nil || e.At.Before(since) {
			continue
		}
		e.Seat, e.Source, e.SessionID = seat, path+":"+strconv.Itoa(n), session
		out = append(out, *e)
	}
	return out
}

func classifyCodex(l codexLine) *Event {
	at := parseTime(l.Timestamp)
	if at.IsZero() || l.Type != "event_msg" {
		return nil
	}
	e := &Event{At: at, Provider: "codex"}
	switch l.Payload.Type {
	case "error", "stream_error":
		msg := l.Payload.Message
		switch {
		case limitText(msg) || codexHitRE.MatchString(msg):
			if bannerRE.MatchString(msg) {
				return nil
			}
			e.Kind, e.ResetAt = KindUsageLimit, codexReset(msg, at)
		case codex429RE.MatchString(msg):
			e.Kind = KindHTTP429
		default:
			e.Kind = KindOtherError
		}
		e.Text = clip(msg)
		return e
	case "token_count":
		rl := l.Payload.RateLimits
		if rl == nil {
			return nil
		}
		for _, w := range []*codexWindow{rl.Primary, rl.Secondary} {
			if w != nil && w.UsedPercent >= 100 {
				e.Kind = KindUsageLimit
				e.Text = "rate_limits window at " + strconv.FormatFloat(w.UsedPercent, 'f', -1, 64) + " percent of " + strconv.Itoa(w.WindowMinutes) + " min"
				switch {
				case w.ResetsAt > 0:
					e.ResetAt = time.Unix(w.ResetsAt, 0).UTC()
				case w.ResetsInSeconds > 0:
					e.ResetAt = at.Add(time.Duration(w.ResetsInSeconds) * time.Second).UTC()
				}
				return e
			}
		}
	}
	return nil
}

// codexReset reads "try again in 2 hours 5 minutes".
func codexReset(msg string, at time.Time) time.Time {
	m := tryAgainInRE.FindStringSubmatch(msg)
	if m == nil || m[1]+m[2]+m[3] == "" {
		return time.Time{}
	}
	d := time.Duration(atoiOr(m[1]))*24*time.Hour + time.Duration(atoiOr(m[2]))*time.Hour + time.Duration(atoiOr(m[3]))*time.Minute
	return at.Add(d).UTC()
}

// FileSummary is one file that carried a signal, for the owner scan.
type FileSummary struct {
	Provider string
	File     string
	Counts   map[string]int
}

// Summarize groups events by file, in file order.
func Summarize(events []Event) []FileSummary {
	var out []FileSummary
	index := map[string]int{}
	for _, e := range events {
		file := e.Source
		if i := strings.LastIndex(file, ":"); i > 0 {
			file = file[:i]
		}
		i, ok := index[file]
		if !ok {
			i = len(out)
			index[file] = i
			out = append(out, FileSummary{Provider: e.Provider, File: file, Counts: map[string]int{}})
		}
		out[i].Counts[e.Kind]++
	}
	return out
}
