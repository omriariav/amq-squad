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
	// runner, when set, answers a gh command line in this process instead of
	// starting the gh executable (a test seam; never set in production).
	runner func(ctx context.Context, args []string) (stdout []byte, stderr string, err error)
	// MaxTimelinePages bounds the force-push timeline read (default
	// DefaultMaxTimelinePages pages of 100).
	MaxTimelinePages int
}

// DefaultMaxTimelinePages is how many pages of 100 force-push events a PR's
// timeline read takes before it stops as history_incomplete.
const DefaultMaxTimelinePages = 50

func (g *GH) run(ctx context.Context, args ...string) ([]byte, error) {
	g.Calls++
	var out []byte
	var stderr string
	var err error
	if g.runner != nil {
		out, stderr, err = g.runner(ctx, args)
	} else {
		out, stderr, err = g.exec(ctx, args)
	}
	if err != nil {
		return out, fmt.Errorf("gh %s: %w: %s", strings.Join(args[:min(len(args), 3)], " "), err, strings.TrimSpace(stderr))
	}
	return out, nil
}

func (g *GH) exec(ctx context.Context, args []string) ([]byte, string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if g.Env != nil {
		cmd.Env = g.Env
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	return out, stderr.String(), err
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
	raw, err := g.run(ctx, "pr", "view", n, "-R", repository, "--json", "number,title,state,isDraft,baseRefName,baseRefOid,headRefOid,mergeable,mergeCommit")
	if err != nil {
		return PRInfo{}, err
	}
	var v struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		State       string `json:"state"`
		IsDraft     bool   `json:"isDraft"`
		BaseRefName string `json:"baseRefName"`
		BaseRefOid  string `json:"baseRefOid"`
		HeadRefOid  string `json:"headRefOid"`
		Mergeable   string `json:"mergeable"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return PRInfo{}, fmt.Errorf("decode PR #%d: %w", number, err)
	}
	pr := PRInfo{Number: v.Number, Title: v.Title, State: v.State, Draft: v.IsDraft, Repository: repository, BaseRef: v.BaseRefName,
		BaseSHA: v.BaseRefOid, HeadSHA: v.HeadRefOid, Mergeable: v.Mergeable}
	if v.MergeCommit != nil {
		pr.MergeCommit = v.MergeCommit.Oid
	}
	if pr.ForcePushes, pr.HistoryIncomplete, err = g.forcePushes(ctx, repository, n); err != nil {
		return PRInfo{}, fmt.Errorf("force pushes of #%d: %w", number, err)
	}
	return pr, nil
}

// forcePushes reads the PR's whole force-push timeline, page by page with
// the cursor (5 re-pass, P1-2: a single page hid every event after the
// first 100, the train's own audited rebases included). A read that the page
// bound or a rate limit leaves partial returns the reason, which the check
// refuses as history_incomplete; any other error is an error.
func (g *GH) forcePushes(ctx context.Context, repository, n string) ([]ForcePush, string, error) {
	owner, name, _ := strings.Cut(repository, "/")
	maxPages := g.MaxTimelinePages
	if maxPages <= 0 {
		maxPages = DefaultMaxTimelinePages
	}
	var out []ForcePush
	cursor := ""
	for page := 1; ; page++ {
		if page > maxPages {
			return out, fmt.Sprintf("the force-push timeline has more than %d pages of 100 events; %d read", maxPages, len(out)), nil
		}
		args := []string{"api", "graphql", "-F", "o=" + owner, "-F", "r=" + name, "-F", "n=" + n}
		if cursor != "" {
			args = append(args, "-F", "c="+cursor)
		}
		args = append(args, "-f", "query="+forcePushQuery, "--jq", forcePushJQ)
		raw, err := g.run(ctx, args...)
		if err != nil {
			if rateLimited(err) {
				return out, fmt.Sprintf("a rate limit stopped the force-push timeline at page %d (%d events read): %v", page, len(out), err), nil
			}
			return nil, "", err
		}
		var pg struct {
			Nodes  []ForcePush `json:"nodes"`
			Next   bool        `json:"next"`
			Cursor string      `json:"cursor"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(raw), &pg); err != nil {
			return nil, "", fmt.Errorf("decode timeline page %d: %w", page, err)
		}
		out = append(out, pg.Nodes...)
		if !pg.Next {
			return out, "", nil
		}
		if pg.Cursor == "" || pg.Cursor == cursor {
			return out, fmt.Sprintf("the force-push timeline page %d has no new cursor", page), nil
		}
		cursor = pg.Cursor
	}
}

func rateLimited(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "rate limit") || strings.Contains(s, "ratelimited")
}

const forcePushQuery = `query($o:String!,$r:String!,$n:Int!,$c:String){repository(owner:$o,name:$r){pullRequest(number:$n){timelineItems(itemTypes:[HEAD_REF_FORCE_PUSHED_EVENT],first:100,after:$c){pageInfo{hasNextPage endCursor} nodes{... on HeadRefForcePushedEvent{beforeCommit{oid} afterCommit{oid} actor{login}}}}}}}`

const forcePushJQ = `.data.repository.pullRequest.timelineItems | {nodes: [.nodes[] | {before: .beforeCommit.oid, after: .afterCommit.oid, actor: (.actor.login // "")}], next: .pageInfo.hasNextPage, cursor: (.pageInfo.endCursor // "")}`

// Diff reads the compare endpoint for the exact base...head pair: one
// snapshot, bound to the SHAs the train checks and pins.
func (g *GH) Diff(ctx context.Context, repository, base, head string) ([]FileChange, error) {
	raw, err := g.run(ctx, "api", "repos/"+repository+"/compare/"+base+"..."+head,
		"--jq", `.files[]? | {path: .filename, previous_path: (.previous_filename // ""), status, additions, deletions}`)
	if err != nil {
		return nil, err
	}
	return lines[FileChange](raw)
}

func (g *GH) Checks(ctx context.Context, repository, sha string) ([]CheckRun, error) {
	raw, err := g.run(ctx, "api", "--paginate", "repos/"+repository+"/commits/"+sha+"/check-runs?per_page=100",
		"--jq", `.check_runs[] | {id, name, app_id: (.app.id // 0), status, conclusion: (.conclusion // "")}`)
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
			Context       string `json:"context"`
			IntegrationID int    `json:"integration_id"`
		} `json:"required_status_checks"`
	} `json:"parameters"`
}

// classicJSON is GET /repos/{r}/branches/{b}/protection.
type classicJSON struct {
	RequiredStatusChecks *struct {
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
			AppID   *int   `json:"app_id"`
		} `json:"checks"`
	} `json:"required_status_checks"`
	RequiredPullRequestReviews *struct {
		Bypass *struct {
			Users []json.RawMessage `json:"users"`
			Teams []json.RawMessage `json:"teams"`
			Apps  []json.RawMessage `json:"apps"`
		} `json:"bypass_pull_request_allowances"`
	} `json:"required_pull_request_reviews"`
	EnforceAdmins struct {
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
// together, as they bind the merging identity (the owner, an admin). A rule
// from a ruleset with bypass actors binds nobody we can rely on, classic
// protection without enforce_admins exempts the owner, and a classic PR rule
// with bypass_pull_request_allowances lets someone push without a PR: none
// of these count, and the note says why. A required check counts only bound
// to its app (fix 2, P1-1 and N2): a check that names no app accepts any
// app's success, so it is ci_app_unbound and the owner binds it to its app
// in the ruleset.
func evaluateProtection(rules []ruleJSON, bypass map[int]int, classic *classicJSON) Protection {
	var p Protection
	addCheck := func(c RequiredCheck) {
		if c.Context != "" && !slices.Contains(p.Checks, c) {
			p.Checks = append(p.Checks, c)
		}
	}
	for _, r := range rules {
		src := "ruleset " + strconv.Itoa(r.RulesetID)
		if !slices.Contains(p.Sources, src) {
			p.Sources = append(p.Sources, src)
		}
		enforced := bypass[r.RulesetID] == 0
		switch r.Type {
		case "required_status_checks":
			if !enforced {
				p.Notes = append(p.Notes, src+" has bypass actors: its required checks do not bind the owner")
				continue
			}
			for _, c := range r.Parameters.RequiredStatusChecks {
				if c.IntegrationID <= 0 {
					p.Notes = append(p.Notes, "ci_app_unbound: "+src+" requires "+c.Context+" from any app; bind it to its app in the ruleset")
					continue
				}
				addCheck(RequiredCheck{Context: c.Context, AppID: c.IntegrationID})
			}
		case "pull_request":
			if enforced {
				p.DirectPushRefused = true
			} else {
				p.Notes = append(p.Notes, src+" has bypass actors: the owner can push directly")
			}
		case "non_fast_forward":
			p.ForcePushRefused = p.ForcePushRefused || enforced
		}
	}
	if classic != nil {
		p.Sources = append(p.Sources, "classic")
		admins := classic.EnforceAdmins.Enabled
		if c := classic.RequiredStatusChecks; c != nil {
			if !admins {
				p.Notes = append(p.Notes, "classic required checks exempt admins (enforce_admins off): they do not bind the owner")
			} else {
				// contexts is the legacy app-less list beside checks: only
				// app-bound checks count
				for _, s := range c.Checks {
					if s.AppID == nil || *s.AppID <= 0 {
						p.Notes = append(p.Notes, "ci_app_unbound: classic protection requires "+s.Context+" from any app; bind it to its app")
						continue
					}
					addCheck(RequiredCheck{Context: s.Context, AppID: *s.AppID})
				}
			}
		}
		reviews := classic.RequiredPullRequestReviews
		switch {
		case reviews != nil && reviews.Bypass != nil && len(reviews.Bypass.Users)+len(reviews.Bypass.Teams)+len(reviews.Bypass.Apps) > 0:
			p.Notes = append(p.Notes, "classic protection has bypass_pull_request_allowances: an allowed identity can push without a PR")
		case reviews != nil && admins:
			p.DirectPushRefused = true
		case reviews != nil:
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

// Rebase runs GitHub's updatePullRequestBranch with updateMethod REBASE and
// expectedHeadOid pinned, and returns the head the mutation reports: the SHA
// the server pushed, which is the only SHA the train binds.
func (g *GH) Rebase(ctx context.Context, repository string, number int, expect string) (string, error) {
	id, err := g.run(ctx, "pr", "view", strconv.Itoa(number), "-R", repository, "--json", "id", "--jq", ".id")
	if err != nil {
		return "", err
	}
	out, err := g.run(ctx, "api", "graphql", "-F", "id="+strings.TrimSpace(string(id)), "-F", "oid="+expect, "-f", "query="+rebaseMutation,
		"--jq", ".data.updatePullRequestBranch.pullRequest.headRefOid")
	return strings.TrimSpace(string(out)), err
}

const rebaseMutation = `mutation($id:ID!,$oid:GitObjectID!){updatePullRequestBranch(input:{pullRequestId:$id,expectedHeadOid:$oid,updateMethod:REBASE}){pullRequest{headRefOid}}}`

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

func (g *GH) Releases(ctx context.Context, repository string) ([]Release, error) {
	raw, err := g.run(ctx, "api", "--paginate", "repos/"+repository+"/releases?per_page=100", "--jq", `.[] | {id, tag: .tag_name}`)
	if err != nil {
		return nil, err
	}
	return lines[Release](raw)
}
