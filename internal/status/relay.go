package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
		raw, err := os.ReadFile(p)
		var r RelayRecord
		if err == nil {
			err = json.Unmarshal(raw, &r)
		}
		if err != nil || r.RequestID == "" || r.RequestID+".json" != filepath.Base(p) {
			warn("relay record %s unreadable", filepath.Base(p))
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
