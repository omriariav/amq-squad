package mergetrain

// A fake gh over a local bare repository. Two ways to reach the same fake:
// in process (GH.runner calls fakeGHRun; the default for every fixture, so a
// gh call costs a function call and the work in the bare repository) and as a
// subprocess (the test binary is the fake: with FAKE_GH_STATE set, TestMain
// runs fakeGH(os.Args[1:]) instead of the tests; the few rows that cover gh's
// real command path use it, see newSubprocessFixture). It answers exactly the gh command lines GH runs, in the shape GH's --jq
// filters produce, from real git objects: diffs, merge-tree conflicts,
// squash merges and rebases happen in the bare repository.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakePR struct {
	Head        string      `json:"head"` // branch
	Title       string      `json:"title,omitempty"`
	Base        string      `json:"base"`
	Draft       bool        `json:"draft"`
	State       string      `json:"state"`
	ForcePushes []ForcePush `json:"force_pushes,omitempty"`
	MergeCommit string      `json:"merge_commit,omitempty"`
}

type fakeState struct {
	Bare     string             `json:"bare"`
	Repo     string             `json:"repo"`
	Rules    json.RawMessage    `json:"rules"` // GET rules/branches/main
	Classic  json.RawMessage    `json:"classic,omitempty"`
	PRs      map[string]*fakePR `json:"prs"`
	RedCI    []string           `json:"red_ci,omitempty"` // head SHAs whose ci check fails
	NoCI     []string           `json:"no_ci,omitempty"`  // head SHAs where no check ran
	Releases []Release          `json:"releases,omitempty"`
	// ExtraRuns are more check runs per head SHA (another app's results).
	ExtraRuns map[string][]CheckRun `json:"extra_runs,omitempty"`
	CIApp     int                   `json:"ci_app"` // the app id of the fixture's ci check
	// PushAfterRebase are PR numbers that get a push right after the
	// server-side rebase reports its head.
	PushAfterRebase []string `json:"push_after_rebase,omitempty"`
	// AlterAfterRebase makes the fake server change a seat hunk while updating.
	AlterAfterRebase []string `json:"alter_after_rebase,omitempty"`
	// AdvanceBaseBeforeRebase moves the base after the train pins it.
	AdvanceBaseBeforeRebase []string `json:"advance_base_before_rebase,omitempty"`
	// TruncateAfterRebase serves the updated head's diff without its last line.
	TruncateAfterRebase []string `json:"truncate_after_rebase,omitempty"`
	// CapCompareAfterRebase reports MaxCompareFiles for the updated head.
	CapCompareAfterRebase []string `json:"cap_compare_after_rebase,omitempty"`
	// RejectCompareAfterRebase makes the updated compare metadata return HTTP 422.
	RejectCompareAfterRebase []string `json:"reject_compare_after_rebase,omitempty"`
	// NoPatchFiles are files for which the completeness answer reports no patch.
	NoPatchFiles []string `json:"no_patch_files,omitempty"`
	// OmitDiffFiles are files whose whole section is left out of the diff answer.
	OmitDiffFiles []string `json:"omit_diff_files,omitempty"`
	// OmitDiffIndex are files whose index line is left out of the diff answer.
	OmitDiffIndex []string `json:"omit_diff_index,omitempty"`
	// ExtraAdditionFiles gain one addition in the proof's file-list answer.
	ExtraAdditionFiles []string `json:"extra_addition_files,omitempty"`
	CIName             string   `json:"ci_name"`
	FailMerge          bool     `json:"fail_merge,omitempty"`
	// LoseMergeResponse: the merge happens on the server, then the
	// connection fails before the response (5 re-pass, P2-5).
	LoseMergeResponse bool `json:"lose_merge_response,omitempty"`
	// RateLimitPage: the timeline read of that page (1-based) answers a rate
	// limit (5 re-pass, P1-2).
	RateLimitPage int `json:"rate_limit_page,omitempty"`
	// AsyncUpdate are PR numbers whose updatePullRequestBranch is GitHub's
	// asynchronous one (brief 3.2.0-3a, run ab184d07): the mutation does the
	// rebase (AlterAfterRebase alters the hunk as in the synchronous path),
	// answers the head as it was, and the branch moves on a later pr view
	// (after AsyncViews[n] views, default 1), where PushAfterRebase lands a
	// competing commit on the rebased head at once. AsyncRejected are PR
	// numbers whose update GitHub rejected asynchronously: the mutation
	// answers the head as it was and the branch never moves.
	AsyncUpdate   []string                  `json:"async_update,omitempty"`
	AsyncViews    map[string]int            `json:"async_views,omitempty"`
	AsyncRejected []string                  `json:"async_rejected,omitempty"`
	Pending       map[string]*pendingUpdate `json:"pending,omitempty"`
	// AuditPath, when set, is the train's audit log; the mutation records
	// the entry kinds present the moment it is called (AuditAtMutation), so
	// a row can assert the request was journaled before the mutation.
	AuditPath       string   `json:"audit_path,omitempty"`
	AuditAtMutation []string `json:"audit_at_mutation,omitempty"`
	// OldGH is a gh older than --allow-escape-sequences (brief
	// 320-train-escape-legacy, E3): any call carrying the flag answers
	// "unknown flag", and a response holding escape bytes is printed
	// unrefused.
	OldGH bool `json:"old_gh,omitempty"`
}

// pendingUpdate is an asynchronous update the fake server has performed
// and not yet published: it lands on the branch after Views more pr views.
type pendingUpdate struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Views  int    `json:"views"`
}

// forcePushAt is the time the fake's timeline reports for a force push.
func forcePushAt() string { return time.Now().UTC().Format(time.RFC3339) }

// timelinePage is GitHub's page size of the force-push timeline query.
const timelinePage = 100

// fixtureHome is the HOME the whole test binary runs under (standing rule
// 18: a test never reads the owner's home): a temporary directory made
// once in TestMain, for the test binary's run and for the fake gh's own run
// when the train calls it.
var fixtureHome string

// isolateGitHome points this process's environment at home for every git
// it starts: the fixture helpers' (gitOut, mustGit, newFixture and any
// fixture a later test adds), production's own (gh.go's git patch-id) and
// the subprocess fake gh's, which inherit it. It is set on the process, not
// per fixture, since t.Setenv panics in the parallel tests that build
// fixtures. XDG_CONFIG_HOME is unset, so git's default ignore and
// attributes files, which GIT_CONFIG_GLOBAL does not cover, are read from
// under home only.
func isolateGitHome(home string) {
	_ = os.Setenv("HOME", home)
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	_ = os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "mergetrain-fixture-home-")
	if err != nil {
		panic(err)
	}
	fixtureHome = home
	isolateGitHome(home)
	code := 0
	if path := os.Getenv("FAKE_GH_STATE"); path != "" {
		code = fakeGH(path, os.Args[1:])
	} else {
		code = m.Run()
	}
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func loadFake(path string) *fakeState {
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var s fakeState
	if err := json.Unmarshal(raw, &s); err != nil {
		panic(err)
	}
	return &s
}

func (s *fakeState) save(path string) {
	raw, _ := json.MarshalIndent(s, "", " ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		panic(err)
	}
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// HOME is the test binary's own (isolateGitHome, TestMain)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=owner@example.com",
		"GIT_COMMITTER_NAME=owner", "GIT_COMMITTER_EMAIL=owner@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gitBytes runs fixture git without trimming its output bytes.
func gitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=owner@example.com",
		"GIT_COMMITTER_NAME=owner", "GIT_COMMITTER_EMAIL=owner@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	return cmd.Output()
}

// fakeDiffSection removes one named file section or just its index line.
func fakeDiffSection(diff []byte, path string, whole bool) []byte {
	lines := bytes.SplitAfter(diff, []byte("\n"))
	start, end := -1, len(lines)
	for i, line := range lines {
		if bytes.HasPrefix(line, []byte("diff --git ")) {
			if start >= 0 {
				end = i
				break
			}
			if bytes.HasSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte(" b/"+path)) {
				start = i
			}
		}
	}
	if start < 0 {
		return diff
	}
	if whole {
		return bytes.Join(append(lines[:start:start], lines[end:]...), nil)
	}
	for i := start + 1; i < end; i++ {
		if bytes.HasPrefix(lines[i], []byte("index ")) {
			return bytes.Join(append(lines[:i:i], lines[i+1:]...), nil)
		}
	}
	return diff
}

func fakeGH(statePath string, args []string) int {
	return fakeGHRun(statePath, args, os.Stdout, os.Stderr)
}

// inProcessRunner is GH.runner over the fake: the same answers as the
// subprocess, from a function call.
func inProcessRunner(statePath string) func(context.Context, []string) ([]byte, string, error) {
	return func(_ context.Context, args []string) (out []byte, stderr string, err error) {
		var o, e bytes.Buffer
		defer func() {
			if r := recover(); r != nil { // the subprocess would have crashed with this
				out, stderr, err = o.Bytes(), fmt.Sprint(e.String(), "fake gh panic: ", r), errors.New("exit status 2")
			}
		}()
		if code := fakeGHRun(statePath, args, &o, &e); code != 0 {
			return o.Bytes(), e.String(), fmt.Errorf("exit status %d", code)
		}
		return o.Bytes(), e.String(), nil
	}
}

func fakeGHRun(statePath string, args []string, stdout, stderr io.Writer) int {
	s := loadFake(statePath)
	logf, _ := os.OpenFile(filepath.Join(filepath.Dir(statePath), "calls.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	fmt.Fprintln(logf, strings.Join(args, " "))
	logf.Close()
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, format+"\n", a...)
		return 1
	}
	rev := func(ref string) string { out, _ := gitOut(s.Bare, "rev-parse", "refs/heads/"+ref); return out }
	pr := func(n string) *fakePR {
		if p := s.PRs[n]; p != nil {
			return p
		}
		return nil
	}
	repoPrefix := "repos/" + s.Repo + "/"
	allowEscape := slices.Contains(args, "--allow-escape-sequences")
	if s.OldGH && allowEscape {
		return fail("unknown flag: --allow-escape-sequences")
	}
	switch {
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view" && slices.Contains(args, "id"):
		fmt.Fprintln(stdout, "PR_"+args[2])
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		p := pr(args[2])
		if p == nil {
			return fail("no PR %s", args[2])
		}
		if pend := s.Pending[args[2]]; pend != nil {
			// GitHub's asynchronous update lands between two reads
			pend.Views--
			if pend.Views <= 0 {
				if out, err := gitOut(s.Bare, "update-ref", "refs/heads/"+p.Head, pend.After); err != nil {
					return fail("publish the update: %s", out)
				}
				p.ForcePushes = append(p.ForcePushes, ForcePush{Before: pend.Before, After: pend.After, Actor: "owner", At: forcePushAt()})
				delete(s.Pending, args[2])
				if slices.Contains(s.PushAfterRebase, args[2]) { // a competing push lands on the rebased head before the train reads it back
					work, _ := os.MkdirTemp("", "fakegh-late-")
					defer os.RemoveAll(work)
					gitOut(work, "clone", "-q", s.Bare, "w")
					w := filepath.Join(work, "w")
					gitOut(w, "checkout", "-q", p.Head)
					os.WriteFile(filepath.Join(w, "late.txt"), []byte("late\n"), 0o644)
					gitOut(w, "add", "-A")
					gitOut(w, "commit", "-q", "-m", "late push")
					gitOut(w, "push", "-q", "origin", p.Head)
				}
			}
			s.save(statePath)
		}
		head, base := rev(p.Head), rev(p.Base)
		mergeable := "MERGEABLE"
		if _, err := gitOut(s.Bare, "merge-tree", "--write-tree", base, head); err != nil {
			mergeable = "CONFLICTING"
		}
		n, _ := strconv.Atoi(args[2])
		view := map[string]any{"number": n, "title": p.Title, "state": p.State, "isDraft": p.Draft, "baseRefName": p.Base,
			"baseRefOid": base, "headRefOid": head, "mergeable": mergeable, "mergeCommit": nil}
		if p.MergeCommit != "" {
			view["mergeCommit"] = map[string]string{"oid": p.MergeCommit}
		}
		// gh prints only the fields --json names (brief 3.1.1-1: the title
		// reaches the train only when it asks for it)
		if i := slices.Index(args, "--json"); i >= 0 && i+1 < len(args) {
			asked := strings.Split(args[i+1], ",")
			for k := range view {
				if !slices.Contains(asked, k) {
					delete(view, k)
				}
			}
		}
		out, _ := json.Marshal(view)
		fmt.Fprintln(stdout, string(out))
	case len(args) >= 2 && args[0] == "pr" && args[1] == "ready":
		pr(args[2]).Draft = false
		s.save(statePath)
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql" && slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "updatePullRequestBranch") }):
		n, expect := "", ""
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "id=PR_"); ok {
				n = v
			}
			if v, ok := strings.CutPrefix(a, "oid="); ok {
				expect = v
			}
		}
		p := pr(n)
		before := rev(p.Head)
		if before != expect {
			// the fake's sentinel line, then GitHub's exact message as `gh api
			// graphql` prints it (gh: <message>), the last line
			return fail("fake: expected head %s, the head is %s\ngh: Head branch was modified. Review and try the update again.", expect, before)
		}
		if s.AuditPath != "" {
			s.AuditAtMutation = nil
			for _, e := range func() []Entry { es, _ := Audit{Path: s.AuditPath}.Read(); return es }() {
				s.AuditAtMutation = append(s.AuditAtMutation, e.Kind)
			}
		}
		work, _ := os.MkdirTemp("", "fakegh-rebase-")
		defer os.RemoveAll(work)
		if out, err := gitOut(work, "clone", "-q", s.Bare, "w"); err != nil {
			return fail("clone: %s", out)
		}
		w := filepath.Join(work, "w")
		if slices.Contains(s.AdvanceBaseBeforeRebase, n) {
			gitOut(w, "checkout", "-q", p.Base)
			os.WriteFile(filepath.Join(w, "base-after-pin.txt"), []byte("advanced\n"), 0o644)
			gitOut(w, "add", "-A")
			gitOut(w, "commit", "-q", "-m", "base after pin")
			gitOut(w, "push", "-q", "origin", p.Base)
		}
		if out, err := gitOut(w, "checkout", "-q", p.Head); err != nil {
			return fail("checkout: %s", out)
		}
		if slices.Contains(s.AsyncRejected, n) {
			// GitHub rejected the update asynchronously: the head is unmoved
			s.save(statePath)
			fmt.Fprintln(stdout, before)
			break
		}
		if out, err := gitOut(w, "rebase", "-q", "origin/"+p.Base); err != nil {
			return fail("rebase conflict: %s", out)
		}
		if slices.Contains(s.AlterAfterRebase, n) {
			os.WriteFile(filepath.Join(w, "server-change.txt"), []byte("changed during update\n"), 0o644)
			gitOut(w, "add", "-A")
			gitOut(w, "commit", "-q", "--amend", "--no-edit")
		}
		if slices.Contains(s.AsyncUpdate, n) {
			// the update is performed and published later, on a pr view; the
			// mutation answers the head as it was (run ab184d07)
			if out, err := gitOut(w, "push", "-q", "--force", "origin", "HEAD:refs/pending/"+p.Head); err != nil {
				return fail("push: %s", out)
			}
			after, _ := gitOut(s.Bare, "rev-parse", "refs/pending/"+p.Head)
			views := s.AsyncViews[n]
			if views <= 0 {
				views = 1
			}
			if s.Pending == nil {
				s.Pending = map[string]*pendingUpdate{}
			}
			s.Pending[n] = &pendingUpdate{Before: before, After: after, Views: views}
			s.save(statePath)
			fmt.Fprintln(stdout, before)
			break
		}
		if out, err := gitOut(w, "push", "-q", "--force", "origin", p.Head); err != nil {
			return fail("push: %s", out)
		}
		after := rev(p.Head)
		if after != before {
			p.ForcePushes = append(p.ForcePushes, ForcePush{Before: before, After: after, Actor: "owner", At: forcePushAt()})
		}
		if slices.Contains(s.PushAfterRebase, n) { // a concurrent push right after the server's rebase
			os.WriteFile(filepath.Join(w, "late.txt"), []byte("late\n"), 0o644)
			gitOut(w, "add", "-A")
			gitOut(w, "commit", "-q", "-m", "late push")
			gitOut(w, "push", "-q", "origin", p.Head)
		}
		s.save(statePath)
		fmt.Fprintln(stdout, after)
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql":
		// The force-push timeline, a page of 100 at a time behind a cursor,
		// as GitHub's connection answers it.
		n, from := "", 0
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "n="); ok {
				n = v
			}
			if v, ok := strings.CutPrefix(a, "c="); ok {
				from, _ = strconv.Atoi(strings.TrimPrefix(v, "cursor-"))
			}
		}
		if s.RateLimitPage > 0 && from/timelinePage+1 == s.RateLimitPage {
			return fail("gh: API rate limit exceeded for user ID 1 (HTTP 403)")
		}
		all := pr(n).ForcePushes
		end := min(from+timelinePage, len(all))
		page := map[string]any{"nodes": append([]ForcePush{}, all[min(from, len(all)):end]...), "next": end < len(all), "cursor": fmt.Sprintf("cursor-%d", end)}
		out, _ := json.Marshal(page)
		fmt.Fprintln(stdout, string(out))
	case len(args) >= 3 && args[0] == "api" && args[1] == "-X" && args[2] == "PUT":
		path := strings.TrimPrefix(args[3], repoPrefix)
		n := strings.TrimSuffix(strings.TrimPrefix(path, "pulls/"), "/merge")
		p := pr(n)
		want := ""
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "sha="); ok {
				want = v
			}
		}
		if s.FailMerge {
			return fail("HTTP 405: merge failed")
		}
		if rev(p.Head) != want {
			return fail("HTTP 409: Head branch was modified")
		}
		work, _ := os.MkdirTemp("", "fakegh-merge-")
		defer os.RemoveAll(work)
		gitOut(work, "clone", "-q", s.Bare, "w")
		w := filepath.Join(work, "w")
		gitOut(w, "checkout", "-q", p.Base)
		if out, err := gitOut(w, "merge", "--squash", "origin/"+p.Head); err != nil {
			return fail("HTTP 405: not mergeable: %s", out)
		}
		if out, err := gitOut(w, "commit", "-q", "-m", "squash #"+n); err != nil {
			return fail("commit: %s", out)
		}
		if out, err := gitOut(w, "push", "-q", "origin", p.Base); err != nil {
			return fail("push: %s", out)
		}
		p.State, p.MergeCommit = "MERGED", rev(p.Base)
		s.save(statePath)
		if s.LoseMergeResponse {
			return fail("gh: connection reset by peer")
		}
		fmt.Fprintln(stdout, rev(p.Base))
	case len(args) >= 2 && args[0] == "api":
		rest := slices.DeleteFunc(slices.Clone(args[1:]), func(a string) bool { return a == "--paginate" })
		path := strings.TrimPrefix(rest[0], repoPrefix)
		path, _, _ = strings.Cut(path, "?")
		parts := strings.Split(path, "/")
		switch {
		case parts[0] == "compare" && len(parts) == 2:
			base, head, _ := strings.Cut(parts[1], "...")
			if slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "missing_patch") }) {
				for _, n := range s.RejectCompareAfterRebase {
					pushes := pr(n).ForcePushes
					if len(pushes) > 0 && pushes[len(pushes)-1].After == head {
						return fail("gh: compare result unavailable (HTTP 422)")
					}
				}
				numstat, _ := gitOut(s.Bare, "diff", "--numstat", "-M", base+"..."+head)
				fileCount, missing := 0, 0
				for _, line := range strings.Split(numstat, "\n") {
					if line == "" {
						continue
					}
					fileCount++
					fields := strings.Split(line, "\t")
					path := fields[len(fields)-1]
					if fields[0] == "-" || fields[1] == "-" || slices.Contains(s.NoPatchFiles, path) {
						missing++
					}
				}
				for _, n := range s.CapCompareAfterRebase {
					pushes := pr(n).ForcePushes
					if len(pushes) > 0 && pushes[len(pushes)-1].After == head {
						fileCount = MaxCompareFiles
					}
				}
				fmt.Fprintf(stdout, `{"file_count":%d,"missing_patch":%d}`+"\n", fileCount, missing)
				break
			}
			if slices.Contains(args, "Accept: application/vnd.github.diff") {
				diff, _ := gitBytes(s.Bare, "diff", "--binary", base+"..."+head)
				for _, path := range s.OmitDiffFiles {
					diff = fakeDiffSection(diff, path, true)
				}
				for _, path := range s.OmitDiffIndex {
					diff = fakeDiffSection(diff, path, false)
				}
				for _, n := range s.TruncateAfterRebase {
					pushes := pr(n).ForcePushes
					if len(pushes) > 0 && pushes[len(pushes)-1].After == head {
						trimmed := bytes.TrimSuffix(diff, []byte("\n"))
						if cut := bytes.LastIndexByte(trimmed, '\n'); cut >= 0 {
							diff = trimmed[:cut+1]
						}
					}
				}
				// gh 2.100.0, measured by the lead on #948's diff: a
				// response holding terminal escape bytes is refused, also
				// when captured (exit 1, no bytes, these words), unless the
				// flag is passed (brief 320-train-escape-legacy, E1)
				if bytes.Contains(diff, []byte("\x1b")) && !s.OldGH && !allowEscape {
					return fail("the response contains terminal escape sequences; pass --allow-escape-sequences to output it anyway")
				}
				// The API diff is served byte for byte: trimming loses a final
				// blank context line and makes a complete hunk look cut short.
				_, _ = stdout.Write(diff)
				break
			}
			if slices.Contains(args, ".behind_by") {
				behind, _ := gitOut(s.Bare, "rev-list", "--count", head+".."+base)
				fmt.Fprintln(stdout, behind)
				break
			}
			if slices.Contains(args, compareProofFilesJQ) {
				capped := false
				for _, n := range s.RejectCompareAfterRebase {
					pushes := pr(n).ForcePushes
					if len(pushes) > 0 && pushes[len(pushes)-1].After == head {
						return fail("gh: compare result unavailable (HTTP 422)")
					}
				}
				for _, n := range s.CapCompareAfterRebase {
					pushes := pr(n).ForcePushes
					if len(pushes) > 0 && pushes[len(pushes)-1].After == head {
						for i := 0; i < MaxCompareFiles; i++ {
							out, _ := json.Marshal(FileChange{Path: fmt.Sprintf("capped/%03d.txt", i)})
							fmt.Fprintln(stdout, string(out))
						}
						capped = true
						break
					}
				}
				if capped {
					break
				}
			}
			numstat, _ := gitOut(s.Bare, "diff", "--numstat", "-M", base+"..."+head)
			status, _ := gitOut(s.Bare, "diff", "--name-status", "-M", base+"..."+head)
			st := strings.Split(status, "\n")
			for i, line := range strings.Split(numstat, "\n") {
				if line == "" {
					continue
				}
				f := strings.Split(line, "\t")
				add, _ := strconv.Atoi(f[0])
				del, _ := strconv.Atoi(f[1])
				fc := FileChange{Path: f[2], Additions: add, Deletions: del, Status: "modified"}
				if ns := strings.Split(st[i], "\t"); strings.HasPrefix(ns[0], "R") {
					fc.PreviousPath, fc.Path, fc.Status = ns[1], ns[2], "renamed"
				} else if ns[0] == "A" {
					fc.Status = "added"
				}
				if slices.Contains(s.ExtraAdditionFiles, fc.Path) {
					fc.Additions++
				}
				out, _ := json.Marshal(fc)
				fmt.Fprintln(stdout, string(out))
			}
		case parts[0] == "commits" && len(parts) == 3 && parts[2] == "check-runs":
			conclusion := "success"
			if slices.Contains(s.NoCI, parts[1]) {
				return 0
			}
			if slices.Contains(s.RedCI, parts[1]) {
				conclusion = "failure"
			}
			fmt.Fprintf(stdout, `{"id":100,"name":%q,"app_id":%d,"status":"completed","conclusion":%q}`+"\n", s.CIName, s.CIApp, conclusion)
			for _, r := range s.ExtraRuns[parts[1]] {
				out, _ := json.Marshal(r)
				fmt.Fprintln(stdout, string(out))
			}
		case parts[0] == "commits" && len(parts) == 3 && parts[2] == "status":
		case parts[0] == "commits" && len(parts) == 2:
			// the commit's first parent, as .parents[0].sha // "" prints it
			parent, err := gitOut(s.Bare, "rev-parse", "--verify", "-q", parts[1]+"^")
			if err != nil {
				parent = ""
			}
			fmt.Fprintln(stdout, parent)
		case parts[0] == "branches" && len(parts) == 2:
			fmt.Fprintln(stdout, rev(parts[1]))
		case parts[0] == "branches" && len(parts) == 3 && parts[2] == "protection":
			if len(s.Classic) == 0 {
				return fail("gh: Branch not protected (HTTP 404)")
			}
			fmt.Fprintln(stdout, string(s.Classic))
		case parts[0] == "rules":
			fmt.Fprintln(stdout, string(s.Rules))
		case parts[0] == "tags":
			refs, _ := gitOut(s.Bare, "for-each-ref", "refs/tags", "--format=%(refname:short) %(objectname)")
			for _, l := range strings.Split(refs, "\n") {
				if f := strings.Fields(l); len(f) == 2 {
					fmt.Fprintf(stdout, `{"name":%q,"sha":%q}`+"\n", f[0], f[1])
				}
			}
		case parts[0] == "releases":
			for _, r := range s.Releases {
				out, _ := json.Marshal(r)
				fmt.Fprintln(stdout, string(out))
			}
		case parts[0] == "rulesets":
			fmt.Fprintln(stdout, "0")
		default:
			return fail("fake gh: unknown api path %s", path)
		}
	default:
		return fail("fake gh: unknown command %v", args)
	}
	return 0
}
