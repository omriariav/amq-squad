package releaseflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// GH is the Executor over the owner's gh: the tag is a ref the API creates at
// the approved commit (no local checkout, no push from a seat's tree); the
// release is a draft read back and compared before it is published.
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

func (g *GH) CreateDraft(ctx context.Context, repository, tag, notes string) (int64, error) {
	out, err := g.run(ctx, "api", "-X", "POST", "repos/"+repository+"/releases", "-f", "tag_name="+tag, "-f", "name="+tag, "-f", "body="+notes, "-F", "draft=true", "--jq", ".id")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(out, 10, 64)
}

func (g *GH) DraftTag(ctx context.Context, repository string, id int64) (string, error) {
	return g.run(ctx, "api", "repos/"+repository+"/releases/"+strconv.FormatInt(id, 10), "--jq", ".tag_name")
}

func (g *GH) PublishDraft(ctx context.Context, repository string, id int64) (string, error) {
	return g.run(ctx, "api", "-X", "PATCH", "repos/"+repository+"/releases/"+strconv.FormatInt(id, 10), "-F", "draft=false", "--jq", ".html_url")
}

func (g *GH) DeleteRelease(ctx context.Context, repository string, id int64) error {
	_, err := g.run(ctx, "api", "-X", "DELETE", "repos/"+repository+"/releases/"+strconv.FormatInt(id, 10))
	return err
}

// TagProtected reads the repository's active tag rulesets: one whose
// conditions cover refs/tags/<tag> and that carries both an update and a
// deletion rule protects the tag. Bypass by the owner is allowed (the owner
// is the one identity, plan 7.3).
func (g *GH) TagProtected(ctx context.Context, repository, tag string) (bool, string, error) {
	out, err := g.run(ctx, "api", "repos/"+repository+"/rulesets?targets=tag&per_page=100", "--jq", `.[] | select(.enforcement == "active") | .id`)
	if err != nil {
		return false, "", err
	}
	for _, id := range strings.Fields(out) {
		raw, err := g.run(ctx, "api", "repos/"+repository+"/rulesets/"+id)
		if err != nil {
			return false, "", err
		}
		var rs struct {
			Conditions struct {
				RefName struct {
					Include []string `json:"include"`
					Exclude []string `json:"exclude"`
				} `json:"ref_name"`
			} `json:"conditions"`
			Rules []struct {
				Type string `json:"type"`
			} `json:"rules"`
		}
		if err := json.Unmarshal([]byte(raw), &rs); err != nil {
			return false, "", fmt.Errorf("decode tag ruleset %s: %w", id, err)
		}
		if TagRuleCovers(rs.Conditions.RefName.Include, rs.Conditions.RefName.Exclude, tag) && hasRules(rs.Rules, "update", "deletion") {
			return true, "", nil
		}
	}
	return false, "no active tag ruleset restricts update and deletion of refs/tags/" + tag, nil
}

func hasRules(rules []struct {
	Type string `json:"type"`
}, want ...string) bool {
	for _, w := range want {
		found := false
		for _, r := range rules {
			found = found || r.Type == w
		}
		if !found {
			return false
		}
	}
	return true
}

// TagRuleCovers reports whether a ruleset's ref conditions cover a tag:
// ~ALL or a refs/tags/ pattern that matches it, and no exclude that does.
func TagRuleCovers(include, exclude []string, tag string) bool {
	ref := "refs/tags/" + tag
	match := func(p string) bool {
		if p == "~ALL" {
			return true
		}
		ok, _ := path.Match(p, ref)
		return ok
	}
	in := false
	for _, p := range include {
		in = in || match(p)
	}
	for _, p := range exclude {
		if match(p) {
			return false
		}
	}
	return in
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
