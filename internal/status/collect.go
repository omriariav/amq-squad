package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/activity"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Collect reads one squad session root into a Build input: the task store
// (lock-free), the seat mailboxes and launch records, the evidence ledger
// (last activity, prompt state, token counts), the AMQ messages (last
// message per seat, threads that wait on the owner) and the scheduler state
// file. Gates come from the caller, which verifies their answers. It never
// writes.

// CollectOptions configures Collect.
type CollectOptions struct {
	Root, Session, ProjectPath, ProducerVersion string
	OperatorHandle                              string // the owner's mailbox; not a seat (default "user")
	Now                                         time.Time
	Gates                                       []GateInput
	PIDAlive                                    func(int) bool // default procinfo.Alive
}

// Freshness windows for the seat liveness inputs.
const (
	presenceFresh = state.PresenceFreshness // 90 s, as AMQ presence
	evidenceFresh = 5 * time.Minute         // a seat in a long tool call writes no presence
	ledgerTailMax = 1 << 20                 // the ledger tail read for activity and tokens
)

// Collect gathers the inputs. A read problem becomes a warning, never an
// error: a partial status is still a valid document.
func Collect(o CollectOptions) Input {
	if o.OperatorHandle == "" {
		o.OperatorHandle = state.DefaultOperatorHandle
	}
	if o.PIDAlive == nil {
		o.PIDAlive = procinfo.Alive
	}
	in := Input{Root: o.Root, Session: o.Session, ProjectPath: o.ProjectPath, ProducerVersion: o.ProducerVersion, Now: o.Now, Gates: o.Gates}
	warn := func(format string, args ...any) { in.Warnings = append(in.Warnings, fmt.Sprintf(format, args...)) }
	var err error
	if in.Tasks, err = task.Snapshot(task.DirFor(o.Root)); err != nil {
		warn("task store: %v", err)
	}
	if in.Scheduler, err = scheduler.ReadState(scheduler.StatePathFor(o.Root)); err != nil {
		warn("scheduler state: %v", err)
	}
	if in.LaunchAlarms, err = launch.ReadAlarms(o.Root); err != nil {
		warn("launch alarms: %v", err)
	}
	msgs, scanWarnings := state.ScanSessionMessages(o.Root, func() time.Time { return o.Now })
	if len(scanWarnings) > 0 {
		warn("mailbox scan: %d unreadable messages skipped", len(scanWarnings))
	}
	lastFrom := map[string]state.Message{}
	byID := map[string]state.Message{}
	for _, m := range msgs { // sorted oldest first
		lastFrom[m.From] = m
		byID[m.ID] = m
	}
	coord := state.ProjectCoordination(msgs, nil, nil, o.Now, state.Thresholds{OperatorHandle: o.OperatorHandle})
	for _, th := range coord.NeedsYouThreads() {
		if th.Historical {
			continue
		}
		seat := byID[th.LatestID].From
		if seat == "" || seat == o.OperatorHandle {
			for _, p := range th.Participants {
				if p != o.OperatorHandle {
					seat = p
					break
				}
			}
		}
		in.NeedsYou = append(in.NeedsYou, ThreadInput{ID: th.ID, Seat: seat, Subject: th.Subject, Since: th.LastEventAt})
	}
	in.Alerts = collectAlerts(o.Root, warn)
	entries, err := os.ReadDir(filepath.Join(o.Root, "agents"))
	if err != nil && !os.IsNotExist(err) {
		warn("agents: %v", err)
	}
	tokenSource := false
	for _, e := range entries {
		if !e.IsDir() || e.Name() == o.OperatorHandle || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		seat, tokens := collectSeat(o, e.Name(), lastFrom[e.Name()], warn)
		tokenSource = tokenSource || tokens
		in.Seats = append(in.Seats, seat)
	}
	in.Capabilities = []string{"seat.last_message:amq_mailbox", "seat.prompt_state:evidence_ledger"}
	if tokenSource {
		in.Capabilities = append(in.Capabilities, "seat.tokens:evidence_ledger")
	}
	return in
}

func collectSeat(o CollectOptions, handle string, last state.Message, warn func(string, ...any)) (SeatInput, bool) {
	dir := filepath.Join(o.Root, "agents", handle)
	seat := SeatInput{Handle: handle, PromptState: "unknown"}
	rec, err := launch.Read(dir)
	switch {
	case err == nil:
		seat.HasSignal = true
		seat.Role, seat.Runtime, seat.Model, seat.ProjectPath = rec.Role, filepath.Base(rec.Binary), rec.Model, rec.CWD
		if rec.StoppedAt == nil && rec.AgentPID > 0 && o.PIDAlive(rec.AgentPID) {
			seat.Live = true
		}
	case !os.IsNotExist(err):
		warn("seat %s launch record: %v", handle, err)
	}
	if pres, err := readPresence(dir); err == nil {
		seat.HasSignal = true
		if pres.Status == "active" && o.Now.Sub(pres.LastSeen) <= presenceFresh {
			seat.Live = true
		}
		seat.LastActivity = later(seat.LastActivity, pres.LastSeen)
	}
	if act, ok, err := activity.Read(dir, o.Now, activity.DefaultStaleAfter); err == nil && ok {
		seat.HasSignal = true
		seat.Live = seat.Live || !act.Stale
		seat.LastActivity = later(seat.LastActivity, act.WrittenAt)
	}
	led, err := readLedgerTail(filepath.Join(o.Root, "extensions", "amq-squad", "ledger", handle+".jsonl"))
	if err != nil && !os.IsNotExist(err) {
		warn("seat %s ledger: %v", handle, err)
	}
	if !led.last.IsZero() {
		seat.HasSignal = true
		seat.LastActivity = later(seat.LastActivity, led.last)
		if o.Now.Sub(led.last) <= evidenceFresh {
			seat.Live = true
		}
		seat.PromptState = led.prompt
	}
	if led.tokens {
		in, out := led.in, led.out
		seat.TokensIn, seat.TokensOut = &in, &out
	}
	if last.ID != "" {
		at := last.Created
		seat.LastMessageAt = &at
		seat.LastMessage = strings.TrimSpace(last.Subject)
		if body := strings.TrimSpace(last.Body); body != "" {
			if seat.LastMessage != "" {
				seat.LastMessage += ": "
			}
			seat.LastMessage += body
		}
	}
	return seat, led.tokens
}

type presence struct {
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

func readPresence(dir string) (presence, error) {
	var p presence
	raw, err := os.ReadFile(filepath.Join(dir, "presence.json"))
	if err == nil {
		err = json.Unmarshal(raw, &p)
	}
	return p, err
}

type ledgerTail struct {
	last    time.Time
	prompt  string
	tokens  bool
	in, out int64
}

// readLedgerTail reads the last MiB of a seat's evidence ledger (schema 1,
// docs/v3-spike/2a3-notes.md): the last line's time and event give the last
// activity and the prompt state; token counts are summed where a payload
// carries them (pi usage; Claude Code hook payloads carry none).
func readLedgerTail(path string) (ledgerTail, error) {
	var t ledgerTail
	f, err := os.Open(path)
	if err != nil {
		return t, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return t, err
	}
	off := st.Size() - ledgerTailMax
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return t, err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return t, err
	}
	lines := bytes.Split(raw, []byte("\n"))
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // a partial first line
	}
	for _, ln := range lines {
		var l struct {
			At      string          `json:"at"`
			Event   string          `json:"event"`
			Tool    string          `json:"tool"`
			Payload json.RawMessage `json:"payload"`
		}
		if len(bytes.TrimSpace(ln)) == 0 || json.Unmarshal(ln, &l) != nil {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, l.At); err == nil && !at.Before(t.last) {
			t.last, t.prompt = at, promptState(l.Event, l.Tool)
		}
		if in, out, ok := payloadTokens(l.Payload); ok {
			t.tokens, t.in, t.out = true, t.in+in, t.out+out
		}
	}
	return t, nil
}

// promptState maps the last hook event to running, idle or unknown.
func promptState(event, tool string) string {
	switch strings.ToLower(event) {
	case "stop", "subagentstop", "agent_end", "turn_end", "idle":
		return "idle"
	case "userpromptsubmit", "pretooluse", "posttooluse", "tool_result", "tool_call", "turn_start", "agent_start":
		return "running"
	}
	if tool != "" {
		return "running"
	}
	return "unknown"
}

// PayloadTokenCounts reads the input and output token counts of one hook
// payload (its "usage", or "message.usage") and says which of the two it
// names. The routing outcome log (2b-4) needs both: a payload with one
// counter is unknown usage there, never a zero for the other (Codex pass 1,
// P2-5). The status seat row keeps its own rule (either counter).
func PayloadTokenCounts(raw json.RawMessage) (in, out int64, inOK, outOK bool) {
	return payloadTokenCounts(raw)
}

// payloadTokens finds a usage object (usage, message.usage) with input and
// output token counts in a hook payload; ok when it names either.
func payloadTokens(raw json.RawMessage) (int64, int64, bool) {
	in, out, inOK, outOK := payloadTokenCounts(raw)
	return in, out, inOK || outOK
}

func payloadTokenCounts(raw json.RawMessage) (int64, int64, bool, bool) {
	if len(raw) == 0 {
		return 0, 0, false, false
	}
	var p map[string]json.RawMessage
	if json.Unmarshal(raw, &p) != nil {
		return 0, 0, false, false
	}
	usage := p["usage"]
	if len(usage) == 0 {
		var msg map[string]json.RawMessage
		if json.Unmarshal(p["message"], &msg) == nil {
			usage = msg["usage"]
		}
	}
	var u map[string]float64
	if len(usage) == 0 || json.Unmarshal(usage, &u) != nil {
		return 0, 0, false, false
	}
	in, inOK := firstKey(u, "input_tokens", "inputTokens", "input")
	out, outOK := firstKey(u, "output_tokens", "outputTokens", "output")
	return int64(in), int64(out), inOK, outOK
}

func firstKey(m map[string]float64, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v, true
		}
	}
	return 0, false
}

func later(cur *time.Time, t time.Time) *time.Time {
	if t.IsZero() || (cur != nil && !t.After(*cur)) {
		return cur
	}
	return &t
}

// collectAlerts reads the owner alerts the merge train and doctor write
// (mergetrain.Alert: id, kind tag|release, detail, at).
func collectAlerts(root string, warn func(string, ...any)) []AlertInput {
	paths, _ := filepath.Glob(filepath.Join(root, "extensions", "amq-squad", "alerts", "ungated-*.json"))
	var out []AlertInput
	for _, p := range paths {
		var a struct {
			ID, Kind, Detail, At string
		}
		raw, err := os.ReadFile(p)
		if err == nil {
			err = json.Unmarshal(raw, &a)
		}
		at, terr := time.Parse(time.RFC3339, a.At)
		if err != nil || terr != nil || a.ID == "" {
			warn("alert %s unreadable", filepath.Base(p))
			continue
		}
		out = append(out, AlertInput{ID: a.ID, Kind: "ungated_" + a.Kind, Subject: a.Detail, At: at})
	}
	return out
}
