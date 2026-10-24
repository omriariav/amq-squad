package buzz

import (
	"errors"
	"fmt"
	"regexp"
)

// OperatorHandle is the operator session's mailbox: the owner's `user`
// handle (briefs lead ruling 16, 2b-5).
const OperatorHandle = "user"

// CarrierHandle is the AMQ sender of every message the attachment delivers
// from the phone. It is the binding's carrier, the handle 3-1's mailbox label
// path accepts a nostr:<event id> label from.
const CarrierHandle = "buzz"

// AttachToOperator is the one-line switch for brief 4-3. Until the operator
// session exists, the Buzz agent attaches to one squad lead, through the same
// code path (brief 4-5 dependencies). When 4-3 lands, set it to true: the
// phone's mailbox becomes the operator's `user` handle and a message that
// names a lead is forwarded from there.
const AttachToOperator = false

// Target is the mailbox the Buzz agent is attached to, and the leads a phone
// message may name.
type Target struct {
	Root   string   // the AMQ root of the mailbox
	Handle string   // the bound mailbox
	Leads  []string // leads a message may name; the bound lead is one of them
}

var handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Resolve picks the mailbox: the operator's `user` handle when
// AttachToOperator is set, else the single squad lead.
func Resolve(root, lead string, leads []string) (Target, error) {
	return resolve(AttachToOperator, root, lead, leads)
}

func resolve(toOperator bool, root, lead string, leads []string) (Target, error) {
	if root == "" {
		return Target{}, errors.New("attachment needs the AMQ root of the mailbox")
	}
	t := Target{Root: root}
	for _, l := range leads {
		if !handleRe.MatchString(l) {
			return Target{}, fmt.Errorf("lead handle %q is not an AMQ handle", l)
		}
		t.Leads = appendUnique(t.Leads, l)
	}
	if toOperator {
		t.Handle = OperatorHandle
		return t, nil
	}
	if !handleRe.MatchString(lead) {
		return Target{}, errors.New("until the operator session lands (brief 4-3), the Buzz agent attaches to one squad lead: name it with --lead")
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
