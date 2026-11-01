package status

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The lead reply relay (brief 31-4, plan 3.1 section 3.2): the Buzz
// attachment opens one request per phone question, posts it to the lead on
// the request's cockpit thread, and relays the lead's replies on that
// thread to the owner's DM. Its record per request is the file below; the
// attachment (internal/buzz) writes it and status reads it. No record
// carries a message's text: posts and refusals are named by sha256.

// RelayDir is the directory of the relay records of an AMQ root.
func RelayDir(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "relay")
}

// RelayRecordSchema is the relay record's schema.
const RelayRecordSchema = 1

// The names the relay records and status show (never a line of text).
const (
	// RelayRefusedGateShape: a reply with a line shaped like a gate answer
	// or naming an open gate; RelayRefusedGateThread: a post whose thread is
	// a gate question's; RelayPostUnconfirmed: a post whose publish was
	// retried once after a crash and never confirmed (never retried again).
	RelayRefusedGateShape  = "relay_refused_gate_shape"
	RelayRefusedGateThread = "relay_refused_gate_thread"
	RelayPostUnconfirmed   = "relay_post_unconfirmed"
	// RelayRecordInvalid: a record that does not decode strictly or whose
	// fields are not their exact shapes; the relay refuses it whole.
	RelayRecordInvalid = "relay_record_invalid"
)

var relayReasons = map[string]bool{RelayRefusedGateShape: true, RelayRefusedGateThread: true, RelayPostUnconfirmed: true}

var (
	requestIDRe = regexp.MustCompile(`^q-[0-9a-f]{12}$`)
	eventIDRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	shaRe       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// an AMQ message id (its file name grammar)
	messageIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	relayHandle = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	relayRoute  = regexp.MustCompile(`^([a-z0-9][a-z0-9._-]{0,63}/)?[a-z0-9][a-z0-9_-]{0,63}$`)
)

// ValidRequestID reports whether id is a request id: q- and 12 hex.
func ValidRequestID(id string) bool { return requestIDRe.MatchString(id) }

// DecodeRelayRecord decodes one relay record strictly (no unknown field,
// one JSON value) and validates every field against its exact shape;
// requestID is the record's file name without .json.
func DecodeRelayRecord(raw []byte, requestID string) (*RelayRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r RelayRecord
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %s: %v", requestID, RelayRecordInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %s: data after the record", requestID, RelayRecordInvalid)
	}
	if err := r.Validate(requestID); err != nil {
		return nil, err
	}
	return &r, nil
}

// Validate checks every field of the record against its exact shape.
func (r *RelayRecord) Validate(requestID string) error {
	var bad []string
	check := func(ok bool, field string) {
		if !ok {
			bad = append(bad, field)
		}
	}
	optional := func(re *regexp.Regexp, v string) bool { return v == "" || re.MatchString(v) }
	check(r.Schema == RelayRecordSchema, "schema")
	check(requestIDRe.MatchString(r.RequestID) && r.RequestID == requestID, "request_id")
	check(r.Thread == "cockpit/"+r.RequestID, "thread")
	check(relayHandle.MatchString(r.Lead), "lead")
	check(optional(relayRoute, r.Route), "route")
	check(eventIDRe.MatchString(r.RootEvent), "root_event")
	check(optional(eventIDRe, r.PostTo), "post_to")
	check(len(r.Turns) > 0, "turns")
	for _, t := range r.Turns {
		check(eventIDRe.MatchString(t.Event) && optional(messageIDRe, t.MessageID) && !t.At.IsZero(), "turns[]")
	}
	check(optional(messageIDRe, r.LastReply), "last_reply")
	for _, id := range r.Seen {
		check(messageIDRe.MatchString(id), "seen[]")
	}
	for _, p := range r.Posts {
		ok := messageIDRe.MatchString(p.ReplyID) && relayKind(p.Kind) && len(p.Events) > 0 && shaRe.MatchString(p.SHA256) && !p.At.IsZero()
		for _, e := range p.Events {
			ok = ok && eventIDRe.MatchString(e)
		}
		check(ok, "posts[]")
	}
	for _, f := range r.Refusals {
		check(messageIDRe.MatchString(f.ReplyID) && relayReasons[f.Reason] && shaRe.MatchString(f.SHA256) && !f.At.IsZero(), "refusals[]")
	}
	check(optional(messageIDRe, r.Pending), "pending_progress")
	check(!r.UpdatedAt.IsZero(), "updated_at")
	if len(bad) > 0 {
		return fmt.Errorf("%s: %s: %s", requestID, RelayRecordInvalid, strings.Join(bad, ", "))
	}
	return nil
}

func relayKind(k string) bool { return k == "answer" || k == "progress" || k == "result" }

// RelayRecord is one phone request: <root>/extensions/amq-squad/relay/<request id>.json.
type RelayRecord struct {
	Schema    int    `json:"schema"`
	RequestID string `json:"request_id"`
	// Thread is the request's cockpit thread, cockpit/<request id>, in the
	// root that holds the record.
	Thread string `json:"thread"`
	// Lead is the handle whose replies on the thread are relayed (the lead
	// the question was posted to); Route is the name the question was
	// forwarded by (lead or squad/lead), "" when it went to the bound
	// mailbox only. A continuation goes the same way.
	Lead  string `json:"lead"`
	Route string `json:"route"`
	// RootEvent is the owner's first DM of the request: the DM conversation.
	// PostTo is the event every relay post replies to: RootEvent, or ""
	// (a top-level post) when the owner's DM is itself in a gate thread.
	RootEvent string         `json:"root_event"`
	PostTo    string         `json:"post_to"`
	Turns     []RelayTurn    `json:"turns"`
	LastReply string         `json:"last_reply"` // the newest lead reply handled
	Seen      []string       `json:"seen"`       // every lead reply handled (posted, refused, superseded)
	Posts     []RelayPost    `json:"posts"`
	Refusals  []RelayRefusal `json:"refusals"`
	// Pending is the newest progress reply held by the rate bound (its id
	// only: the text is read from the thread when it posts).
	Pending    string     `json:"pending_progress,omitempty"`
	ProgressAt *time.Time `json:"progress_at,omitempty"`
	RelayedAt  *time.Time `json:"relayed_at"`
	Closed     bool       `json:"closed"` // a result reply closed it; a continuation opens it again
	UpdatedAt  time.Time  `json:"updated_at"`
}

// RelayTurn is one owner DM of the request and the AMQ message that took it
// to the lead.
type RelayTurn struct {
	Event     string    `json:"event"`
	MessageID string    `json:"message_id"`
	At        time.Time `json:"at"`
}

// RelayPost is one lead reply posted to the owner's DM (in up to three
// parts, one event each).
type RelayPost struct {
	ReplyID string    `json:"reply_id"`
	Kind    string    `json:"kind"` // answer | progress | result
	Events  []string  `json:"events"`
	SHA256  string    `json:"sha256"` // of the posted text
	At      time.Time `json:"at"`
}

// RelayRefusal is one lead reply the relay did not post: the reason's name
// and the sha256 of the line that caused it, never the line.
type RelayRefusal struct {
	ReplyID string    `json:"reply_id"`
	Reason  string    `json:"reason"`
	SHA256  string    `json:"sha256"`
	At      time.Time `json:"at"`
}

// Request is a phone request's row in the status document: whether the
// lead's answer reached the owner's DM (relayed_at) or what the relay
// refused (reason names only).
type Request struct {
	RequestID string     `json:"request_id"`
	Thread    string     `json:"thread"`
	Lead      string     `json:"lead"`
	Turns     int        `json:"turns"`
	Posts     int        `json:"posts"`
	RelayedAt *time.Time `json:"relayed_at"`
	Refused   []string   `json:"refused"`
	Closed    bool       `json:"closed"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// RequestsTail is how many requests the document carries, newest first.
const RequestsTail = 20

// collectRequests reads the relay records of the root.
func collectRequests(root string, warn func(string, ...any)) []Request {
	paths, _ := filepath.Glob(filepath.Join(RelayDir(root), "*.json"))
	out := []Request{}
	for _, p := range paths {
		id := strings.TrimSuffix(filepath.Base(p), ".json")
		raw, err := os.ReadFile(p)
		var r *RelayRecord
		if err == nil {
			r, err = DecodeRelayRecord(raw, id)
		}
		if err != nil {
			// the relay refuses the record whole; the row names it when the
			// file name is a request id, the warning either way
			warn("relay record %s unreadable (%s)", filepath.Base(p), RelayRecordInvalid)
			if requestIDRe.MatchString(id) {
				var at time.Time
				if fi, err := os.Stat(p); err == nil {
					at = fi.ModTime().UTC()
				}
				out = append(out, Request{RequestID: id, Thread: "cockpit/" + id, Refused: []string{RelayRecordInvalid}, UpdatedAt: at})
			}
			continue
		}
		row := Request{RequestID: r.RequestID, Thread: r.Thread, Lead: r.Lead, Turns: len(r.Turns), Posts: len(r.Posts),
			RelayedAt: r.RelayedAt, Refused: []string{}, Closed: r.Closed, UpdatedAt: r.UpdatedAt.UTC()}
		for _, f := range r.Refusals {
			row.Refused = append(row.Refused, f.Reason)
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].RequestID < out[j].RequestID
	})
	if len(out) > RequestsTail {
		out = out[:RequestsTail]
	}
	return out
}
