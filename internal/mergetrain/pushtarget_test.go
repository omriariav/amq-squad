package mergetrain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Milestone B rulings D1 and D2: merge.push_url and merge.local_push_ack.
// A push_url with a scheme other than SSH, HTTPS github.com, an absolute path
// or file:// is grants_invalid at parse; a real run against a local push
// target is refused (push_target_local) whatever the ack says; a dry run
// proceeds.
func TestLocalPushTargetRefusesARealRun(t *testing.T) {
	grant := func(url string) string {
		return "merge:\n  - repository: owner/fixture\n    base_branch: main\n    push_url: " + url + "\n    local_push_ack: true\n"
	}
	for _, ok := range []string{"git@github.com:owner/fixture.git", "ssh://git@github.com/owner/fixture.git", "https://github.com/owner/fixture.git", "/tmp/fixture.git", "file:///tmp/fixture.git"} {
		if _, err := grants.ParseOwner(grant(ok), "g"); err != nil {
			t.Errorf("push_url %s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://github.com/owner/fixture.git", "git://github.com/owner/fixture.git", `"ext::sh -c touch% /tmp/x"`, "fd::3", "https://gitlab.com/owner/fixture.git", "relative/path.git"} {
		if _, err := grants.ParseOwner(grant(bad), "g"); err == nil {
			t.Errorf("push_url %s accepted", bad)
		}
	}
	f := newFixture(t)
	f.req.PRs = f.req.PRs[:1]
	f.train.Grants.Merge[0].PushURL, f.train.Grants.Merge[0].LocalPushAck = "/tmp/fixture.git", true
	if _, err := f.train.Run(context.Background(), f.req); !errors.Is(err, ErrPushTargetLocal) || strings.Contains(f.calls(), "/merge") {
		t.Fatalf("a real run against a local push target: %v", err)
	}
	f.train.DryRun = true
	if res, err := f.train.Run(context.Background(), f.req); err != nil || len(res.Pairs) != 1 {
		t.Fatalf("the dry run did not proceed: %v %+v", err, res)
	}
}
