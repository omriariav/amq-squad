package buzz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
)

// SeenWindow is how long a handled event id is remembered: past it serve
// would not admit the DM again (its admission window is 2 minutes) and the
// handler's own routing window refuses it; a gate answer is single-use in
// the gate store regardless.
const SeenWindow = 2 * RouteWindow

// SeenPath is the durable once-only record of a share root (B-P7): handled
// event ids and when, under the share root's extension directory, which
// guard protects (~/.amq-squad/buzz/).
func SeenPath(shareRoot string) string {
	return filepath.Join(shareRoot, "extensions", "amq-squad", "buzz-seen.json")
}

type seenEntry struct {
	At   time.Time `json:"at"`
	Gate string    `json:"gate"` // the gate an answer named, "-" for a route
}

type seenDoc struct {
	Schema int                  `json:"schema"`
	Events map[string]seenEntry `json:"events"`
}

// seenSet is the handled event ids, in memory and, with a path, on disk
// through internal/atomicfile, pruned to SeenWindow.
type seenSet struct {
	path   string
	loaded bool
	events map[string]seenEntry
}

func (s *seenSet) load() error {
	if s.loaded {
		return nil
	}
	s.events = map[string]seenEntry{}
	if s.path != "" {
		raw, err := os.ReadFile(s.path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return err
		default:
			var d seenDoc
			if err := json.Unmarshal(raw, &d); err != nil {
				return fmt.Errorf("%s is unreadable: %w", s.path, err)
			}
			for k, v := range d.Events {
				s.events[k] = v
			}
		}
	}
	s.loaded = true
	return nil
}

// get reports whether id was handled within the window, and its gate.
func (s *seenSet) get(id string, now time.Time) (string, bool, error) {
	if err := s.load(); err != nil {
		return "", false, err
	}
	e, ok := s.events[id]
	if !ok || now.Sub(e.At) > SeenWindow {
		return "", false, nil
	}
	return e.Gate, true, nil
}

// put records id, prunes what is past the window, and writes the record.
func (s *seenSet) put(id, gate string, now time.Time) error {
	if err := s.load(); err != nil {
		return err
	}
	for k, v := range s.events {
		if now.Sub(v.At) > SeenWindow {
			delete(s.events, k)
		}
	}
	s.events[id] = seenEntry{At: now.UTC(), Gate: gate}
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(seenDoc{Schema: 1, Events: s.events}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(s.path, append(raw, '\n'), 0o600)
}

// hungBeforeFetch is a verification that hung before the event was read:
// the gate the delivery names is used only to place the owner's attention
// row (no authority comes from it), and nothing is marked.
func (h *Handler) hungBeforeFetch(ctx context.Context, hd *Handled, m Prompt, limit time.Duration, step string) {
	body := strings.TrimSpace(m.Body)
	if i := strings.LastIndex(body, ServePromptSuffix); i >= 0 {
		body = strings.TrimSpace(body[:i])
	}
	gate := "-"
	if isGateAnswer(body) {
		gate = answerGateID(body)
	}
	var core = h.Core
	if gate != "-" {
		core = h.holderOf(gate)
	}
	h.hung(ctx, hd, core, gate, limit, step)
}
