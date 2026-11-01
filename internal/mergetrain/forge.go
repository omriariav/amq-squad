package mergetrain

import "context"

// FileChange is one file of a PR's diff.
type FileChange struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previous_path,omitempty"` // a rename's source
	Status       string `json:"status,omitempty"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
}

// CheckRun is one CI result at a commit: a check run (with its app and id)
// or a commit status (app 0).
type CheckRun struct {
	ID         int64  `json:"id,omitempty"`
	Name       string `json:"name"`
	AppID      int    `json:"app_id,omitempty"`
	Status     string `json:"status"`     // completed, in_progress, queued
	Conclusion string `json:"conclusion"` // success, failure, ...
}

// RequiredCheck is one required CI check as the protection rule names it:
// the context and, when the rule pins one, the app that must report it.
type RequiredCheck struct {
	Context string `json:"context"`
	AppID   int    `json:"app_id,omitempty"` // 0: the rule pins no app
}

// ForcePush is one head_ref_force_pushed event on a PR.
type ForcePush struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Actor  string `json:"actor,omitempty"`
}

// PRInfo is what the train reads about one PR at one head. Files is the diff
// of the pinned (base, head) pair from the compare endpoint, never the PR's
// live files list.
type PRInfo struct {
	Number      int          `json:"number"`
	Title       string       `json:"title,omitempty"` // the gate question's quoted title (brief 3.1.1-1)
	State       string       `json:"state"`           // OPEN, CLOSED, MERGED
	Draft       bool         `json:"draft"`
	Repository  string       `json:"repository"` // the base repository, owner/name
	BaseRef     string       `json:"base_ref"`
	BaseSHA     string       `json:"base_sha"`
	HeadSHA     string       `json:"head_sha"`
	Mergeable   string       `json:"mergeable"` // MERGEABLE, CONFLICTING, UNKNOWN
	Files       []FileChange `json:"files"`
	ForcePushes []ForcePush  `json:"force_pushes,omitempty"`
	// HistoryIncomplete says the force-push timeline could not be read whole
	// (the page bound, a rate limit), with the reason: history_incomplete.
	HistoryIncomplete string `json:"history_incomplete,omitempty"`
	// MergeCommit is the merge commit of a MERGED PR ("" otherwise).
	MergeCommit string `json:"merge_commit,omitempty"`
}

// Protection is the server's branch protection for one branch as it binds
// the merging identity (the owner, an admin): rules the owner can bypass do
// not count.
type Protection struct {
	Checks            []RequiredCheck `json:"required_checks"`
	DirectPushRefused bool            `json:"direct_push_refused"`
	ForcePushRefused  bool            `json:"force_push_refused"`
	Sources           []string        `json:"sources,omitempty"` // ruleset ids, "classic"
	Notes             []string        `json:"notes,omitempty"`   // why something does not count
}

// Met is the brief's condition: the server requires CI and refuses direct
// push, both enforced against the owner.
func (p Protection) Met() bool { return len(p.Checks) > 0 && p.DirectPushRefused }

// Forge is the train's view of the code host. GH runs the owner's gh; the
// fixture tests run a fake gh over a local bare repository.
type Forge interface {
	// PR reads the PR's state and head; its Files are left empty.
	PR(ctx context.Context, repository string, number int) (PRInfo, error)
	// Diff is the change from base to head (three-dot, from their merge base)
	// at those exact SHAs.
	Diff(ctx context.Context, repository, base, head string) ([]FileChange, error)
	Checks(ctx context.Context, repository, sha string) ([]CheckRun, error)
	BranchHead(ctx context.Context, repository, branch string) (string, error)
	Protection(ctx context.Context, repository, branch string) (Protection, error)
	Ready(ctx context.Context, repository string, number int) error
	// Rebase rebases the PR's head onto its base on the server, only if the
	// head is still expect, and returns the head the rebase produced.
	Rebase(ctx context.Context, repository string, number int, expect string) (string, error)
	// Merge squashes the PR into its base only if its head is still head;
	// it returns the merge commit.
	Merge(ctx context.Context, repository string, number int, head string) (string, error)
}

// MaxCompareFiles is the compare endpoint's file limit: a diff that reaches
// it cannot be verified whole.
const MaxCompareFiles = 300
