package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/activity"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
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
	in.PushURLMissing = pushURLMissing(o.Root)
	in.VerifierWaiting = seatcommit.VerifierWaiting(o.Root)
	in.SeatMarkers = task.SeatMarkerProblems(task.DirFor(o.Root))
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
		if !e.IsDir() || e.Name() == o.OperatorHandle || e.Name() == scheduler.SenderHandle || strings.HasPrefix(e.Name(), ".") {
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
	// The seat's role, runtime, model and effort join from its launch record
	// (2b-2): the published one, else the core's staged one (before seat-exec
	// publishes). Only the published record is a liveness signal.
	if raw, err := os.ReadFile(filepath.Join(launch.StagedDir(o.Root, handle), launch.FileName)); err == nil {
		if staged, err := launch.Decode(raw); err == nil {
			joinLaunchRecord(&seat, staged)
		}
	}
	rec, err := launch.Read(dir)
	switch {
	case err == nil:
		seat.HasSignal = true
		joinLaunchRecord(&seat, rec)
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

// joinLaunchRecord fills the seat row's identity fields from a launch record;
// an empty field keeps what an earlier record gave.
func joinLaunchRecord(seat *SeatInput, rec launch.Record) {
	runtime := rec.Runtime
	if runtime == "" && rec.Binary != "" {
		runtime = filepath.Base(rec.Binary)
	}
	for _, kv := range []struct {
		dst *string
		v   string
	}{{&seat.Role, rec.Role}, {&seat.Runtime, runtime}, {&seat.Model, rec.Model}, {&seat.Effort, rec.Effort}, {&seat.ProjectPath, rec.CWD}} {
		if kv.v != "" {
			*kv.dst = kv.v
		}
	}
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
		if len(bytes.TrimSpace(ln)) == 0 || json.Unmarshal(ln, &l) != nil || l.Event == seatcommit.EventVerifier {
			continue // the harness's verifier line is not the seat's activity
		}
		if at, err := time.Parse(time.RFC3339Nano, l.At); err == nil && !at.Before(t.last) {
			t.last, t.prompt = at, promptState(l.Event, l.Tool)
		}
		if in, out, ok := payloadTokens(l.Payload); ok { // an invalid counter is no count (phase 5 re-pass, P2-4)
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

// MaxTokenCount bounds one payload's token counter: a larger value is out of
// range (no provider reports it for one turn), and a window's sum stays far
// from overflow.
const MaxTokenCount = 1 << 40

// TokenCounts is what one hook payload says about token usage: each counter
// and whether the payload names it, or, when a named counter is not a
// count (negative, fractional, out of range, not a number), why the
// payload's usage is unknown.
type TokenCounts struct {
	In, Out     int64
	InOK, OutOK bool
	Invalid     string
}

// The counters by name (phase 5 re-pass, P2-3): the Anthropic and pi names,
// their camel-case forms and the OpenAI-style aliases. A bare "input" or
// "output" is read only inside a usage object, never from a flat payload,
// where it names the tool's input or output.
var (
	inputCounters  = []string{"input_tokens", "inputTokens", "prompt_tokens", "promptTokens"}
	outputCounters = []string{"output_tokens", "outputTokens", "completion_tokens", "completionTokens"}
)

// PayloadTokenCounts reads the input and output token counts of one hook
// payload: its "usage" object, else "message.usage", else counters flat on
// the payload. Only the known counters are decoded, so a usage object that
// also carries nested objects (input_tokens_details) is read; the aliases
// prompt_tokens and completion_tokens are the input and output counters
// (phase 5 re-pass, P2-3). A named counter that is negative, fractional or
// out of range makes the payload's usage unknown, with the reason (P2-4).
// The routing outcome log (2b-4) needs both counters: a payload with one is
// unknown usage there, never a zero for the other (Codex pass 1, P2-5). The
// status seat row keeps its own rule (either counter).
func PayloadTokenCounts(raw json.RawMessage) TokenCounts {
	var c TokenCounts
	p := jsonObject(raw)
	if p == nil {
		return c
	}
	in, out := inputCounters, outputCounters
	usage := jsonObject(p["usage"])
	if usage == nil {
		if msg := jsonObject(p["message"]); msg != nil {
			usage = jsonObject(msg["usage"])
		}
	}
	if usage != nil {
		in, out = append(slices.Clone(in), "input"), append(slices.Clone(out), "output")
	} else {
		usage = p
	}
	var bad string
	if c.In, c.InOK, bad = counter(usage, in); bad != "" {
		return TokenCounts{Invalid: bad}
	}
	if c.Out, c.OutOK, bad = counter(usage, out); bad != "" {
		return TokenCounts{Invalid: bad}
	}
	return c
}

// payloadTokens finds a usage object (usage, message.usage) with input and
// output token counts in a hook payload; ok when it names either.
func payloadTokens(raw json.RawMessage) (int64, int64, bool) {
	c := PayloadTokenCounts(raw)
	return c.In, c.Out, c.Invalid == "" && (c.InOK || c.OutOK)
}

func jsonObject(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// counter is the first of names the object carries, as a count; a null is
// absent. The reason is set when the value is not a count.
func counter(m map[string]json.RawMessage, names []string) (int64, bool, string) {
	for _, k := range names {
		v, ok := m[k]
		if !ok || string(bytes.TrimSpace(v)) == "null" {
			continue
		}
		var f float64
		if err := json.Unmarshal(v, &f); err != nil {
			return 0, false, k + " is not a number"
		}
		switch {
		case math.IsNaN(f) || f != math.Trunc(f):
			return 0, false, k + " is fractional"
		case f < 0:
			return 0, false, k + " is negative"
		case f > MaxTokenCount:
			return 0, false, k + " is out of range"
		}
		return int64(f), true, ""
	}
	return 0, false, ""
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
	paths, _ := filepath.Glob(filepath.Join(root, "extensions", "amq-squad", "alerts", "*.json"))
	var out []AlertInput
	for _, p := range paths {
		if b := filepath.Base(p); strings.HasPrefix(b, "baseline-") || strings.HasPrefix(b, "covered-") {
			continue
		}
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
		kind := a.Kind
		if kind == "tag" || kind == "release" {
			kind = "ungated_" + kind
		}
		out = append(out, AlertInput{ID: a.ID, Kind: kind, Subject: a.Detail, At: at})
	}
	return out
}

// pushURLMissing lists the seats whose commit ledger (internal/seatcommit,
// <root>/extensions/amq-squad/commits/<seat>.json) records no push URL
// (D1), sorted.
func pushURLMissing(root string) []string {
	dir := filepath.Join(root, "extensions", "amq-squad", "commits")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var l struct {
			Handle  string `json:"handle"`
			PushURL string `json:"push_url"`
		}
		if json.Unmarshal(raw, &l) == nil && l.Handle == name && l.PushURL == "" {
			out = append(out, name)
		}
	}
	return out
}
