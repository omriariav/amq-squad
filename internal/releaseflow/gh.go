package releaseflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

const draftJQ = `{id, tag: .tag_name, target_commitish, draft, html_url}`

// FindDrafts lists the releases (every page) whose body carries the nonce.
func (g *GH) FindDrafts(ctx context.Context, repository, nonce string) ([]Draft, error) {
	if strings.Trim(nonce, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("nonce %q is not hex", nonce)
	}
	out, err := g.run(ctx, "api", "--paginate", "repos/"+repository+"/releases?per_page=100",
		"--jq", `.[] | select((.body // "") | contains("`+NonceMarker(nonce)+`")) | `+draftJQ)
	if err != nil {
		return nil, err
	}
	var ds []Draft
	for _, ln := range strings.Split(out, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		d, err := decodeDraft(ln)
		if err != nil {
			return nil, err
		}
		ds = append(ds, d)
	}
	return ds, nil
}

func decodeDraft(out string) (Draft, error) {
	var d Draft
	err := json.Unmarshal([]byte(out), &d)
	return d, err
}

// CreateDraft creates the release as a draft that names the tag and, as its
// target_commitish, the approved commit.
func (g *GH) CreateDraft(ctx context.Context, repository, tag, commit, notes string) (Draft, error) {
	out, err := g.run(ctx, "api", "-X", "POST", "repos/"+repository+"/releases", "-f", "tag_name="+tag, "-f", "target_commitish="+commit,
		"-f", "name="+tag, "-f", "body="+notes, "-F", "draft=true", "--jq", draftJQ)
	if err != nil {
		return Draft{}, err
	}
	return decodeDraft(out)
}

func (g *GH) ReadDraft(ctx context.Context, repository string, id int64) (Draft, bool, error) {
	out, err := g.run(ctx, "api", "repos/"+repository+"/releases/"+strconv.FormatInt(id, 10), "--jq", draftJQ)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return Draft{}, false, nil
		}
		return Draft{}, false, err
	}
	d, err := decodeDraft(out)
	return d, err == nil, err
}

func (g *GH) PublishDraft(ctx context.Context, repository string, id int64) (string, error) {
	return g.run(ctx, "api", "-X", "PATCH", "repos/"+repository+"/releases/"+strconv.FormatInt(id, 10), "-F", "draft=false", "--jq", ".html_url")
}

func (g *GH) DeleteRelease(ctx context.Context, repository string, id int64) error {
	_, err := g.run(ctx, "api", "-X", "DELETE", "repos/"+repository+"/releases/"+strconv.FormatInt(id, 10))
	return err
}

// tagRuleset is what the release flow reads of one tag ruleset.
type tagRuleset struct {
	ID           int               `json:"id"`
	BypassActors []json.RawMessage `json:"bypass_actors"`
	Conditions   struct {
		RefName struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []struct {
		Type string `json:"type"`
	} `json:"rules"`
}

// TagProtected reads the repository's active tag rulesets (fix 4, items 6
// and 7). One protects the tag when its include patterns are of a form the
// flow can match exactly (TagRuleCovers), one of them covers the tag, it has
// no exclusion list, it carries both an update and a deletion rule, and it
// has no bypass actors (the same rule as the branch check). Anything else is
// protection_unmet with the reason.
func (g *GH) TagProtected(ctx context.Context, repository, tag string) (bool, string, error) {
	out, err := g.run(ctx, "api", "repos/"+repository+"/rulesets?targets=tag&per_page=100", "--jq", `.[] | select(.enforcement == "active") | .id`)
	if err != nil {
		return false, "", err
	}
	var reasons []string
	for _, id := range strings.Fields(out) {
		raw, err := g.run(ctx, "api", "repos/"+repository+"/rulesets/"+id)
		if err != nil {
			return false, "", err
		}
		var rs tagRuleset
		if err := json.Unmarshal([]byte(raw), &rs); err != nil {
			return false, "", fmt.Errorf("decode tag ruleset %s: %w", id, err)
		}
		if ok, why := rs.protects(tag); ok {
			return true, "", nil
		} else if why != "" {
			reasons = append(reasons, "ruleset "+id+": "+why)
		}
	}
	if len(reasons) == 0 {
		reasons = []string{"no active tag ruleset covers refs/tags/" + tag}
	}
	return false, strings.Join(reasons, "; "), nil
}

// protects says whether one ruleset protects a tag, and why not when it
// covers or tries to.
func (rs tagRuleset) protects(tag string) (bool, string) {
	covered, why := TagRuleCovers(rs.Conditions.RefName.Include, rs.Conditions.RefName.Exclude, tag)
	switch {
	case why != "":
		return false, why
	case !covered:
		return false, ""
	case len(rs.BypassActors) > 0:
		return false, fmt.Sprintf("%d bypass actors can update or delete refs/tags/%s", len(rs.BypassActors), tag)
	}
	has := map[string]bool{}
	for _, r := range rs.Rules {
		has[r.Type] = true
	}
	if !has["update"] || !has["deletion"] {
		return false, "covers refs/tags/" + tag + " without both an update and a deletion rule"
	}
	return true, ""
}

// TagRuleCovers matches a tag against a ruleset's ref conditions in the only
// forms the flow accepts (ruling, fix 4 item 7), since GitHub's fnmatch
// cannot be reproduced: an include pattern is ~ALL, the exact
// refs/tags/<tag>, or a single-segment prefix glob refs/tags/<prefix>*
// (no **, no ?, no [, no further /), and the exclusion list is empty. Any
// other form makes why name the pattern (protection_unmet).
func TagRuleCovers(include, exclude []string, tag string) (covered bool, why string) {
	if len(exclude) > 0 {
		return false, fmt.Sprintf("exclusion patterns %v are not accepted", exclude)
	}
	ref := "refs/tags/" + tag
	for _, p := range include {
		rest, ok := strings.CutPrefix(p, "refs/tags/")
		switch {
		case p == "~ALL" || p == ref:
			covered = true
		case !ok || rest == "":
			return false, fmt.Sprintf("include pattern %q is not accepted", p)
		case strings.ContainsAny(rest, "?[]\\/{}") || strings.Count(rest, "*") > 1:
			return false, fmt.Sprintf("include pattern %q is not accepted", p)
		case strings.HasSuffix(rest, "*"):
			covered = covered || strings.HasPrefix(tag, strings.TrimSuffix(rest, "*"))
		case strings.Contains(rest, "*"):
			return false, fmt.Sprintf("include pattern %q is not accepted", p)
		}
	}
	return covered, ""
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

// CommitExists asks the remote for the commit; a 404 or 422 (no commit for
// that SHA) is false.
func (g *GH) CommitExists(ctx context.Context, repository, commit string) (bool, error) {
	if _, err := g.run(ctx, "api", "repos/"+repository+"/commits/"+commit, "--jq", ".sha"); err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "422") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// BranchContains resolves the commit against the remote branch: the compare
// of branch...commit is identical or behind when the branch contains it; a
// commit or branch the remote does not know is false.
func (g *GH) BranchContains(ctx context.Context, repository, branch, commit string) (bool, error) {
	out, err := g.run(ctx, "api", "repos/"+repository+"/compare/"+branch+"..."+commit, "--jq", ".status")
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return false, nil
		}
		return false, err
	}
	return out == "identical" || out == "behind", nil
}
