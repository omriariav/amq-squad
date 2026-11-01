package scheduler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The launch budget's second mark (brief 31-1, plan 7.3): a launched seat's
// first AMQ message. The tick asks Config.FirstMessage for each seat this
// incarnation launched that has not sent one yet and logs
// seat_first_message once, its reason the message's own created stamp (the
// decision's at is the tick's). An adopted seat carries no launch of this
// incarnation and gets no mark.

// KindSeatFirstMessage is the decision of a seat's first AMQ message.
const KindSeatFirstMessage = "seat_first_message"

// requestAt is when this incarnation issued the request id (zero when it
// is not one of its open requests).
func (s *Scheduler) requestAt(id string) time.Time {
	if id == "" {
		return time.Time{}
	}
	for _, r := range s.requests {
		if r.req.ID == id {
			return r.at
		}
	}
	return time.Time{}
}

func (s *Scheduler) noteFirstMessages(cfg Config, log func(Decision)) {
	if cfg.FirstMessage == nil {
		return
	}
	handles := make([]string, 0, len(s.seats))
	for h := range s.seats {
		handles = append(handles, h)
	}
	sort.Strings(handles)
	for _, h := range handles {
		st := s.seats[h]
		if st.firstSent || st.launchedAt.IsZero() {
			continue
		}
		if at, ok := cfg.FirstMessage(h, st.launchedAt); ok {
			st.firstSent = true
			log(Decision{Kind: KindSeatFirstMessage, Seat: h, Reason: at.UTC().Format(time.RFC3339Nano)})
		}
	}
}

// OutboxFirstMessage reads a seat's first sent AMQ message at or after
// since from its outbox (<root>/agents/<handle>/outbox/sent/*.md, each
// file's JSON front matter carrying its created stamp). A file last
// modified before since is skipped unread, so a seat that sends nothing
// costs one directory read per tick.
func OutboxFirstMessage(root string) func(handle string, since time.Time) (time.Time, bool) {
	return func(handle string, since time.Time) (time.Time, bool) {
		dir := filepath.Join(root, "agents", handle, "outbox", "sent")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return time.Time{}, false
		}
		var first time.Time
		floor := since.Add(-time.Second) // a file system's mtime may be second-precise
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if info, err := e.Info(); err != nil || info.ModTime().Before(floor) {
				continue
			}
			created, ok := messageCreated(filepath.Join(dir, e.Name()))
			if !ok || created.Before(since) {
				continue
			}
			if first.IsZero() || created.Before(first) {
				first = created
			}
		}
		return first, !first.IsZero()
	}
}

// messageCreated reads an AMQ message file's front matter ("---json" ...
// "---") for its created stamp.
func messageCreated(path string) (time.Time, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	_, rest, ok := bytes.Cut(raw, []byte("---json"))
	if !ok {
		return time.Time{}, false
	}
	head, _, ok := bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return time.Time{}, false
	}
	var h struct {
		Created time.Time `json:"created"`
	}
	if json.Unmarshal(head, &h) != nil || h.Created.IsZero() {
		return time.Time{}, false
	}
	return h.Created, true
}
