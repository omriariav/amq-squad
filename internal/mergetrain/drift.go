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

// ReleaseLister reads a repository's tags and releases (GH does).
type ReleaseLister interface {
	Tags(ctx context.Context, repository string) ([]Tag, error)
	Releases(ctx context.Context, repository string) ([]string, error) // tag names
}

// Alert is an owner alert: a tag or release on the repository that no
// accepted gate covers. A seat that reads the owner's gh token (same UID,
// ruling 12) can create one; the CI-only ruleset does not stop it, so the
// core detects it after the fact at every train and doctor run.
type Alert struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Kind       string `json:"kind"` // tag | release
	Name       string `json:"name"`
	SHA        string `json:"sha,omitempty"`
	At         string `json:"at"`
	Detail     string `json:"detail"`
}

// AlertsDir is where alerts and each repository's baseline live; the status
// contract reads it into the owner's needs_you attention.
func AlertsDir(root string) string { return filepath.Join(root, "extensions", "amq-squad", "alerts") }

type baseline struct {
	Repository string   `json:"repository"`
	At         string   `json:"at"`
	Tags       []Tag    `json:"tags"`
	Releases   []string `json:"releases"`
}

// Drift compares the repository's tags and releases against the session's
// gate store. The first run on a repository records the existing set as its
// baseline (what was there before the squad); after that every tag or
// release outside the baseline needs an accepted gate for it: the release
// flow's targets (gatetarget.ReleaseTag, ReleaseCreate) or a seat's gated
// command (git tag <name>, git push refs/tags/<name>, gh release create
// <name>). Each uncovered one is written once as an alert and returned.
func Drift(ctx context.Context, l ReleaseLister, gates gatestore.Store, root, repository string, now time.Time) ([]Alert, error) {
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
	var base baseline
	if err := json.Unmarshal(raw, &base); err != nil {
		return nil, fmt.Errorf("baseline %s: %w", bpath, err)
	}
	accepted := acceptedTargets(gates)
	var alerts []Alert
	raise := func(kind, name, sha string, targets ...string) error {
		for _, t := range targets {
			if accepted[t] {
				return nil
			}
		}
		sum := sha256.Sum256([]byte(repository + "\x00" + kind + "\x00" + name + "\x00" + sha))
		a := Alert{ID: "ungated-" + kind + "-" + hex.EncodeToString(sum[:6]), Repository: repository, Kind: kind, Name: name, SHA: sha,
			At: now.UTC().Format(time.RFC3339), Detail: fmt.Sprintf("%s %s on %s has no accepted owner gate", kind, name, repository)}
		if err := writeOnceJSON(filepath.Join(dir, a.ID+".json"), a); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		alerts = append(alerts, a)
		return nil
	}
	for _, t := range tags {
		if slices.Contains(base.Tags, t) {
			continue
		}
		if err := raise("tag", t.Name, t.SHA, gatetarget.ReleaseTag(repository, t.Name, t.SHA), gatetarget.Command("git tag", t.Name), gatetarget.TagPush("refs/tags/"+t.Name)); err != nil {
			return alerts, err
		}
	}
	for _, r := range releases {
		if slices.Contains(base.Releases, r) {
			continue
		}
		if err := raise("release", r, "", gatetarget.ReleaseCreate(repository, r), gatetarget.Command("gh release create", r)); err != nil {
			return alerts, err
		}
	}
	return alerts, nil
}

// acceptedTargets are the targets of the store's tag and release gates with
// an accepted decision.
func acceptedTargets(gates gatestore.Store) map[string]bool {
	out := map[string]bool{}
	ids, _ := gates.IDs()
	for _, id := range ids {
		rec, err := gates.Load(id)
		if err == nil && gatetarget.Class(rec.Class) == gatetarget.ClassRelease && gates.Decision(id) == gatestore.StateAccepted {
			out[rec.Target] = true
		}
	}
	return out
}

// ReadAlerts lists the session's alerts (the status contract reads them).
func ReadAlerts(root string) []Alert {
	paths, _ := filepath.Glob(filepath.Join(AlertsDir(root), "ungated-*.json"))
	var out []Alert
	for _, p := range paths {
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
