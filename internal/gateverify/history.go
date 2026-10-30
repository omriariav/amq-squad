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

// maxHistoryDeletions bounds one read of the deletions since the question; a
// full page is an unknown history.
const maxHistoryDeletions = 200

// CheckHistory reads the history of the gate question and of the owner's
// answer from the relay, the checks upstream amq-remote's CheckHistory runs
// for a Claude approval (S1 open question 3), with 5-amq-B's deletion rules:
//
//   - every edit of the question (kind 40003, by the body or by the owner,
//     whom Buzz lets edit the body's messages) must show the record's text
//     unchanged and carry no attachment; the core never edits a question;
//   - the owner's answer has no edit: the signed answer is what the core
//     verifies, and an owner who edits it has changed it;
//   - visible targets, the target-and-signer rule (fix round 1): a deletion
//     (kinds 5, 9005) signed by the owner's pinned key that names the
//     question, an edit of it or the answer is question_altered; a deletion
//     from any key other than the owner's and the body's is ignored, as a
//     relay ignores it;
//   - hidden targets (fix round 2, item 3), the deletions since `since`
//     (the question, or the history re-ask that showed the question's text
//     again): a deletion signed by the body key is altered_history whatever
//     it names, since amq-squad's body never posts a reaction and never
//     deletes anything (the grep is in the 5-amq-B log); a deletion signed
//     by the owner whose target the relay no longer returns (it may have
//     hidden an edit of the question the owner answered) is altered_history
//     unless every k tag on it names kind 7, an owner reaction cleanup.
//     altered_history is answered with a re-ask that shows the question as
//     raised (Core.historyReask), so it costs the owner one re-answer.
//
// Recorded limit: a holder of the body key can alter a question and cover
// the edit only until the verifier sees the deletion; the same-UID model
// already concedes the body key (plan 7.3, R33). An owner deletion tagged
// k=7 is trusted to be a reaction cleanup.
//
// A read that errors, or a full page of edits or deletions, is
// history_unknown: the core does not know the history and does not accept.
func CheckHistory(ctx context.Context, r Relay, question, answer nostr.Event, owner string, since int64) error {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	body := question.PubKey
	authors := []string{body, owner}
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
	// hidden targets: the body's and the owner's deletions since the window
	recent, err := r.Query(ctx, Filter{Kinds: deletionKinds, Authors: authors, Since: since, Limit: maxHistoryDeletions})
	if err != nil {
		return reject(RejectHistory, "read deletions since the question: %v", err)
	}
	if len(recent) >= maxHistoryDeletions {
		return reject(RejectHistory, "%d or more deletions since the question; the history is not known", maxHistoryDeletions)
	}
	for _, raw := range recent {
		d, err := nostr.ParseEvent(raw)
		if err != nil || d.VerifySignature() != nil || d.CreatedAt < since {
			continue
		}
		switch d.PubKey {
		case body:
			return reject(RejectAlteredHistory, "the body key deleted %v since the question; amq-squad's body never deletes", nostr.TagValues(d, "e"))
		case owner:
			if reactionCleanupTags(d) {
				continue
			}
			hidden, err := hiddenTargets(ctx, r, nostr.TagValues(d, "e"))
			if err != nil {
				return reject(RejectHistory, "read a deletion's targets: %v", err)
			}
			if len(hidden) > 0 {
				return reject(RejectAlteredHistory, "the owner deleted %v since the question and the relay hides it", hidden)
			}
		}
	}
	return nil
}

// reactionCleanupTags reports whether every k tag of a deletion names kind
// 7 (and there is one): an owner reaction cleanup.
func reactionCleanupTags(d nostr.Event) bool {
	ks := nostr.TagValues(d, "k")
	if len(ks) == 0 {
		return false
	}
	for _, k := range ks {
		if k != "7" {
			return false
		}
	}
	return true
}

// hiddenTargets are the ids the relay no longer returns.
func hiddenTargets(ctx context.Context, r Relay, targets []string) ([]string, error) {
	var valid []string
	for _, t := range targets {
		if nostr.IsHex64(t) {
			valid = append(valid, t)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	got, err := r.Query(ctx, Filter{IDs: valid})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, raw := range got {
		if e, err := nostr.ParseEvent(raw); err == nil && e.CheckID() {
			seen[e.ID] = true
		}
	}
	var hidden []string
	for _, t := range valid {
		if !seen[t] {
			hidden = append(hidden, t)
		}
	}
	return hidden, nil
}

// ownerDeletes is the visible-target rule (fix round 1): a kind 5 or 9005
// alters the answer when it is signed by the owner's pinned key and its e
// tags name the answer, the question or an edit of the question. A
// deletion from any other key is ignored here, as a relay ignores it
// (NIP-09: only the author's deletion takes effect); the body's deletions
// are refused by the hidden-target rule above.
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
