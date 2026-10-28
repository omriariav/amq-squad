package gateverify

import (
	"context"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Buzz event kinds that change or hide a message (upstream
// internal/remote/buzzio: KindEdit 40003, KindDeletion 5 (NIP-09),
// KindGroupDeletion 9005 (NIP-29)).
const (
	KindEdit          = 40003
	KindDeletion      = 5
	KindGroupDeletion = 9005
)

const (
	historyTimeout  = 10 * time.Second
	maxHistoryEdits = 20
)

// CheckHistory reads the history of the gate question and of the owner's
// answer from the relay, the checks upstream amq-remote's CheckHistory runs
// for a Claude approval (S1 open question 3):
//
//   - every edit of the question (kind 40003, by the body or by the owner,
//     whom Buzz lets edit the body's messages) must show the record's text
//     unchanged and carry no attachment; the core never edits a question;
//   - the owner's answer has no edit: the signed answer is what the core
//     verifies, and an owner who edits it has changed it;
//   - no deletion (kinds 5, 9005) signed by the owner's pinned key may name
//     the question, an edit of it or the answer: an owner who deletes the
//     answer withdraws it (ownerDeletes). A deletion from any other key,
//     the body's reaction cleanup included, is no alteration (5-amq-B fix
//     round, after upstream #1284).
//
// Recorded limit: a deletion the relay honours hides its target, so an edit
// of the question that was deleted again is not seen, and the owner's
// deletion of such an edit names an id this check does not know; it is not
// refused. Only the owner (who signs the answer) or the body key (held by
// the core and serve alone) can make that edit and deletion.
//
// An edit or deletion that fails is question_altered. A read that errors,
// or a full page of edits, is history_unknown: the core does not know the
// history and does not accept.
func CheckHistory(ctx context.Context, r Relay, question, answer nostr.Event, owner string) error {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	authors := []string{question.PubKey, owner}
	deletionKinds := []int{KindDeletion, KindGroupDeletion}
	edits, err := r.Query(ctx, Filter{Kinds: []int{KindEdit}, Authors: authors, Tags: map[string][]string{"e": {question.ID}}, Limit: maxHistoryEdits})
	if err != nil {
		return reject(RejectHistory, "read the question's edits: %v", err)
	}
	if len(edits) >= maxHistoryEdits {
		return reject(RejectHistory, "the question has %d or more edits; its history is not known", maxHistoryEdits)
	}
	ids := []string{question.ID}
	for _, raw := range edits {
		ed, err := nostr.ParseEvent(raw)
		if err != nil || ed.VerifySignature() != nil {
			continue // not an event anyone signed; the relay is not the source of truth
		}
		if ed.Kind != KindEdit || ed.Content != question.Content || hasTag(ed, "imeta") {
			return reject(RejectAltered, "an edit of the gate question shows other text")
		}
		ids = append(ids, ed.ID)
	}
	if answer.ID != "" {
		got, err := r.Query(ctx, Filter{Kinds: []int{KindEdit}, Authors: []string{owner}, Tags: map[string][]string{"e": {answer.ID}}, Limit: 1})
		if err != nil {
			return reject(RejectHistory, "read the answer's edits: %v", err)
		}
		for _, raw := range got {
			if ed, err := nostr.ParseEvent(raw); err == nil && ed.VerifySignature() == nil {
				return reject(RejectAltered, "the owner's answer was edited")
			}
		}
		ids = append(ids, answer.ID)
	}
	got, err := r.Query(ctx, Filter{Kinds: deletionKinds, Authors: []string{owner}, Tags: map[string][]string{"e": ids}})
	if err != nil {
		return reject(RejectHistory, "read deletions: %v", err)
	}
	for _, raw := range got {
		d, err := nostr.ParseEvent(raw)
		if err != nil || d.VerifySignature() != nil {
			continue // not an event anyone signed; the relay is not the source of truth
		}
		if ownerDeletes(d, owner, ids) {
			return reject(RejectAltered, "the owner deleted the question, an edit of it or the answer")
		}
	}
	return nil
}

// ownerDeletes is the deletion rule (5-amq-B fix round, ruling on
// acceptance 10): a kind 5 or 9005 alters the answer only when it is signed
// by the owner's pinned key and its e tags name the answer, the question or
// an edit of the question. A deletion from any other key is ignored, as a
// relay ignores it (NIP-09: only the author's deletion takes effect); a
// reaction cleanup names only the reaction's id (v0.116.2
// internal/remote/buzzio/desktop_proof.go, lines 220 to 224, buzz-acp's 👀
// and 💬 cleanup), so the body's, the attachment's or Buzz Desktop's
// cleanup is never an alteration.
func ownerDeletes(d nostr.Event, owner string, ids []string) bool {
	if d.PubKey != owner || (d.Kind != KindDeletion && d.Kind != KindGroupDeletion) {
		return false
	}
	for _, e := range nostr.TagValues(d, "e") {
		for _, id := range ids {
			if e == id {
				return true
			}
		}
	}
	return false
}

func hasTag(e nostr.Event, name string) bool {
	for _, t := range e.Tags {
		if len(t) > 0 && t[0] == name {
			return true
		}
	}
	return false
}
