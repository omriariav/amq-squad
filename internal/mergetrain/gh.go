package mergetrain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
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
		return out, &GHError{Command: strings.Join(args[:min(len(args), 3)], " "), Err: err, Stderr: stderr}
	}
	return out, nil
}

// GHError is a gh command that failed: the command, the exec (or the
// context's) error and gh's stderr exactly as gh printed it. Its text is
// the one GH.run has always returned; Stderr lets definiteRefusal read
// gh's own last line without this wrapper around it.
type GHError struct {
	Command string
	Err     error
	Stderr  string
}

func (e *GHError) Error() string {
	return fmt.Sprintf("gh %s: %v: %s", e.Command, e.Err, strings.TrimSpace(e.Stderr))
}

func (e *GHError) Unwrap() error { return e.Err }

func (g *GH) exec(ctx context.Context, args []string) ([]byte, string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if g.Env != nil {
		cmd.Env = g.Env
	}
	// a gh killed at the context's deadline may leave a child holding its
	// pipes (a credential helper, a pager); Output then returns after
	// WaitDelay instead of waiting for the child (the run's bound, Codex's
	// re-pass on #903)
	cmd.WaitDelay = 2 * time.Second
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

const forcePushQuery = `query($o:String!,$r:String!,$n:Int!,$c:String){repository(owner:$o,name:$r){pullRequest(number:$n){timelineItems(itemTypes:[HEAD_REF_FORCE_PUSHED_EVENT],first:100,after:$c){pageInfo{hasNextPage endCursor} nodes{... on HeadRefForcePushedEvent{createdAt beforeCommit{oid} afterCommit{oid} actor{login}}}}}}}`

const forcePushJQ = `.data.repository.pullRequest.timelineItems | {nodes: [.nodes[] | {before: .beforeCommit.oid, after: .afterCommit.oid, actor: (.actor.login // ""), at: (.createdAt // "")}], next: .pageInfo.hasNextPage, cursor: (.pageInfo.endCursor // "")}`

// compareProofFilesJQ is unique to the proof so its call is distinguishable from every other file-list call.
const compareProofFilesJQ = `.files[]? | {path: .filename, previous_path: (.previous_filename // ""), additions, deletions, status}`

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

// Behind reads GitHub's ancestry result for the exact pinned base and head.
func (g *GH) Behind(ctx context.Context, repository, base, head string) (bool, error) {
	raw, err := g.run(ctx, "api", "repos/"+repository+"/compare/"+base+"..."+head, "--jq", ".behind_by")
	if err != nil {
		return false, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return false, fmt.Errorf("decode compare behind_by: %w", err)
	}
	return n > 0, nil
}

func (g *GH) PatchID(ctx context.Context, repository, base, head string) (string, error) {
	path := "repos/" + repository + "/compare/" + base + "..." + head
	raw, err := g.run(ctx, "api", path, "--jq", compareProofFilesJQ)
	if err != nil {
		if compareTooLarge(err) {
			return "", ErrIncompleteDiff
		}
		return "", err
	}
	files, err := lines[FileChange](raw)
	if err != nil {
		return "", fmt.Errorf("decode compare file list: %w", err)
	}
	if len(files) >= MaxCompareFiles {
		return "", ErrIncompleteDiff
	}
	// gh refuses to print a response holding terminal escape bytes (a
	// captured log in the diff), also when its output is captured, unless
	// this flag is passed (brief 320-train-escape-legacy, E2). The bytes go
	// only to git patch-id's stdin below; no other gh call passes it.
	diff, err := g.run(ctx, "api", path,
		"-H", "Accept: application/vnd.github.diff", "--allow-escape-sequences")
	if unknownEscapeFlag(err) {
		// a gh older than the flag also prints the bytes unrefused: that
		// answer, and no other, is read once more without it (E3, E4)
		diff, err = g.run(ctx, "api", path,
			"-H", "Accept: application/vnd.github.diff")
	}
	if err != nil {
		if compareTooLarge(err) {
			return "", ErrIncompleteDiff
		}
		return "", err
	}
	if mismatch := compareDiffMismatch(files, diff); mismatch != "" {
		return "", fmt.Errorf("%w: %s", ErrIncompleteDiff, mismatch)
	}
	cmd := exec.CommandContext(ctx, "git", "patch-id", "--stable")
	cmd.Stdin = bytes.NewReader(diff)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git patch-id --stable: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("git patch-id --stable returned no patch id")
	}
	return fields[0], nil
}

// compareDiffMismatch checks that a compare's file list and API diff describe
// the same files and per-file line counts. It returns the mismatch detail.
func compareDiffMismatch(files []FileChange, diff []byte) string {
	const (
		filesMismatch   = "the diff's files are not the compare's files"
		countsMismatch  = "the diff's added and removed lines are not the compare's counts"
		zeroMismatch    = "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines"
		cutMismatch     = "the diff is cut short inside a hunk"
		newlineMismatch = "the diff does not end with a newline"
		headerMismatch  = "a file's section holds a line that is neither a header line nor in a hunk"
		indexMismatch   = "a file with no changed line holds an index line and neither a binary part nor an empty file's ids"
	)
	if len(diff) > 0 && diff[len(diff)-1] != '\n' {
		return newlineMismatch + ": the compare diff ends inside its last line"
	}
	type wantedFile struct {
		file      FileChange
		seen      bool
		add       int
		del       int
		hunk      bool
		index     bool
		indexLine string
		binary    bool
		sim100    bool
		from      bool
		to        bool
	}
	wanted := make(map[string]*wantedFile, len(files))
	for _, file := range files {
		from := file.Path
		if file.PreviousPath != "" {
			from = file.PreviousPath
		}
		header := "diff --git a/" + from + " b/" + file.Path
		if _, exists := wanted[header]; exists {
			return filesMismatch + ": duplicate compare entry for " + strconv.Quote(file.Path)
		}
		wanted[header] = &wantedFile{file: file}
	}
	if len(files) == 0 && len(diff) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(string(diff), "\n"), "\n")
	var current *wantedFile
	oldLeft, newLeft := 0, 0
	inHunk := false
	markerAllowed := false
	markerCount := 0
	binaryPart := false
	finishHunk := func() string {
		if inHunk && (oldLeft != 0 || newLeft != 0) {
			name := ""
			if current != nil {
				name = current.file.Path
			}
			return cutMismatch + ": " + strconv.Quote(name)
		}
		inHunk = false
		return ""
	}
	for _, line := range lines {
		if inHunk && oldLeft == 0 && newLeft == 0 {
			inHunk = false
		}
		if line == `\ No newline at end of file` {
			if markerAllowed {
				if markerCount >= 2 {
					return headerMismatch + ": " + strconv.Quote(current.file.Path)
				}
				markerCount++
				markerAllowed = false
				continue
			}
			if inHunk {
				return cutMismatch + ": " + strconv.Quote(current.file.Path)
			}
			return headerMismatch + ": " + strconv.Quote(current.file.Path)
		}
		if inHunk {
			switch {
			case strings.HasPrefix(line, " "):
				oldLeft--
				newLeft--
			case strings.HasPrefix(line, "-"):
				oldLeft--
				current.del++
			case strings.HasPrefix(line, "+"):
				newLeft--
				current.add++
			default:
				return cutMismatch + ": " + strconv.Quote(current.file.Path)
			}
			if oldLeft < 0 || newLeft < 0 {
				return cutMismatch + ": " + strconv.Quote(current.file.Path)
			}
			markerAllowed = true
			continue
		}
		if strings.HasPrefix(line, "diff --git ") {
			if mismatch := finishHunk(); mismatch != "" {
				return mismatch
			}
			next, ok := wanted[line]
			if !ok {
				return filesMismatch + ": unexpected section " + strconv.Quote(line)
			}
			if next.seen {
				return filesMismatch + ": duplicate section for " + strconv.Quote(next.file.Path)
			}
			next.seen = true
			current = next
			markerAllowed = false
			markerCount = 0
			binaryPart = false
			continue
		}
		if current == nil {
			if line == "" {
				continue
			}
			return filesMismatch + ": content before the first file section"
		}
		if binaryPart {
			if compareBinaryPatchLine(line) {
				continue
			}
			return headerMismatch + ": " + strconv.Quote(current.file.Path)
		}
		if strings.HasPrefix(line, "@@ ") {
			old, new, ok := compareHunkLengths(line)
			if !ok {
				return cutMismatch + ": malformed hunk in " + strconv.Quote(current.file.Path)
			}
			current.hunk = true
			oldLeft, newLeft, inHunk = old, new, true
			markerAllowed = false
			markerCount = 0
			continue
		}
		markerAllowed = false
		switch {
		case strings.HasPrefix(line, "index "):
			current.index = true
			current.indexLine = line
		case strings.HasPrefix(line, "new file mode "), strings.HasPrefix(line, "deleted file mode "):
		case strings.HasPrefix(line, "similarity index "):
			current.sim100 = line == "similarity index 100%"
		case strings.HasPrefix(line, "rename from "):
			current.from = line == "rename from "+current.file.PreviousPath
		case strings.HasPrefix(line, "rename to "):
			current.to = line == "rename to "+current.file.Path
		case strings.HasPrefix(line, "old mode "), strings.HasPrefix(line, "new mode "):
		case strings.HasPrefix(line, "dissimilarity index "), strings.HasPrefix(line, "copy from "), strings.HasPrefix(line, "copy to "):
		case strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ"):
			current.binary = true
		case (strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")) && !current.hunk:
		case line == "GIT binary patch" && !current.hunk:
			current.binary = true
			binaryPart = true
		default:
			return headerMismatch + ": " + strconv.Quote(current.file.Path)
		}
	}
	if mismatch := finishHunk(); mismatch != "" {
		return mismatch
	}
	for _, file := range files {
		from := file.Path
		if file.PreviousPath != "" {
			from = file.PreviousPath
		}
		got := wanted["diff --git a/"+from+" b/"+file.Path]
		if !got.seen {
			return filesMismatch + ": missing section for " + strconv.Quote(file.Path)
		}
		if file.Additions == 0 && file.Deletions == 0 {
			rename := file.PreviousPath != "" && got.sim100 && got.from && got.to
			if got.hunk || (!got.index && !rename) {
				return zeroMismatch + ": " + strconv.Quote(file.Path)
			}
			if got.index && !got.binary && !compareEmptyFileIndex(got.indexLine) {
				return indexMismatch + ": " + strconv.Quote(file.Path)
			}
			continue
		}
		if got.add != file.Additions || got.del != file.Deletions {
			return countsMismatch + ": " + strconv.Quote(file.Path)
		}
	}
	return ""
}

// emptyBlobID is git's full object id for an empty blob.
const emptyBlobID = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"

// compareEmptyFileIndex reports whether an index line is the added or deleted
// empty-file form, using an abbreviation of 7 through 40 hexadecimal digits.
func compareEmptyFileIndex(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "index" {
		return false
	}
	a, b, ok := strings.Cut(fields[1], "..")
	if !ok {
		return false
	}
	zero := func(s string) bool {
		return len(s) >= 7 && len(s) <= 40 && strings.Trim(s, "0") == ""
	}
	empty := func(s string) bool {
		return len(s) >= 7 && len(s) <= len(emptyBlobID) && strings.HasPrefix(emptyBlobID, s)
	}
	return zero(a) && empty(b) || empty(a) && zero(b)
}

// compareBinaryPatchLine recognizes the measured git binary-patch body and
// the documented delta length form.
func compareBinaryPatchLine(line string) bool {
	if line == "" {
		return true
	}
	for _, prefix := range []string{"literal ", "delta "} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			if rest == "" {
				return false
			}
			for _, r := range rest {
				if r < '0' || r > '9' {
					return false
				}
			}
			return true
		}
	}
	if (line[0] < 'A' || line[0] > 'Z') && (line[0] < 'a' || line[0] > 'z') {
		return false
	}
	const base85 = "!#$%&()*+-;<=>?@^_`{|}~"
	for _, r := range line[1:] {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && !strings.ContainsRune(base85, r) {
			return false
		}
	}
	n := int(line[0]-'A') + 1
	if line[0] >= 'a' {
		n = int(line[0]-'a') + 27
	}
	return len(line)-1 == (n+3)/4*5
}

// compareHunkLengths reads the old and new lengths from a unified hunk header.
func compareHunkLengths(line string) (old, new int, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] != "@@" || fields[3] != "@@" {
		return 0, 0, false
	}
	parse := func(field string, sign byte) (int, bool) {
		if len(field) < 2 || field[0] != sign {
			return 0, false
		}
		start, length, hasLength := strings.Cut(field[1:], ",")
		if !compareDigits(start) {
			return 0, false
		}
		if !hasLength {
			return 1, true
		}
		if !compareDigits(length) {
			return 0, false
		}
		n, err := strconv.Atoi(length)
		return n, err == nil && n >= 0
	}
	old, oldOK := parse(fields[1], '-')
	new, newOK := parse(fields[2], '+')
	return old, new, oldOK && newOK
}

// compareDigits reports whether s is one or more ASCII decimal digits.
func compareDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// unknownEscapeFlag reports gh's own answer to a flag it does not carry.
func unknownEscapeFlag(err error) bool {
	var ge *GHError
	return errors.As(err, &ge) && strings.Contains(ge.Stderr, "unknown flag: --allow-escape-sequences")
}

func compareTooLarge(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "diff too large") || strings.Contains(s, "http 406") || strings.Contains(s, "http 422")
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

// FirstParent reads a commit's first parent ("" for a root commit).
func (g *GH) FirstParent(ctx context.Context, repository, sha string) (string, error) {
	raw, err := g.run(ctx, "api", "repos/"+repository+"/commits/"+sha, "--jq", `.parents[0].sha // ""`)
	return strings.TrimSpace(string(raw)), err
}

// ruleJSON is one effective rule of GET /repos/{r}/rules/branches/{b}.
type ruleJSON struct {
	Type       string `json:"type"`
	RulesetID  int    `json:"ruleset_id"`
	Parameters struct {
		StrictRequiredStatusChecksPolicy bool `json:"strict_required_status_checks_policy"`
		RequiredStatusChecks             []struct {
			Context       string `json:"context"`
			IntegrationID int    `json:"integration_id"`
		} `json:"required_status_checks"`
	} `json:"parameters"`
}

// classicJSON is GET /repos/{r}/branches/{b}/protection.
type classicJSON struct {
	RequiredStatusChecks *struct {
		Strict   bool     `json:"strict"`
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
			p.Strict = p.Strict || r.Parameters.StrictRequiredStatusChecksPolicy
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
				p.Strict = p.Strict || c.Strict
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
// expectedHeadOid pinned, and returns the head the mutation reports. GitHub
// performs the update asynchronously and answers with the head as it was
// (run mt-20261105T192313Z-ab184d07 on #894: the mutation answered 68792071,
// the branch moved to 2555d34a five seconds later), so the train never binds
// this answer: it reads the head back until it moves (Train.readUpdatedHead).
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
