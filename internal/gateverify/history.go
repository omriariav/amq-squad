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

// CheckHistory reads the gate question's history from the relay, the
// checks upstream amq-remote's CheckHistory runs for a Claude approval
// (S1 open question 3):
//
//   - every edit of the question (kind 40003, by the body or by the owner,
//     whom Buzz lets edit the body's messages) must show the record's text
//     unchanged and carry no attachment; the core never edits a question;
//   - no deletion (kinds 5, 9005) may name the question or an edit of it;
//   - no deletion by the body may exist at all, and none by the owner since
//     the question, wherever it points: the relay hides a deleted edit, and
//     an owner who deletes the answer withdraws it.
//
// An edit or deletion that fails is question_altered. A read that errors,
// or a full page of edits, is history_unknown: the core does not know the
// history and does not accept.
func CheckHistory(ctx context.Context, r Relay, question nostr.Event, owner string) error {
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
	for _, q := range []struct {
		f    Filter
		what string
	}{
		{Filter{Kinds: deletionKinds, Tags: map[string][]string{"e": ids}}, "the question or an edit of it was deleted"},
		{Filter{Kinds: deletionKinds, Authors: []string{question.PubKey}}, "a deletion by the body exists"},
		{Filter{Kinds: deletionKinds, Authors: []string{owner}, Since: question.CreatedAt}, "a deletion by the owner since the question exists"},
	} {
		got, err := r.Query(ctx, q.f)
		if err != nil {
			return reject(RejectHistory, "read deletions: %v", err)
		}
		for _, raw := range got {
			if d, err := nostr.ParseEvent(raw); err == nil && d.VerifySignature() == nil {
				return reject(RejectAltered, "%s", q.what)
			}
		}
	}
	return nil
}

func hasTag(e nostr.Event, name string) bool {
	for _, t := range e.Tags {
		if len(t) > 0 && t[0] == name {
			return true
		}
	}
	return false
}
