package gateverify

import (
	"context"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Buzz event kinds that change or hide a message (upstream
// internal/remote/buzzio: KindEdit 40003, KindDeletion 5 (NIP-09),
// KindGroupDeletion 9005 (NIP-29)), and the NIP-25 reaction kind a
// reaction cleanup names in its NIP-09 `k` tag.
const (
	KindEdit          = 40003
	KindDeletion      = 5
	KindGroupDeletion = 9005
	KindReaction      = 7
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
//   - no deletion (kinds 5, 9005), by any key, may name the question, an
//     edit of it or the answer;
//   - no other deletion by the body may exist, and none by the owner since
//     the question, wherever it points: the relay hides a deleted edit, and
//     an owner who deletes the answer withdraws it. A reaction cleanup is
//     the one exception (5-amq-B acceptance 10, after upstream #1284): a
//     deletion whose NIP-09 `k` tags all name kind 7 and that names none of
//     those ids hid a reaction, not text, so it is not an alteration.
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
	for _, q := range []struct {
		f       Filter
		what    string
		cleanup bool // a reaction cleanup does not count
	}{
		{Filter{Kinds: deletionKinds, Tags: map[string][]string{"e": ids}}, "the question, an edit of it or the answer was deleted", false},
		{Filter{Kinds: deletionKinds, Authors: []string{question.PubKey}}, "a deletion by the body exists", true},
		{Filter{Kinds: deletionKinds, Authors: []string{owner}, Since: question.CreatedAt}, "a deletion by the owner since the question exists", true},
	} {
		got, err := r.Query(ctx, q.f)
		if err != nil {
			return reject(RejectHistory, "read deletions: %v", err)
		}
		for _, raw := range got {
			d, err := nostr.ParseEvent(raw)
			if err != nil || d.VerifySignature() != nil {
				continue
			}
			if q.cleanup && reactionCleanup(d, ids) {
				continue
			}
			return reject(RejectAltered, "%s", q.what)
		}
	}
	return nil
}

// reactionCleanup reports whether a deletion hid only reactions: it carries
// at least one NIP-09 `k` tag, every one names kind 7, and it names none of
// the ids whose history the core checks.
func reactionCleanup(d nostr.Event, ids []string) bool {
	ks := nostr.TagValues(d, "k")
	if len(ks) == 0 {
		return false
	}
	for _, k := range ks {
		if k != "7" {
			return false
		}
	}
	for _, e := range nostr.TagValues(d, "e") {
		for _, id := range ids {
			if e == id {
				return false
			}
		}
	}
	return true
}

func hasTag(e nostr.Event, name string) bool {
	for _, t := range e.Tags {
		if len(t) > 0 && t[0] == name {
			return true
		}
	}
	return false
}
