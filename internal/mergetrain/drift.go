package mergetrain

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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

// Tag is one repository tag.
type Tag struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

// Release is one repository release: its id and its tag. A release deleted
// and created again under the same tag has a new id (P2-12).
type Release struct {
	ID  int64  `json:"id"`
	Tag string `json:"tag"`
}

// ReleaseLister reads a repository's tags and releases (GH does).
type ReleaseLister interface {
	Tags(ctx context.Context, repository string) ([]Tag, error)
	Releases(ctx context.Context, repository string) ([]Release, error)
}

// Alert is an owner alert: a tag or release on the repository that no
// accepted gate covers. A seat that reads the owner's gh token (same UID,
// ruling 12) can create one; the CI-only ruleset does not stop it, so the
// core detects it after the fact at every train and doctor run.
type Alert struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Kind       string `json:"kind"` // tag | release | tag_moved
	Name       string `json:"name"`
	SHA        string `json:"sha,omitempty"`
	At         string `json:"at"`
	Detail     string `json:"detail"`
	// GateID names the open gate an alert is about (5-amq-B: a hung
	// verification); the status contract shows it only while that gate is
	// open.
	GateID string `json:"gate_id,omitempty"`
}

// AlertsDir is where alerts and each repository's baseline live; the status
// contract reads it into the owner's needs_you attention.
func AlertsDir(root string) string { return filepath.Join(root, "extensions", "amq-squad", "alerts") }

type baseline struct {
	Repository string    `json:"repository"`
	At         string    `json:"at"`
	Tags       []Tag     `json:"tags"`
	Releases   []Release `json:"releases"`
}

// GateVerifier is the owner-signed acceptance of one gate record held in
// the named session's gate store: gateverify.Verifier.Accepted under that
// session's namespace (the owner's BIP-340 signature re-verified, never a
// file's presence). nil verifies nothing, so no gate clears an alert.
type GateVerifier func(session string, store gatestore.Store, rec gatestore.Record) error

// Drift compares the repository's tags and releases against the gate
// stores of every session under the AMQ root (brief 3.1.1-2 item 5: each
// release is cut in its own fresh session, so the squad's own store never
// holds its gates). The first run on a repository records the existing set
// as its baseline (what was there before the squad); after that every tag or
// release outside the baseline needs an accepted gate for it: the release
// flow's targets (gatetarget.ReleaseTag, ReleaseCreate) or, in this squad's
// own session, a seat's gated command (git tag <name>, git push
// refs/tags/<name>, gh release create <name>). Another session's gate clears
// only through a target that names the tag and its commit
// (gatetarget.ReleaseTag), or the release's tag (ReleaseCreate). A gate
// clears only when verify accepts it. Tags compare by name and commit,
// releases by id, and one accepted gate covers one object: the first tag
// commit or release id it is seen covering is bound to it
// (alerts/covered-<gate>.json, covered-<session>--<gate>.json for another
// session's), so a tag moved to another commit or a release deleted and
// recreated under the same tag needs a new gate. Each uncovered one is
// written once as an alert and returned; a gate store that cannot be read
// is an alert of its own (gate_store_unreadable), never a clearance.
// Recorded limit: what exists at the first run is the baseline, so the
// owner's first run is the baseline review.
func Drift(ctx context.Context, l ReleaseLister, verify GateVerifier, root, repository string, now time.Time) ([]Alert, error) {
	tags, err := l.Tags(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	releases, err := l.Releases(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	dir := AlertsDir(root)
	bpath := filepath.Join(dir, "baseline-"+strings.ReplaceAll(repository, "/", "--")+".json")
	raw, err := os.ReadFile(bpath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, writeOnceJSON(bpath, baseline{Repository: repository, At: now.UTC().Format(time.RFC3339), Tags: tags, Releases: releases})
	}
	if err != nil {
		return nil, err
	}
	base, err := readBaseline(bpath, raw, releases)
	if err != nil {
		return nil, err
	}
	var alerts []Alert
	alert := func(a Alert) error {
		if err := writeOnceJSON(filepath.Join(dir, a.ID+".json"), a); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		alerts = append(alerts, a)
		return nil
	}
	accepted, unreadable := sessionTargets(root, verify)
	for _, u := range unreadable {
		sum := sha256.Sum256([]byte(u.path))
		if err := alert(Alert{ID: "gate-store-unreadable-" + hex.EncodeToString(sum[:6]), Repository: repository, Kind: "gate_store_unreadable", Name: u.path,
			At: now.UTC().Format(time.RFC3339), Detail: fmt.Sprintf("the gate store %s could not be read (%v); no gate in it clears an alert", u.path, u.err)}); err != nil {
			return alerts, err
		}
	}
	raise := func(kind, name, sha, object string, own []string, other ...string) error {
		for _, t := range append(own, other...) {
			for _, g := range accepted[t] {
				if (g.own || slices.Contains(other, t)) && bindCovered(dir, g.bindKey, object) {
					return nil
				}
			}
		}
		sum := sha256.Sum256([]byte(repository + "\x00" + kind + "\x00" + name + "\x00" + sha))
		return alert(Alert{ID: "ungated-" + kind + "-" + hex.EncodeToString(sum[:6]), Repository: repository, Kind: kind, Name: name, SHA: sha,
			At: now.UTC().Format(time.RFC3339), Detail: fmt.Sprintf("%s %s on %s has no accepted owner gate", kind, name, repository)})
	}
	for _, t := range tags {
		if slices.Contains(base.Tags, t) {
			continue
		}
		own := []string{gatetarget.Command("git tag", t.Name), gatetarget.TagPush("refs/tags/" + t.Name)}
		if err := raise("tag", t.Name, t.SHA, "tag "+t.Name+"@"+t.SHA, own, gatetarget.ReleaseTag(repository, t.Name, t.SHA)); err != nil {
			return alerts, err
		}
	}
	for _, r := range releases {
		if slices.Contains(base.Releases, r) {
			continue
		}
		own := []string{gatetarget.Command("gh release create", r.Tag)}
		if err := raise("release", r.Tag, "", "release "+strconv.FormatInt(r.ID, 10), own, gatetarget.ReleaseCreate(repository, r.Tag)); err != nil {
			return alerts, err
		}
	}
	return alerts, nil
}

// acceptedGate is a verified release gate: the key its coverage binds under
// and whether it is this squad's own session's.
type acceptedGate struct {
	bindKey string
	own     bool
}

type unreadableStore struct {
	path string
	err  error
}

// sessionTargets maps the targets of every verified tag and release gate in
// the gate stores of the sessions under the AMQ root (root's parent) to the
// gates that accept them. The sessions are the root's parent's directory
// entries, read with os.ReadDir and taken literally (no glob, no symlink
// followed); one with no gate store is skipped. A record counts only when
// it was raised in the session whose store holds it (its squad identity)
// and verify accepts it. A store whose gate list cannot be read is returned
// as unreadable.
func sessionTargets(root string, verify GateVerifier) (map[string][]acceptedGate, []unreadableStore) {
	out := map[string][]acceptedGate{}
	var bad []unreadableStore
	parent := filepath.Dir(filepath.Clean(root))
	entries, err := os.ReadDir(parent)
	if err != nil {
		return out, []unreadableStore{{path: parent, err: err}}
	}
	ownName := filepath.Base(filepath.Clean(root))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionRoot := filepath.Join(parent, e.Name())
		store, err := gatestore.Open(sessionRoot)
		if err != nil {
			bad = append(bad, unreadableStore{path: sessionRoot, err: err})
			continue
		}
		if _, err := os.Lstat(store.Dir("gates")); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		ids, err := store.IDs()
		if err != nil {
			bad = append(bad, unreadableStore{path: store.Dir("gates"), err: err})
			continue
		}
		own := e.Name() == ownName
		for _, id := range ids {
			rec, err := store.Load(id)
			if err != nil || gatetarget.Class(rec.Class) != gatetarget.ClassRelease || !SameSquad(rec, store.Squad()) {
				continue
			}
			if verify == nil || verify(e.Name(), store, rec) != nil {
				continue
			}
			key := id
			if !own {
				key = e.Name() + "--" + id
			}
			out[rec.Target] = append(out[rec.Target], acceptedGate{bindKey: key, own: own})
		}
	}
	return out, bad
}

// bindCovered binds an accepted gate to the first object it covers and
// reports whether object is that object.
func bindCovered(dir, gateID, object string) bool {
	path := filepath.Join(dir, "covered-"+gateID+".json")
	if err := writeOnceJSON(path, map[string]string{"gate": gateID, "object": object}); err == nil {
		return true
	}
	raw, err := os.ReadFile(path)
	var b map[string]string
	return err == nil && json.Unmarshal(raw, &b) == nil && b["object"] == object
}

// WriteAlert writes one owner alert once (the release flow's tag_moved too).
func WriteAlert(root string, a Alert) error {
	err := writeOnceJSON(filepath.Join(AlertsDir(root), a.ID+".json"), a)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}

// ReadAlerts lists the session's alerts (the status contract reads them).
func ReadAlerts(root string) []Alert {
	paths, _ := filepath.Glob(filepath.Join(AlertsDir(root), "*.json"))
	var out []Alert
	for _, p := range paths {
		if b := filepath.Base(p); strings.HasPrefix(b, "baseline-") || strings.HasPrefix(b, "covered-") {
			continue
		}
		var a Alert
		if raw, err := os.ReadFile(p); err == nil && json.Unmarshal(raw, &a) == nil {
			out = append(out, a)
		}
	}
	return out
}

func writeOnceJSON(path string, v any) error {
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

// ErrBaselineInvalid: a repository's drift baseline does not decode; the run
// stops (drift_baseline_invalid), it never continues without the baseline.
var ErrBaselineInvalid = errors.New("drift_baseline_invalid")

// readBaseline decodes a baseline. The first 3-2 format kept releases as tag
// names; it is read once and rewritten in the object form, each old tag name
// taking the ids its releases have now (fix 2, N3).
func readBaseline(path string, raw []byte, current []Release) (baseline, error) {
	var base baseline
	if err := json.Unmarshal(raw, &base); err == nil {
		return base, nil
	}
	var old struct {
		Repository string   `json:"repository"`
		At         string   `json:"at"`
		Tags       []Tag    `json:"tags"`
		Releases   []string `json:"releases"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		return base, fmt.Errorf("%w: %s: %v", ErrBaselineInvalid, path, err)
	}
	base = baseline{Repository: old.Repository, At: old.At, Tags: old.Tags, Releases: []Release{}}
	for _, r := range current {
		if slices.Contains(old.Releases, r.Tag) {
			base.Releases = append(base.Releases, r)
		}
	}
	b, _ := json.Marshal(base)
	tmp := path + ".migrate"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return base, err
	}
	return base, os.Rename(tmp, path)
}
