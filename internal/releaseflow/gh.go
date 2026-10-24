package releaseflow

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GH is the Executor over the owner's gh: the tag is a ref the API creates at
// the approved commit (no local checkout, no push from a seat's tree), the
// release names that existing tag (--verify-tag).
type GH struct{ Bin string }

func (g *GH) run(ctx context.Context, args ...string) (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args[:min(3, len(args))], " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *GH) Tag(ctx context.Context, repository, tag, commit string) error {
	_, err := g.run(ctx, "api", "-X", "POST", "repos/"+repository+"/git/refs", "-f", "ref=refs/tags/"+tag, "-f", "sha="+commit)
	return err
}

func (g *GH) Release(ctx context.Context, repository, tag, notes string) (string, error) {
	return g.run(ctx, "release", "create", tag, "-R", repository, "--verify-tag", "--title", tag, "--notes", notes)
}

// TagCommit resolves a remote tag to its commit, through an annotated tag
// object when there is one; "" when the tag does not exist.
func (g *GH) TagCommit(ctx context.Context, repository, tag string) (string, error) {
	out, err := g.run(ctx, "api", "repos/"+repository+"/git/ref/tags/"+tag, "--jq", `.object.type + " " + .object.sha`)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", nil
		}
		return "", err
	}
	kind, sha, _ := strings.Cut(out, " ")
	if kind == "tag" {
		return g.run(ctx, "api", "repos/"+repository+"/git/tags/"+sha, "--jq", ".object.sha")
	}
	return sha, nil
}
