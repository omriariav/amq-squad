package buzz

import (
	"errors"

	"fmt"
	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"regexp"
)

// OperatorHandle is the operator session's mailbox: the owner's `user`
// handle (briefs lead ruling 16, 2b-5).
const OperatorHandle = fleet.OperatorHandle

// CarrierHandle is the AMQ sender of every message the attachment delivers
// from the phone. It is the binding's carrier, the handle 3-1's mailbox label
// path accepts a nostr:<event id> label from.
const CarrierHandle = "buzz"

// AttachToOperator is the one-line switch of brief 4-5, set when 4-3 landed:
// the phone's mailbox is the operator's `user` handle and a message that
// names a lead is forwarded from there to that squad. An explicit --lead
// still attaches to one squad lead through the same code path.
const AttachToOperator = true

// Target is the mailbox the Buzz agent is attached to, and the leads a phone
// message may name.
type Target struct {
	Root   string   // the AMQ root of the mailbox
	Handle string   // the bound mailbox
	Leads  []string // leads a message may name; the bound lead is one of them
	// Registry decides whether a named lead is a lead at delivery time;
	// without one a message is never forwarded (4-5 review item 3).
	Registry LeadRegistry
}

var (
	handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	// destRe is a destination a message may name: a handle, or squad/handle
	destRe = regexp.MustCompile(`^([a-z0-9][a-z0-9._-]{0,63}/)?[a-z0-9][a-z0-9_-]{0,63}$`)
)

// Resolve picks the mailbox: an explicitly named squad lead (single-squad
// mode, root is that squad's root), else the operator's `user` handle when
// AttachToOperator is set (root is the operator session's root).
func Resolve(root, lead string, leads []string) (Target, error) {
	return resolve(AttachToOperator && lead == "", root, lead, leads)
}

func resolve(toOperator bool, root, lead string, leads []string) (Target, error) {
	if root == "" {
		return Target{}, errors.New("attachment needs the AMQ root of the mailbox")
	}
	t := Target{Root: root}
	for _, l := range leads {
		if !destRe.MatchString(l) {
			return Target{}, fmt.Errorf("lead handle %q is not an AMQ handle", l)
		}
		t.Leads = appendUnique(t.Leads, l)
	}
	if toOperator {
		t.Handle = OperatorHandle
		return t, nil
	}
	if !handleRe.MatchString(lead) {
		return Target{}, errors.New("name the squad lead to attach to with --lead (or attach to the operator session)")
	}
	t.Handle = lead
	t.Leads = appendUnique(t.Leads, lead)
	return t, nil
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
