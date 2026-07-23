package worktreeplan

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type GitRunner interface {
	Run(dir string, args ...string) (string, error)
}

type ExecGit struct{}

func (ExecGit) Run(dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

type RegisteredWorktree struct {
	Path      string
	HEAD      string
	BranchRef string
	Prunable  bool
}

func listWorktrees(git GitRunner, repo string) ([]RegisteredWorktree, error) {
	out, err := git.Run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	var records []RegisteredWorktree
	var current *RegisteredWorktree
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			if current != nil {
				records = append(records, *current)
				current = nil
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current != nil {
				records = append(records, *current)
			}
			current = &RegisteredWorktree{Path: filepath.Clean(strings.TrimPrefix(line, "worktree "))}
		case current == nil:
			continue
		case strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
		case strings.HasPrefix(line, "branch "):
			current.BranchRef = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		case strings.HasPrefix(line, "prunable"):
			current.Prunable = true
		}
	}
	if current != nil {
		records = append(records, *current)
	}
	return records, nil
}

func resolveCommit(git GitRunner, repo, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("base ref is required")
	}
	out, err := git.Run(repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func gitRoot(git GitRunner, dir string) (string, error) {
	out, err := git.Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	return filepath.Clean(root), nil
}

func branchCommit(git GitRunner, repo, branch string) (string, bool) {
	out, err := git.Run(repo, "rev-parse", "--verify", "--end-of-options", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

func currentBranch(git GitRunner, path string) (string, error) {
	return git.Run(path, "symbolic-ref", "--quiet", "--short", "HEAD")
}

func currentHEAD(git GitRunner, path string) (string, error) {
	return git.Run(path, "rev-parse", "--verify", "HEAD")
}

func worktreeClean(git GitRunner, path string) (bool, error) {
	out, err := git.Run(path, "status", "--porcelain=v1", "--untracked-files=all")
	return strings.TrimSpace(out) == "", err
}

func indexPath(git GitRunner, path string) (string, error) {
	out, err := git.Run(path, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(path, out)
	}
	absolute, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func baseIsAncestor(git GitRunner, path, base, head string) bool {
	out, err := git.Run(path, "merge-base", base, head)
	return err == nil && strings.TrimSpace(out) == strings.TrimSpace(base)
}

func branchRef(branch string) string {
	return "refs/heads/" + branch
}
