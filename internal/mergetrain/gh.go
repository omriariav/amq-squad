package mergetrain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// GH is the Forge over the owner's gh CLI: the train runs under the owner's
// identity outside any seat process (7.3, R33). Merges go through the REST
// merge endpoint with the head pinned (sha=), never `gh pr merge`, which can
// turn itself into auto-merge when checks are pending.
type GH struct {
	Bin string // gh executable (default "gh")
	// Env, when set, is the gh process environment (tests point it at a fake).
	Env []string
	// Calls counts gh invocations (measured in the S6 dry run).
	Calls int
}

func (g *GH) run(ctx context.Context, args ...string) ([]byte, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	g.Calls++
	cmd := exec.CommandContext(ctx, bin, args...)
	if g.Env != nil {
		cmd.Env = g.Env
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("gh %s: %w: %s", strings.Join(args[:min(len(args), 3)], " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// lines decodes one JSON value per line (the --jq '.[]' form of a paginated
// list) into dst items.
func lines[T any](raw []byte) ([]T, error) {
	var out []T
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

func (g *GH) PR(ctx context.Context, repository string, number int) (PRInfo, error) {
	n := strconv.Itoa(number)
	raw, err := g.run(ctx, "pr", "view", n, "-R", repository, "--json", "number,state,isDraft,baseRefName,baseRefOid,headRefOid,mergeable")
	if err != nil {
		return PRInfo{}, err
	}
	var v struct {
		Number      int    `json:"number"`
		State       string `json:"state"`
		IsDraft     bool   `json:"isDraft"`
		BaseRefName string `json:"baseRefName"`
		BaseRefOid  string `json:"baseRefOid"`
		HeadRefOid  string `json:"headRefOid"`
		Mergeable   string `json:"mergeable"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return PRInfo{}, fmt.Errorf("decode PR #%d: %w", number, err)
	}
	pr := PRInfo{Number: v.Number, State: v.State, Draft: v.IsDraft, Repository: repository, BaseRef: v.BaseRefName,
		BaseSHA: v.BaseRefOid, HeadSHA: v.HeadRefOid, Mergeable: v.Mergeable}
	raw, err = g.run(ctx, "api", "--paginate", "repos/"+repository+"/pulls/"+n+"/files?per_page=100",
		"--jq", `.[] | {path: .filename, previous_path: (.previous_filename // ""), status, additions, deletions}`)
	if err != nil {
		return PRInfo{}, err
	}
	if pr.Files, err = lines[FileChange](raw); err != nil {
		return PRInfo{}, fmt.Errorf("decode files of #%d: %w", number, err)
	}
	owner, name, _ := strings.Cut(repository, "/")
	raw, err = g.run(ctx, "api", "graphql", "-F", "o="+owner, "-F", "r="+name, "-F", "n="+n, "-f", "query="+forcePushQuery,
		"--jq", `.data.repository.pullRequest.timelineItems.nodes[] | {before: .beforeCommit.oid, after: .afterCommit.oid, actor: (.actor.login // "")}`)
	if err != nil {
		return PRInfo{}, err
	}
	if pr.ForcePushes, err = lines[ForcePush](raw); err != nil {
		return PRInfo{}, fmt.Errorf("decode force pushes of #%d: %w", number, err)
	}
	return pr, nil
}

const forcePushQuery = `query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){timelineItems(itemTypes:[HEAD_REF_FORCE_PUSHED_EVENT],first:100){nodes{... on HeadRefForcePushedEvent{beforeCommit{oid} afterCommit{oid} actor{login}}}}}}}`

func (g *GH) Checks(ctx context.Context, repository, sha string) ([]CheckRun, error) {
	raw, err := g.run(ctx, "api", "--paginate", "repos/"+repository+"/commits/"+sha+"/check-runs?per_page=100",
		"--jq", `.check_runs[] | {name, status, conclusion: (.conclusion // "")}`)
	if err != nil {
		return nil, err
	}
	runs, err := lines[CheckRun](raw)
	if err != nil {
		return nil, err
	}
	raw, err = g.run(ctx, "api", "repos/"+repository+"/commits/"+sha+"/status",
		"--jq", `.statuses[] | {name: .context, status: (if .state == "pending" then "pending" else "completed" end), conclusion: .state}`)
	if err != nil {
		return nil, err
	}
	statuses, err := lines[CheckRun](raw)
	return append(runs, statuses...), err
}

func (g *GH) BranchHead(ctx context.Context, repository, branch string) (string, error) {
	raw, err := g.run(ctx, "api", "repos/"+repository+"/branches/"+branch, "--jq", ".commit.sha")
	return strings.TrimSpace(string(raw)), err
}

// ruleJSON is one effective rule of GET /repos/{r}/rules/branches/{b}.
type ruleJSON struct {
	Type       string `json:"type"`
	RulesetID  int    `json:"ruleset_id"`
	Parameters struct {
		RequiredStatusChecks []struct {
			Context string `json:"context"`
		} `json:"required_status_checks"`
	} `json:"parameters"`
}

// classicJSON is GET /repos/{r}/branches/{b}/protection.
type classicJSON struct {
	RequiredStatusChecks *struct {
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
		} `json:"checks"`
	} `json:"required_status_checks"`
	RequiredPullRequestReviews *json.RawMessage `json:"required_pull_request_reviews"`
	EnforceAdmins              struct {
		Enabled bool `json:"enabled"`
	} `json:"enforce_admins"`
	AllowForcePushes struct {
		Enabled bool `json:"enabled"`
	} `json:"allow_force_pushes"`
}

func (g *GH) Protection(ctx context.Context, repository, branch string) (Protection, error) {
	raw, err := g.run(ctx, "api", "repos/"+repository+"/rules/branches/"+branch)
	if err != nil {
		return Protection{}, err
	}
	var rules []ruleJSON
	if err := json.Unmarshal(raw, &rules); err != nil {
		return Protection{}, fmt.Errorf("decode rules: %w", err)
	}
	bypass := map[int]int{}
	for _, r := range rules {
		if _, seen := bypass[r.RulesetID]; seen || r.RulesetID == 0 {
			continue
		}
		raw, err := g.run(ctx, "api", "repos/"+repository+"/rulesets/"+strconv.Itoa(r.RulesetID), "--jq", ".bypass_actors | length")
		if err != nil {
			return Protection{}, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			n = -1 // unreadable bypass list: assume the owner may bypass
		}
		bypass[r.RulesetID] = n
	}
	var classic *classicJSON
	raw, err = g.run(ctx, "api", "repos/"+repository+"/branches/"+branch+"/protection")
	switch {
	case err == nil:
		classic = &classicJSON{}
		if err := json.Unmarshal(raw, classic); err != nil {
			return Protection{}, fmt.Errorf("decode branch protection: %w", err)
		}
	case strings.Contains(err.Error(), "404"):
	default:
		return Protection{}, err
	}
	return evaluateProtection(rules, bypass, classic), nil
}

// evaluateProtection reads the effective rules and the classic protection
// together. Direct push counts as refused only when the owner cannot bypass
// it: a ruleset pull_request rule whose ruleset has no bypass actors, or
// classic required reviews with enforce_admins on (the owner is the admin).
func evaluateProtection(rules []ruleJSON, bypass map[int]int, classic *classicJSON) Protection {
	var p Protection
	addCheck := func(c string) {
		if c != "" && !slices.Contains(p.RequiredChecks, c) {
			p.RequiredChecks = append(p.RequiredChecks, c)
		}
	}
	for _, r := range rules {
		src := "ruleset " + strconv.Itoa(r.RulesetID)
		if !slices.Contains(p.Sources, src) {
			p.Sources = append(p.Sources, src)
		}
		switch r.Type {
		case "required_status_checks":
			for _, c := range r.Parameters.RequiredStatusChecks {
				addCheck(c.Context)
			}
		case "pull_request":
			if bypass[r.RulesetID] == 0 {
				p.DirectPushRefused = true
			} else {
				p.Notes = append(p.Notes, src+" has bypass actors: the owner can push directly")
			}
		case "non_fast_forward":
			p.ForcePushRefused = true
		}
	}
	if classic != nil {
		p.Sources = append(p.Sources, "classic")
		if c := classic.RequiredStatusChecks; c != nil {
			for _, s := range c.Contexts {
				addCheck(s)
			}
			for _, s := range c.Checks {
				addCheck(s.Context)
			}
		}
		switch {
		case classic.RequiredPullRequestReviews != nil && classic.EnforceAdmins.Enabled:
			p.DirectPushRefused = true
		case classic.RequiredPullRequestReviews != nil:
			p.Notes = append(p.Notes, "classic protection requires a PR but not for admins: the owner can push directly")
		default:
			p.Notes = append(p.Notes, "classic protection requires no PR: direct push is allowed")
		}
		if !classic.AllowForcePushes.Enabled {
			p.ForcePushRefused = true
		}
	}
	if len(rules) == 0 && classic == nil {
		p.Notes = append(p.Notes, "no ruleset and no branch protection")
	}
	return p
}

func (g *GH) Ready(ctx context.Context, repository string, number int) error {
	_, err := g.run(ctx, "pr", "ready", strconv.Itoa(number), "-R", repository)
	return err
}

func (g *GH) Rebase(ctx context.Context, repository string, number int) error {
	_, err := g.run(ctx, "pr", "update-branch", strconv.Itoa(number), "-R", repository, "--rebase")
	return err
}

func (g *GH) Merge(ctx context.Context, repository string, number int, head string) (string, error) {
	raw, err := g.run(ctx, "api", "-X", "PUT", "repos/"+repository+"/pulls/"+strconv.Itoa(number)+"/merge",
		"-f", "merge_method=squash", "-f", "sha="+head, "--jq", ".sha")
	return strings.TrimSpace(string(raw)), err
}

func (g *GH) Tags(ctx context.Context, repository string) ([]Tag, error) {
	raw, err := g.run(ctx, "api", "--paginate", "repos/"+repository+"/tags?per_page=100", "--jq", `.[] | {name, sha: .commit.sha}`)
	if err != nil {
		return nil, err
	}
	return lines[Tag](raw)
}

func (g *GH) Releases(ctx context.Context, repository string) ([]string, error) {
	raw, err := g.run(ctx, "api", "--paginate", "repos/"+repository+"/releases?per_page=100", "--jq", `.[] | .tag_name`)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}
