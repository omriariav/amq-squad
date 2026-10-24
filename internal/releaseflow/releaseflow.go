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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Executor performs the two release actions under the owner's identity.
type Executor interface {
	Tag(ctx context.Context, repository, tag, commit string) error
	Release(ctx context.Context, repository, tag, notes string) (string, error)
}

// Flow is one release series: its compoundrelease store and the session's
// gate store.
type Flow struct {
	Release  *compoundrelease.Store
	Gates    gatestore.Store
	Raise    func(gatestore.Spec) (gatestore.Record, error) // 3-1's writer: record, then question
	Accepted func(gatestore.Record) error                   // the full owner check (gateverify.Verifier.Accepted)
	Exec     Executor
	Lead     string
	Now      func() time.Time
}

var (
	tagRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// Spec builds the compound release spec for a tag at a commit.
func Spec(scope compoundrelease.Scope, lead, owner, repository, tag, commit, notes string) (operatorauth.ReleaseSpec, error) {
	if !tagRe.MatchString(tag) || !shaRe.MatchString(commit) || !repoRe.MatchString(repository) {
		return operatorauth.ReleaseSpec{}, errors.New("release needs a tag, a full lowercase commit SHA and owner/name")
	}
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
		if rec.Class != child.Action || rec.Target != child.Target {
			return id, fmt.Errorf("%w: gate %s binds %s %q", compoundrelease.ErrChildConflict, id, rec.Class, rec.Target)
		}
		return id, nil
	}
	if rec, err := f.Gates.Load(id); err == nil {
		return check(rec)
	}
	rec, err := f.Raise(gatestore.Spec{GateID: id, Action: child.Action, Target: child.Target, RaisedBy: f.Lead})
	if errors.Is(err, fs.ErrExist) {
		if rec, err = f.Gates.Load(id); err != nil {
			return id, err
		}
	} else if err != nil {
		return id, err
	}
	return check(rec)
}

// Prepare writes the release (or finds it, for the same spec) and raises both
// child gates; the result is the active manifest naming the two gates.
func (f *Flow) Prepare(spec operatorauth.ReleaseSpec) (compoundrelease.Snapshot, error) {
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
func (f *Flow) Publish(ctx context.Context, root string) ([]Step, error) {
	snap, err := f.Release.ReadCurrent()
	if err != nil {
		return nil, err
	}
	if snap.Active == nil {
		return nil, fmt.Errorf("release is %s, not active: prepare it first", snap.Pointer.State)
	}
	var steps []Step
	for i, child := range snap.Prepared.Children {
		id := snap.Active.Children[i].QuestionMessageID
		st := Step{Role: child.Role, GateID: id, Target: child.Target}
		rec, err := f.Gates.Load(id)
		if err != nil || rec.Class != child.Action || rec.Target != child.Target {
			return append(steps, refused(st, fmt.Sprintf("gate %s does not bind this child: %v", id, err))), ErrNotApproved
		}
		marker := filepath.Join(root, "extensions", "amq-squad", "releases", snap.Prepared.ReleaseID, child.Role+".json")
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
		repo, tag, commit, err := parseTarget(child)
		if err != nil {
			return append(steps, refused(st, err.Error())), err
		}
		switch child.Role {
		case operatorauth.ReleaseChildTag:
			err = f.Exec.Tag(ctx, repo, tag, commit)
			st.Result = commit
		default:
			st.Result, err = f.Exec.Release(ctx, repo, tag, snap.Prepared.Spec.Note.Summary)
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
