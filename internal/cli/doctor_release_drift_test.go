package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

type fakeReleaseLister struct {
	tags     []mergetrain.Tag
	releases []string
}

func (f *fakeReleaseLister) Tags(context.Context, string) ([]mergetrain.Tag, error) {
	return f.tags, nil
}
func (f *fakeReleaseLister) Releases(context.Context, string) ([]string, error) {
	return f.releases, nil
}

// 3-2 ruling 12: doctor compares each granted repository's tags and releases
// with the gate store; one with no accepted owner gate fails and becomes an
// owner alert.
func TestDoctorReleaseDriftFailsOnAnUngatedTag(t *testing.T) {
	root := t.TempDir()
	g := grants.Defaults()
	if checks := doctorReleaseDrift(root, g, &fakeReleaseLister{}, time.Now()); checks != nil {
		t.Fatalf("no merge grant, no check: %+v", checks)
	}
	g.Merge = []grants.Merge{{Repository: "o/r", BaseBranch: "main"}}
	l := &fakeReleaseLister{tags: []mergetrain.Tag{{Name: "v1", SHA: "a1"}}}
	if c := doctorReleaseDrift(root, g, l, time.Now()); len(c) != 1 || c[0].Status != doctorOK {
		t.Fatalf("baseline run: %+v", c)
	}
	l.tags = append(l.tags, mergetrain.Tag{Name: "v2", SHA: "b2"})
	l.releases = []string{"v2"}
	c := doctorReleaseDrift(root, g, l, time.Now())
	if len(c) != 1 || c[0].Status != doctorFail || !strings.Contains(c[0].Detail, "tag v2") || !strings.Contains(c[0].Detail, "release v2") {
		t.Fatalf("ungated tag and release: %+v", c)
	}
	if alerts := mergetrain.ReadAlerts(root); len(alerts) != 2 {
		t.Fatalf("owner alerts %+v", alerts)
	}
}
