// Package releaseflow is the 3.0 release gate flow (plan 7.1, R30, 16.1;
// brief 3-2): tag and release always stop for the owner. The flow writes the
// release into the compoundrelease store (prepared manifest, two children),
// and each child's question is a typed gate raised through 3-1's gate store
// writer: tag (target "git tag <tag> <commit> on <owner/repo>") and
// github_release ("gh release create <tag> on <owner/repo>"). Only after the
// owner's verified answer for a child (the full owner-signature check) does
// the flow run that child, under the owner's identity outside any seat: the
// tag first, then the release. A child without a verified answer is refused
// and nothing runs. The compoundrelease writer is the record of the release:
// a child runs only against the active manifest's gate for it, and a done
// marker keeps a retry from running it twice.
package releaseflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Executor performs the release actions under the owner's identity. The
// release is published in two steps (fix 2, P1-6): created as a draft, read
// back and compared, then published; GitHub ignores target_commitish for an
// existing tag, so the draft's tag is resolved to its commit and compared to
// the approved SHA before anything is public.
type Executor interface {
	Tag(ctx context.Context, repository, tag, commit string) error
	// TagCommit resolves a remote tag to its commit ("" when it does not exist).
	TagCommit(ctx context.Context, repository, tag string) (string, error)
	// CommitExists reports whether the remote has the commit at all.
	CommitExists(ctx context.Context, repository, commit string) (bool, error)
	// BranchContains reports whether the remote branch contains the commit
	// (false when either is unknown to the remote).
	BranchContains(ctx context.Context, repository, branch, commit string) (bool, error)
	// TagProtected reports whether a tag rule restricts update and deletion
	// of this tag, and why not.
	TagProtected(ctx context.Context, repository, tag string) (bool, string, error)
	// CreateDraft creates the release as a draft naming the tag and, as its
	// target_commitish, the approved commit.
	CreateDraft(ctx context.Context, repository, tag, commit, notes string) (Draft, error)
	// ReadDraft reads a release back by id; found is false when it is gone.
	ReadDraft(ctx context.Context, repository string, id int64) (d Draft, found bool, err error)
	PublishDraft(ctx context.Context, repository string, id int64) (string, error)
	DeleteRelease(ctx context.Context, repository string, id int64) error
	// FindDrafts lists the repository's releases whose body carries the
	// creation nonce (5 re-pass, P2-4): a draft whose POST succeeded but
	// whose response or record was lost.
	FindDrafts(ctx context.Context, repository, nonce string) ([]Draft, error)
}

// Draft is a release as the flow created and reads it back. GitHub keeps a
// published release at its id: a read after publication finds it with
// Draft false.
type Draft struct {
	ID              int64  `json:"id"`
	Tag             string `json:"tag"`
	TargetCommitish string `json:"target_commitish"`
	Draft           bool   `json:"draft"`
	URL             string `json:"html_url,omitempty"`
}

// intent is the creation intent written before the draft's POST (5 re-pass,
// P2-4): the tag, the target and the nonce the draft's body carries.
type intent struct {
	Tag    string `json:"tag"`
	Target string `json:"target"`
	Nonce  string `json:"nonce"`
}

// NonceMarker is how the draft's body carries the creation nonce: an HTML
// comment, invisible in the rendered notes.
func NonceMarker(nonce string) string { return "<!-- amq-squad-release-nonce: " + nonce + " -->" }

// ErrDraftEdited: the draft was changed between creation and publication.
var ErrDraftEdited = errors.New("draft_edited")

// ErrProtectionUnmet: the repository has no tag rule restricting update and
// deletion of the release's tag, so no release runs (protection_unmet).
var ErrProtectionUnmet = errors.New("protection_unmet")

// Flow is one release series: its compoundrelease store and the session's
// gate store.
type Flow struct {
	Release  *compoundrelease.Store
	Gates    gatestore.Store
	Raise    func(gatestore.Spec) (gatestore.Record, error) // 3-1's writer: record, then question
	Accepted func(gatestore.Record) error                   // the full owner check (gateverify.Verifier.Accepted)
	Exec     Executor
	Lead     string
	Squad    string // this squad's identity, set on the gates it raises and read back (mergetrain.Squad)
	Now      func() time.Time
	// Body is the GitHub release body: the release notes file's content
	// (3.0.2). Its digest is in the spec recorded at prepare (Spec), and
	// publish refuses a body that is not that file's content.
	Body string
	// RepoDir is the local repository the commit must resolve in, and Branch
	// the remote branch that must contain it, before any gate is raised.
	RepoDir string
	Branch  string
}

// The named refusals of 3.0.2.
var (
	// ErrBodyMismatch: the release body at publish is not the content of the
	// notes file whose digest was recorded at prepare.
	ErrBodyMismatch = errors.New("release_body_mismatch")
	// ErrNoteCarriesDigest: --notes already contains the digest label; the
	// digest line is written by prepare only.
	ErrNoteCarriesDigest = errors.New("note_carries_digest")
	// ErrBodyDigestMissing: the prepared note carries no well-formed digest.
	ErrBodyDigestMissing = errors.New("release_body_digest_missing")
	// ErrBodyDigestAmbiguous: the prepared note carries more than one digest.
	ErrBodyDigestAmbiguous = errors.New("release_body_digest_ambiguous")
	// ErrCommitUnknown: --commit names no commit in the local repository.
	ErrCommitUnknown = errors.New("commit_unknown")
	// ErrCommitUnknownRemote: the remote does not have the commit at all
	// (the lead pushes it), distinct from a commit the branch lacks (the lead
	// merges it).
	ErrCommitUnknownRemote = errors.New("commit_unknown_remote")
	// ErrCommitNotOnBranch: the remote branch does not contain the commit.
	ErrCommitNotOnBranch = errors.New("commit_not_on_branch")
)

// bodyDigestLabel joins the release body's digest to the gate note (the
// spec's one note field): the owner's gate question shows the note and the
// digest of the body the release will carry.
const bodyDigestLabel = "release body sha256:"

// BodyDigest is the hex SHA-256 of a release body.
func BodyDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// recordedBodyDigest reads the one body digest Spec joined to the note: zero
// (or a malformed one) is release_body_digest_missing, two or more is
// release_body_digest_ambiguous.
func recordedBodyDigest(summary string) (string, error) {
	switch n := strings.Count(summary, bodyDigestLabel); {
	case n == 0:
		return "", fmt.Errorf("%w: the prepared note %q carries no %q", ErrBodyDigestMissing, summary, bodyDigestLabel)
	case n > 1:
		return "", fmt.Errorf("%w: the prepared note carries %d digests", ErrBodyDigestAmbiguous, n)
	}
	d := summary[strings.Index(summary, bodyDigestLabel)+len(bodyDigestLabel):]
	if len(d) != 64 || strings.Trim(d, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%w: the prepared note's digest %q is not 64 lowercase hex at the end", ErrBodyDigestMissing, d)
	}
	return d, nil
}

var (
	tagRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	shaRe    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
)

// Spec builds the compound release spec for a tag at a commit. notes is the
// gate note, shown to the owner in the gate question only; body is the
// release body (the notes file's content), recorded in the spec by its
// digest, joined to the note.
func Spec(scope compoundrelease.Scope, lead, owner, repository, tag, commit, notes, body string) (operatorauth.ReleaseSpec, error) {
	if !tagRe.MatchString(tag) || !shaRe.MatchString(commit) || !repoRe.MatchString(repository) {
		return operatorauth.ReleaseSpec{}, errors.New("release needs a tag, a full lowercase commit SHA and owner/name")
	}
	if strings.TrimSpace(body) == "" {
		return operatorauth.ReleaseSpec{}, errors.New("release needs a release body: --notes-file names the release notes document")
	}
	if strings.Contains(notes, bodyDigestLabel) {
		return operatorauth.ReleaseSpec{}, fmt.Errorf("%w: --notes contains %q; prepare writes the digest line", ErrNoteCarriesDigest, bodyDigestLabel)
	}
	notes = strings.TrimSpace(strings.TrimSpace(notes) + " " + bodyDigestLabel + BodyDigest(body))
	profile := squadnamespace.NormalizeProfile(scope.Profile)
	return operatorauth.ReleaseSpec{
		SchemaVersion: operatorauth.ReleaseSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Namespace: operatorauth.NamespaceBinding{ProjectDir: scope.ProjectDir, Profile: profile, Session: scope.Session,
			NamespaceID: squadnamespace.ID(profile, scope.Session), Generation: scope.NamespaceGeneration},
		ParentGate: scope.ParentGate, RequesterHandle: lead, OperatorHandle: owner,
		TagTarget: gatetarget.ReleaseTag(repository, tag, commit), GitHubReleaseTarget: gatetarget.ReleaseCreate(repository, tag),
		Note: operatorauth.ReleaseNote{Summary: notes},
	}, nil
}

// GateID is the deterministic gate of one child: a retry raises or finds the
// same gate, never a second question.
func GateID(child operatorauth.ReleaseChildPlan) string {
	sum := sha256.Sum256([]byte(child.ReleaseChild.AttemptID))
	role := "tag"
	if child.Role == operatorauth.ReleaseChildGitHubRelease {
		role = "gh"
	}
	return "rel-" + role + "-" + hex.EncodeToString(sum[:10])
}

// ChildGate is the compoundrelease reconcile seam: raise the child's typed
// gate, or find the one a crash left, and refuse a gate that binds another
// action or target.
func (f *Flow) ChildGate(child operatorauth.ReleaseChildPlan) (string, error) {
	id := GateID(child)
	check := func(rec gatestore.Record) (string, error) {
		if rec.Class != child.Action || rec.Target != child.Target || !mergetrain.SameSquad(rec, f.Squad) {
			return id, fmt.Errorf("%w: gate %s binds %s %q for squad %q", compoundrelease.ErrChildConflict, id, rec.Class, rec.Target, rec.Squad)
		}
		return id, nil
	}
	if rec, err := f.Gates.Load(id); err == nil {
		return check(rec)
	}
	rec, err := f.Raise(gatestore.Spec{GateID: id, Action: child.Action, Target: child.Target, RaisedBy: f.Lead, Squad: f.Squad})
	if errors.Is(err, fs.ErrExist) {
		if rec, err = f.Gates.Load(id); err != nil {
			return id, err
		}
	} else if err != nil {
		return id, err
	}
	return check(rec)
}

// CheckCommit proves the tag's commit names an object before any gate is
// raised (3.0.2): it resolves to a commit in the local repository (git
// cat-file -e), and the named remote branch contains it, resolved on the
// remote as TagCommit resolves the tag. A shape-valid SHA naming nothing is
// commit_unknown; a commit the remote does not have is commit_unknown_remote
// (push it); a commit the branch does not carry is commit_not_on_branch
// (merge it).
func (f *Flow) CheckCommit(ctx context.Context, repository, commit string) error {
	if !shaRe.MatchString(commit) {
		return fmt.Errorf("%w: %q is not a full lowercase commit SHA", ErrCommitUnknown, commit)
	}
	if !branchRe.MatchString(f.Branch) || strings.Contains(f.Branch, "..") {
		return fmt.Errorf("%w: no valid branch is named (%q)", ErrCommitNotOnBranch, f.Branch)
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", f.RepoDir, "cat-file", "-e", commit+"^{commit}").CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s names no commit in the repository at %s: %v %s", ErrCommitUnknown, commit, f.RepoDir, err, strings.TrimSpace(string(out)))
	}
	if ok, err := f.Exec.CommitExists(ctx, repository, commit); err != nil {
		return fmt.Errorf("resolve %s on %s: %w", commit, repository, err)
	} else if !ok {
		return fmt.Errorf("%w: %s has no commit %s; push it first", ErrCommitUnknownRemote, repository, commit)
	}
	ok, err := f.Exec.BranchContains(ctx, repository, f.Branch, commit)
	if err != nil {
		return fmt.Errorf("resolve %s on branch %s of %s: %w", commit, f.Branch, repository, err)
	}
	if !ok {
		return fmt.Errorf("%w: branch %s on %s does not contain %s; merge it first", ErrCommitNotOnBranch, f.Branch, repository, commit)
	}
	return nil
}

// Prepare writes the release (or finds it, for the same spec) and raises both
// child gates; the result is the active manifest naming the two gates. The
// tag's commit is checked first (CheckCommit): nothing is written and no gate
// is raised for a commit that names nothing or that the branch lacks.
func (f *Flow) Prepare(spec operatorauth.ReleaseSpec) (compoundrelease.Snapshot, error) {
	if fl := strings.Fields(spec.TagTarget); len(fl) != 6 || fl[0] != "git" || fl[1] != "tag" || fl[4] != "on" {
		return compoundrelease.Snapshot{}, fmt.Errorf("release tag target %q is not the flow's", spec.TagTarget)
	} else if err := f.CheckCommit(context.Background(), fl[5], fl[3]); err != nil {
		return compoundrelease.Snapshot{}, err
	}
	snap, err := f.Release.Create(spec)
	if err != nil {
		return snap, err
	}
	res, err := f.Release.Reconcile(snap.Pointer.GenerationID, f)
	if err != nil {
		return res.Snapshot, err
	}
	if res.Disposition != compoundrelease.ReconcileActivated {
		return res.Snapshot, fmt.Errorf("release is %s", res.Disposition)
	}
	return res.Snapshot, nil
}

// ErrNotApproved: a child has no verified owner answer, so it does not run.
var ErrNotApproved = errors.New("no verified owner answer")

// Step is one child's outcome.
type Step struct {
	Role   string `json:"role"`
	GateID string `json:"gate_id"`
	Target string `json:"target"`
	Status string `json:"status"` // done | ran | refused
	Result string `json:"result,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Publish runs the children of the active release in order: each only with
// its gate's verified answer. A refused child stops the flow before anything
// after it runs (the release never precedes its tag).
func (f *Flow) Publish(ctx context.Context, root string) (steps []Step, err error) {
	snap, err := f.Release.ReadCurrent()
	if err != nil {
		return nil, err
	}
	if snap.Active == nil {
		return nil, fmt.Errorf("release is %s, not active: prepare it first", snap.Pointer.State)
	}
	// The body is the prepared notes file's content, or nothing runs.
	want, err := recordedBodyDigest(snap.Prepared.Spec.Note.Summary)
	if err != nil {
		return nil, err
	}
	if BodyDigest(f.Body) != want {
		return nil, fmt.Errorf("%w: the release body's sha256 is %s, the release was prepared with %s", ErrBodyMismatch, BodyDigest(f.Body), want)
	}
	dir := filepath.Join(root, "extensions", "amq-squad", "releases", snap.Prepared.ReleaseID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// One publication at a time per release: the retry after a crash
	// reconciles against the remote under the same lock (P2-10).
	err = flock.WithLock(filepath.Join(dir, "publish.lock"), func() error {
		steps, err = f.publishLocked(ctx, root, dir, snap)
		return err
	})
	return steps, err
}

// ErrTagMoved: the remote tag no longer names the approved commit.
var ErrTagMoved = errors.New("tag_moved")

func (f *Flow) publishLocked(ctx context.Context, root, dir string, snap compoundrelease.Snapshot) ([]Step, error) {
	var steps []Step
	approved := ""
	// Before anything runs: a tag rule restricts update and deletion of the
	// tag (fix 2, P1-6a), so the approved tag cannot be moved under it.
	if repo, tag, _, err := parseTarget(snap.Prepared.Children[0]); err != nil {
		return nil, err
	} else if ok, why, err := f.Exec.TagProtected(ctx, repo, tag); err != nil {
		return nil, fmt.Errorf("read tag protection of %s: %w", repo, err)
	} else if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProtectionUnmet, why)
	}
	for i, child := range snap.Prepared.Children {
		id := snap.Active.Children[i].QuestionMessageID
		st := Step{Role: child.Role, GateID: id, Target: child.Target}
		rec, err := f.Gates.Load(id)
		if err != nil || rec.Class != child.Action || rec.Target != child.Target || !mergetrain.SameSquad(rec, f.Squad) {
			return append(steps, refused(st, fmt.Sprintf("gate %s does not bind this child: %v", id, err))), ErrNotApproved
		}
		repo, tag, commit, err := parseTarget(child)
		if err != nil {
			return append(steps, refused(st, err.Error())), err
		}
		if commit != "" {
			approved = commit
		}
		marker := filepath.Join(dir, child.Role+".json")
		if done, err := os.ReadFile(marker); err == nil {
			var prev Step
			_ = json.Unmarshal(done, &prev)
			st.Status, st.Result = "done", prev.Result
			steps = append(steps, st)
			continue
		}
		if err := f.Accepted(rec); err != nil {
			return append(steps, refused(st, err.Error())), fmt.Errorf("%s: %w (gate %s): %v", child.Role, ErrNotApproved, id, err)
		}
		// The remote tag is resolved before each step: a tag created by a
		// crashed run at the approved commit is that run's (reconciled); a tag
		// at any other commit is tag_moved and raises the owner alert.
		remote, err := f.Exec.TagCommit(ctx, repo, tag)
		if err != nil {
			return append(steps, refused(st, err.Error())), fmt.Errorf("%s: resolve tag %s: %w", child.Role, tag, err)
		}
		if remote != "" && remote != approved {
			aerr := f.tagMoved(root, repo, tag, approved, remote)
			return append(steps, refused(st, fmt.Sprintf("tag %s names %s, approved %s", tag, remote, approved))), errors.Join(fmt.Errorf("%s: %w: tag %s names %s, the owner approved %s", child.Role, ErrTagMoved, tag, remote, approved), aerr)
		}
		switch child.Role {
		case operatorauth.ReleaseChildTag:
			if remote == "" {
				err = f.Exec.Tag(ctx, repo, tag, commit)
			} else {
				st.Detail = "the tag already named the approved commit (a crashed run's); reconciled"
			}
			st.Result = commit
		default:
			if remote == "" {
				err = fmt.Errorf("tag %s does not exist on %s", tag, repo)
				break
			}
			st.Result, err = f.release(ctx, root, dir, repo, tag, approved, f.Body)
			if errors.Is(err, ErrTagMoved) || errors.Is(err, ErrDraftEdited) {
				return append(steps, refused(st, err.Error())), fmt.Errorf("%s: %w", child.Role, err)
			}
		}
		if err != nil {
			return append(steps, refused(st, err.Error())), fmt.Errorf("%s: %w", child.Role, err)
		}
		st.Status = "ran"
		if err := writeOnce(marker, st); err != nil {
			return append(steps, st), err
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// release publishes the release in two steps (fix 4, items 8 to 10). The
// draft is created naming the tag and the approved commit, and its id goes
// into the publication lock record (draft.json) at once, so a run that dies
// leaves it to recovery, not an orphan. It is read back and compared (id,
// tag_name, target_commitish); the tag is resolved once and compared to the
// approved commit; immediately before the publish request the draft is read
// again and the tag resolved again, and both must be unchanged. Any mismatch
// deletes the draft, audits the draft id, outcome and deletion result, and
// raises the owner alert (tag_moved or draft_edited; a failed deletion is a
// draft_orphan alert naming the draft). A recorded draft from an earlier run
// is completed when it still matches, else deleted.
func (f *Flow) release(ctx context.Context, root, dir, repo, tag, approved, notes string) (string, error) {
	audit := mergetrain.Audit{Path: filepath.Join(dir, "audit.jsonl")}
	note := func(kind string, d Draft, detail string) error {
		return audit.Append(mergetrain.Entry{Kind: kind, Repository: repo, PR: int(d.ID), Head: approved, Detail: fmt.Sprintf("draft %d (%s at %s): %s", d.ID, d.Tag, d.TargetCommitish, detail)})
	}
	record := filepath.Join(dir, "draft.json")
	want := Draft{Tag: tag, TargetCommitish: approved, Draft: true}
	same := func(a, b Draft) bool {
		return a.ID == b.ID && a.Tag == b.Tag && a.TargetCommitish == b.TargetCommitish && a.Draft && b.Draft
	}
	intentPath := filepath.Join(dir, "draft-intent.json")
	discard := func(d Draft, cause error, why, remote string) (string, error) {
		if !d.Draft {
			// A published release is never deleted (5 re-pass, P1-1): it is
			// reported for the owner.
			alertErr := f.alert(root, repo, "draft_edited", tag, approved, fmt.Sprintf("published release %d for %s on %s does not match the approved one (%s); it was not deleted", d.ID, tag, repo, why))
			return "", errors.Join(fmt.Errorf("%w: published release %d: %s", ErrDraftEdited, d.ID, why), note("release_mismatch", d, why+"; not deleted"), alertErr)
		}
		derr := f.Exec.DeleteRelease(ctx, repo, d.ID)
		result := "deleted"
		var orphanErr, alertErr error
		if derr != nil {
			result = "deletion failed: " + derr.Error()
			orphanErr = f.alert(root, repo, "draft_orphan", tag, approved, fmt.Sprintf("draft release %d for %s on %s could not be deleted after %s: %v", d.ID, tag, repo, why, derr))
		} else {
			_ = os.Remove(record)
		}
		aerr := note("draft_"+strings.TrimPrefix(cause.Error(), "draft_"), d, why+"; "+result)
		if errors.Is(cause, ErrTagMoved) {
			alertErr = f.tagMoved(root, repo, tag, approved, remote)
		} else {
			alertErr = f.alert(root, repo, "draft_edited", tag, approved, fmt.Sprintf("draft release %d for %s on %s was edited before publication: %s", d.ID, tag, repo, why))
		}
		return "", errors.Join(fmt.Errorf("%w: draft %d: %s", cause, d.ID, why), derr, aerr, orphanErr, alertErr)
	}

	var d Draft
	if raw, err := os.ReadFile(record); err == nil { // recovery of an earlier run's draft
		var prev Draft
		if json.Unmarshal(raw, &prev) != nil || prev.ID == 0 {
			return "", fmt.Errorf("publication lock record %s is unreadable", record)
		}
		got, found, err := f.Exec.ReadDraft(ctx, repo, prev.ID)
		switch {
		case err != nil:
			return "", fmt.Errorf("read back recorded draft %d: %w", prev.ID, err)
		case !found:
			_ = os.Remove(record)
			_ = note("draft_gone", prev, "the recorded draft no longer exists; a new draft is created")
		case !got.Draft && got.ID == prev.ID && got.Tag == want.Tag && got.TargetCommitish == want.TargetCommitish:
			// The publish request succeeded and its response was lost (5
			// re-pass, P1-1): GitHub keeps the published release at its id.
			// The same id, tag and commit is completion, never a deletion.
			_ = os.Remove(record)
			_ = os.Remove(intentPath)
			return got.URL, note("draft_published", got, "recovered: the release was already published at its id ("+got.URL+")")
		case !same(got, prev) || got.Tag != want.Tag || got.TargetCommitish != want.TargetCommitish:
			return discard(got, ErrDraftEdited, fmt.Sprintf("the recorded draft now names %s at %s", got.Tag, got.TargetCommitish), "")
		default:
			d = got
			_ = note("draft_recovered", d, "completing the recorded draft")
		}
	}
	if d.ID == 0 {
		// The creation intent first (5 re-pass, P2-4): a draft whose POST
		// succeeded but whose response or record was lost carries the nonce
		// in its body, and is adopted here, never created twice.
		in, err := readIntent(intentPath, tag, approved)
		if err != nil {
			return "", err
		}
		matches, err := f.Exec.FindDrafts(ctx, repo, in.Nonce)
		if err != nil {
			return "", fmt.Errorf("find drafts of intent %s: %w", in.Nonce, err)
		}
		var created Draft
		for _, c := range matches {
			switch {
			case created.ID == 0 && c.Draft && c.Tag == tag && c.TargetCommitish == approved:
				created = c
			case c.Draft:
				// another draft of the same intent: a duplicate, or edited
				derr := f.Exec.DeleteRelease(ctx, repo, c.ID)
				_ = note("draft_duplicate", c, fmt.Sprintf("a further draft of intent %s; deleted: %v", in.Nonce, derr))
			}
		}
		if created.ID != 0 {
			_ = note("draft_adopted", created, "the draft of intent "+in.Nonce+" was created by a run whose response or record was lost")
		} else if created, err = f.Exec.CreateDraft(ctx, repo, tag, approved, strings.TrimSpace(notes+"\n\n"+NonceMarker(in.Nonce))); err != nil {
			return "", err
		}
		if err := writeOnce(record, created); err != nil {
			return "", fmt.Errorf("record draft %d: %w", created.ID, err)
		}
		if err := note("draft_created", created, "created"); err != nil {
			return "", err
		}
		got, found, err := f.Exec.ReadDraft(ctx, repo, created.ID)
		if err != nil {
			return "", fmt.Errorf("read back draft %d: %w", created.ID, err) // recorded: the next run recovers it
		}
		want.ID = created.ID
		if !found {
			got = created // gone already: the deletion records the created id
		}
		if !found || !same(got, want) || !same(created, want) {
			return discard(got, ErrDraftEdited, fmt.Sprintf("read back as %s at %s (found %v)", got.Tag, got.TargetCommitish, found), "")
		}
		d = got
	}
	commit, err := f.Exec.TagCommit(ctx, repo, tag)
	if err != nil {
		return "", fmt.Errorf("resolve tag %s: %w", tag, err)
	}
	if commit != approved {
		return discard(d, ErrTagMoved, fmt.Sprintf("tag %s names %s", tag, commit), commit)
	}
	// immediately before the publish request: the draft and the tag again
	again, found, err := f.Exec.ReadDraft(ctx, repo, d.ID)
	if err != nil {
		return "", fmt.Errorf("re-read draft %d: %w", d.ID, err)
	}
	if !found {
		again = d
	}
	if !found || !same(again, d) {
		return discard(again, ErrDraftEdited, fmt.Sprintf("before publication it names %s at %s (found %v)", again.Tag, again.TargetCommitish, found), "")
	}
	if commit, err = f.Exec.TagCommit(ctx, repo, tag); err != nil {
		return "", fmt.Errorf("resolve tag %s: %w", tag, err)
	} else if commit != approved {
		return discard(d, ErrTagMoved, fmt.Sprintf("tag %s names %s before publication", tag, commit), commit)
	}
	url, err := f.Exec.PublishDraft(ctx, repo, d.ID)
	if err != nil {
		return "", err
	}
	_ = os.Remove(record)
	_ = os.Remove(intentPath)
	return url, note("draft_published", d, "published "+url)
}

// readIntent reads the creation intent, or writes a new one with a fresh
// nonce; an intent for another tag or commit is refused.
func readIntent(path, tag, target string) (intent, error) {
	var in intent
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if json.Unmarshal(raw, &in) != nil || in.Nonce == "" {
			return in, fmt.Errorf("creation intent %s is unreadable", path)
		}
		if in.Tag != tag || in.Target != target {
			return in, fmt.Errorf("creation intent %s is for %s at %s, not %s at %s", path, in.Tag, in.Target, tag, target)
		}
		return in, nil
	case !errors.Is(err, fs.ErrNotExist):
		return in, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return in, err
	}
	in = intent{Tag: tag, Target: target, Nonce: hex.EncodeToString(b[:])}
	return in, writeOnce(path, in)
}

// ErrAlertUnwritten: an owner alert could not be written; the mismatch it
// reports never passes silently (narrow pass, 3-2 P1-1).
var ErrAlertUnwritten = errors.New("owner_alert_unwritten")

// alert raises one owner alert of a kind; a failed write is an error.
func (f *Flow) alert(root, repo, kind, tag, approved, detail string) error {
	now := time.Now()
	if f.Now != nil {
		now = f.Now()
	}
	sum := sha256.Sum256([]byte(repo + "\x00" + kind + "\x00" + tag + "\x00" + approved + "\x00" + detail))
	if err := mergetrain.WriteAlert(root, mergetrain.Alert{ID: strings.ReplaceAll(kind, "_", "-") + "-" + hex.EncodeToString(sum[:6]), Repository: repo, Kind: kind, Name: tag,
		At: now.UTC().Format(time.RFC3339), Detail: detail}); err != nil {
		return fmt.Errorf("%w: %s alert: %v", ErrAlertUnwritten, kind, err)
	}
	return nil
}

// tagMoved raises the owner alert for a tag that left its approved commit;
// a failed write is an error.
func (f *Flow) tagMoved(root, repo, tag, approved, remote string) error {
	now := time.Now()
	if f.Now != nil {
		now = f.Now()
	}
	sum := sha256.Sum256([]byte(repo + "\x00" + tag + "\x00" + approved + "\x00" + remote))
	if err := mergetrain.WriteAlert(root, mergetrain.Alert{ID: "tag-moved-" + hex.EncodeToString(sum[:6]), Repository: repo, Kind: "tag_moved", Name: tag, SHA: remote,
		At: now.UTC().Format(time.RFC3339), Detail: fmt.Sprintf("tag %s on %s moved from the approved %s to %s before the release", tag, repo, approved, remote)}); err != nil {
		return fmt.Errorf("%w: tag_moved alert: %v", ErrAlertUnwritten, err)
	}
	return nil
}

func refused(st Step, detail string) Step {
	st.Status, st.Detail = "refused", detail
	return st
}

// parseTarget reads the child's own target, so what runs is exactly what the
// owner approved.
func parseTarget(child operatorauth.ReleaseChildPlan) (repo, tag, commit string, err error) {
	f := strings.Fields(child.Target)
	switch {
	case child.Role == operatorauth.ReleaseChildTag && len(f) == 6 && f[0] == "git" && f[1] == "tag" && f[4] == "on":
		return f[5], f[2], f[3], nil
	case child.Role == operatorauth.ReleaseChildGitHubRelease && len(f) == 6 && f[0] == "gh" && f[3] != "" && f[4] == "on":
		return f[5], f[3], "", nil
	}
	return "", "", "", fmt.Errorf("release child target %q is not the flow's", child.Target)
}

func writeOnce(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
