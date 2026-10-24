package mergetrain

// A fake gh over a local bare repository. The test binary is the fake: with
// FAKE_GH_STATE set, TestMain runs fakeGH(os.Args[1:]) instead of the tests.
// It answers exactly the gh command lines GH runs, in the shape GH's --jq
// filters produce, from real git objects: diffs, merge-tree conflicts,
// squash merges and rebases happen in the bare repository.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type fakePR struct {
	Head        string      `json:"head"` // branch
	Base        string      `json:"base"`
	Draft       bool        `json:"draft"`
	State       string      `json:"state"`
	ForcePushes []ForcePush `json:"force_pushes,omitempty"`
}

type fakeState struct {
	Bare      string             `json:"bare"`
	Repo      string             `json:"repo"`
	Rules     json.RawMessage    `json:"rules"` // GET rules/branches/main
	Classic   json.RawMessage    `json:"classic,omitempty"`
	PRs       map[string]*fakePR `json:"prs"`
	RedCI     []string           `json:"red_ci,omitempty"` // head SHAs whose ci check fails
	NoCI      []string           `json:"no_ci,omitempty"`  // head SHAs where no check ran
	Releases  []string           `json:"releases,omitempty"`
	CIName    string             `json:"ci_name"`
	FailMerge bool               `json:"fail_merge,omitempty"`
}

func TestMain(m *testing.M) {
	if path := os.Getenv("FAKE_GH_STATE"); path != "" {
		os.Exit(fakeGH(path, os.Args[1:]))
	}
	os.Exit(m.Run())
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
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=owner@example.com",
		"GIT_COMMITTER_NAME=owner", "GIT_COMMITTER_EMAIL=owner@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func fakeGH(statePath string, args []string) int {
	s := loadFake(statePath)
	logf, _ := os.OpenFile(filepath.Join(filepath.Dir(statePath), "calls.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	fmt.Fprintln(logf, strings.Join(args, " "))
	logf.Close()
	fail := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
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
	switch {
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		p := pr(args[2])
		if p == nil {
			return fail("no PR %s", args[2])
		}
		head, base := rev(p.Head), rev(p.Base)
		mergeable := "MERGEABLE"
		if _, err := gitOut(s.Bare, "merge-tree", "--write-tree", base, head); err != nil {
			mergeable = "CONFLICTING"
		}
		n, _ := strconv.Atoi(args[2])
		mb, _ := gitOut(s.Bare, "merge-base", base, head)
		out, _ := json.Marshal(map[string]any{"number": n, "state": p.State, "isDraft": p.Draft, "baseRefName": p.Base,
			"baseRefOid": mb, "headRefOid": head, "mergeable": mergeable})
		fmt.Println(string(out))
	case len(args) >= 2 && args[0] == "pr" && args[1] == "ready":
		pr(args[2]).Draft = false
		s.save(statePath)
	case len(args) >= 2 && args[0] == "pr" && args[1] == "update-branch":
		p := pr(args[2])
		before := rev(p.Head)
		work, _ := os.MkdirTemp("", "fakegh-rebase-")
		defer os.RemoveAll(work)
		if out, err := gitOut(work, "clone", "-q", s.Bare, "w"); err != nil {
			return fail("clone: %s", out)
		}
		w := filepath.Join(work, "w")
		if out, err := gitOut(w, "checkout", "-q", p.Head); err != nil {
			return fail("checkout: %s", out)
		}
		if out, err := gitOut(w, "rebase", "-q", "origin/"+p.Base); err != nil {
			return fail("rebase conflict: %s", out)
		}
		if out, err := gitOut(w, "push", "-q", "--force", "origin", p.Head); err != nil {
			return fail("push: %s", out)
		}
		if after := rev(p.Head); after != before {
			p.ForcePushes = append(p.ForcePushes, ForcePush{Before: before, After: after, Actor: "owner"})
		}
		s.save(statePath)
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql":
		n := ""
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "n="); ok {
				n = v
			}
		}
		for _, fp := range pr(n).ForcePushes {
			out, _ := json.Marshal(fp)
			fmt.Println(string(out))
		}
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
		p.State = "MERGED"
		s.save(statePath)
		fmt.Println(rev(p.Base))
	case len(args) >= 2 && args[0] == "api":
		rest := slices.DeleteFunc(slices.Clone(args[1:]), func(a string) bool { return a == "--paginate" })
		path := strings.TrimPrefix(rest[0], repoPrefix)
		path, _, _ = strings.Cut(path, "?")
		parts := strings.Split(path, "/")
		switch {
		case parts[0] == "pulls" && len(parts) == 3 && parts[2] == "files":
			p := pr(parts[1])
			base, head := rev(p.Base), rev(p.Head)
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
				out, _ := json.Marshal(fc)
				fmt.Println(string(out))
			}
		case parts[0] == "commits" && len(parts) == 3 && parts[2] == "check-runs":
			conclusion := "success"
			if slices.Contains(s.NoCI, parts[1]) {
				return 0
			}
			if slices.Contains(s.RedCI, parts[1]) {
				conclusion = "failure"
			}
			fmt.Printf(`{"name":%q,"status":"completed","conclusion":%q}`+"\n", s.CIName, conclusion)
		case parts[0] == "commits" && len(parts) == 3 && parts[2] == "status":
		case parts[0] == "branches" && len(parts) == 2:
			fmt.Println(rev(parts[1]))
		case parts[0] == "branches" && len(parts) == 3 && parts[2] == "protection":
			if len(s.Classic) == 0 {
				return fail("gh: Branch not protected (HTTP 404)")
			}
			fmt.Println(string(s.Classic))
		case parts[0] == "rules":
			fmt.Println(string(s.Rules))
		case parts[0] == "tags":
			refs, _ := gitOut(s.Bare, "for-each-ref", "refs/tags", "--format=%(refname:short) %(objectname)")
			for _, l := range strings.Split(refs, "\n") {
				if f := strings.Fields(l); len(f) == 2 {
					fmt.Printf(`{"name":%q,"sha":%q}`+"\n", f[0], f[1])
				}
			}
		case parts[0] == "releases":
			for _, r := range s.Releases {
				fmt.Println(r)
			}
		case parts[0] == "rulesets":
			fmt.Println("0")
		default:
			return fail("fake gh: unknown api path %s", path)
		}
	default:
		return fail("fake gh: unknown command %v", args)
	}
	return 0
}
