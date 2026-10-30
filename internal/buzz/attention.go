package buzz

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

// KindVerifyTimeout is the attention kind of a hung verification (ruling 20
// point 3; the 5-amq-B fix round): an owner alert in the gate's squad root,
// which the status contract shows in its attention queue, with the gate id,
// while the gate is open.
const KindVerifyTimeout = "verify_timeout"

func verifyTimeoutID(gateID string) string { return "verify-timeout-" + gateID }

// raiseVerifyTimeout writes the owner alert of a hung verification into the
// squad root of the core that holds the gate (written once per gate).
func raiseVerifyTimeout(c *gateverify.Core, gateID string, limit time.Duration, now time.Time) error {
	if c == nil || c.Store.Root == "" || gateID == "" || gateID == "-" {
		return errors.New("no squad root for the gate")
	}
	return mergetrain.WriteAlert(c.Store.Root, mergetrain.Alert{
		ID: verifyTimeoutID(gateID), Kind: KindVerifyTimeout, GateID: gateID, At: now.UTC().Format(time.RFC3339),
		Detail: "gate " + gateID + ": verify_timeout (no result within " + limit.String() + "); the gate stays open, answer again in its thread",
	})
}

// clearVerifyTimeout removes the alert once a verification of the gate
// finished.
func clearVerifyTimeout(c *gateverify.Core, gateID string) {
	if c == nil || c.Store.Root == "" || gateID == "" {
		return
	}
	// a stale row is hidden anyway once the gate closes (status.Build)
	_ = os.Remove(filepath.Join(mergetrain.AlertsDir(c.Store.Root), verifyTimeoutID(gateID)+".json"))
}

// holderOf is the one core whose store holds the gate id, else Core.
func (h *Handler) holderOf(gateID string) *gateverify.Core {
	var found *gateverify.Core
	for _, c := range append([]*gateverify.Core{h.Core}, h.Cores...) {
		if _, err := c.Store.Load(gateID); err == nil {
			if found != nil {
				return h.Core
			}
			found = c
		}
	}
	if found == nil {
		return h.Core
	}
	return found
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// ErrInboxUnreadable is an attach inbox the amq CLI answered with output
// that does not parse: an owner alert (kind buzz_inbox_unreadable) in the
// primary squad's status document until the inbox reads again.
var ErrInboxUnreadable = errors.New("the attach inbox is unreadable")

// KindInboxUnreadable is the attention kind of an unreadable attach inbox.
const KindInboxUnreadable = "buzz_inbox_unreadable"

const inboxAlertID = "buzz-inbox-unreadable"

func raiseInboxAlert(c *gateverify.Core, cause error, now time.Time) error {
	if c == nil || c.Store.Root == "" {
		return errors.New("no squad root")
	}
	return mergetrain.WriteAlert(c.Store.Root, mergetrain.Alert{ID: inboxAlertID, Kind: KindInboxUnreadable, At: now.UTC().Format(time.RFC3339),
		Detail: "the Buzz attach inbox is unreadable (" + cause.Error() + "); owner DMs are not handled"})
}

func clearInboxAlert(c *gateverify.Core) {
	if c == nil || c.Store.Root == "" {
		return
	}
	_ = os.Remove(filepath.Join(mergetrain.AlertsDir(c.Store.Root), inboxAlertID+".json"))
}
