package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	reviewWorktreeManifestName = ".amq-squad-review.json"
	reviewWorktreeTempPrefix   = "amq-squad-review-"
)

type reviewWorktreeManifest struct {
	SchemaVersion   int    `json:"schema_version"`
	Commit          string `json:"commit"`
	Tree            string `json:"tree"`
	CreatedAt       string `json:"created_at"`
	GoVersion       string `json:"go_version"`
	AMQSquadVersion string `json:"amq_squad_version"`
	AMQVersion      string `json:"amq_version"`
	Repository      string `json:"repository"`
	Worktree        string `json:"worktree"`
	SourceRef       string `json:"source_ref"`
}

type reviewWorktree struct {
	Path     string
	Manifest reviewWorktreeManifest
}

var reviewWorktreeNow = time.Now

func runReviewWorktree(args []string, version string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printReviewWorktreeUsage()
		return nil
	}
	mode := "create"
	if len(args) > 0 {
		switch args[0] {
		case "create", "exec", "shell", "remove":
			mode = args[0]
			args = args[1:]
		}
	}
	if mode == "remove" {
		return runReviewWorktreeRemove(args)
	}
	return runReviewWorktreeCreate(mode, args, version)
}

func printReviewWorktreeUsage() {
	fmt.Fprint(os.Stderr, `amq-squad review-worktree - isolate an exact commit for trustworthy review evidence

Usage:
  amq-squad review-worktree [create] [--repo DIR] REF
  amq-squad review-worktree exec [--repo DIR] REF -- COMMAND [ARG...]
  amq-squad review-worktree shell [--repo DIR] REF
  amq-squad review-worktree remove PATH

The helper resolves REF once to an exact commit, creates a detached worktree
under the system temporary directory, verifies its commit, tree, and clean
state, then writes .amq-squad-review.json inside it. The manifest records the
commit, tree, UTC creation time, Go version, amq-squad version, and AMQ version.

exec and shell clear all AM_*, AMQ_SQUAD_*, and TMUX* variables before starting
the reviewer process, including AM_ROOT, AM_BASE_ROOT, AM_ME, AM_SESSION,
AM_WAKE_FD, TMUX, and TMUX_PANE. The worktree is deliberately kept after the
process exits so its manifest and evidence remain inspectable. Use the printed
remove command when review is complete; cleanup is performed only by
git worktree remove --force, never rm -rf.

Options:
  --repo DIR   Git repository containing REF (default: current directory)

Examples:
  amq-squad review-worktree HEAD
  amq-squad review-worktree exec --repo ~/Code/app abc123 -- go test ./...
  amq-squad review-worktree shell v2.19.1
  amq-squad review-worktree remove /tmp/amq-squad-review-123456
`)
}

func runReviewWorktreeCreate(mode string, args []string, version string) error {
	fs := flag.NewFlagSet("review-worktree "+mode, flag.ContinueOnError)
	repo := fs.String("repo", "", "Git repository containing REF (default: current directory)")
	fs.Usage = printReviewWorktreeUsage
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return usageErrorf("review-worktree %s requires REF", mode)
	}
	ref := rest[0]
	command := rest[1:]
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	switch mode {
	case "create":
		if len(command) != 0 {
			return usageErrorf("review-worktree create takes exactly one REF; use 'exec REF -- COMMAND' to run a command")
		}
	case "exec":
		if len(command) == 0 {
			return usageErrorf("review-worktree exec requires COMMAND after REF (optionally separated by --)")
		}
	case "shell":
		if len(command) != 0 {
			return usageErrorf("review-worktree shell takes exactly one REF")
		}
	default:
		return usageErrorf("unknown review-worktree mode %q", mode)
	}

	wt, err := createReviewWorktree(*repo, ref, version)
	if err != nil {
		return err
	}
	printReviewWorktreeCreated(wt)

	switch mode {
	case "exec":
		if err := runSanitizedReviewProcess(wt.Path, command[0], command[1:]); err != nil {
			return fmt.Errorf("review command failed (worktree kept at %s): %w", wt.Path, err)
		}
	case "shell":
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/sh"
		}
		if err := runSanitizedReviewProcess(wt.Path, shell, nil); err != nil {
			return fmt.Errorf("review shell failed (worktree kept at %s): %w", wt.Path, err)
		}
	}
	return nil
}

func runReviewWorktreeRemove(args []string) error {
	fs := flag.NewFlagSet("review-worktree remove", flag.ContinueOnError)
	fs.Usage = printReviewWorktreeUsage
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErrorf("review-worktree remove requires exactly one PATH")
	}
	path, err := validateReviewWorktreeForRemoval(fs.Arg(0))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(path, reviewWorktreeManifestName))
	if err != nil {
		return fmt.Errorf("read review manifest: %w", err)
	}
	var manifest reviewWorktreeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse review manifest: %w", err)
	}
	if _, err := gitOutput(manifest.Repository, "worktree", "remove", "--force", path); err != nil {
		return fmt.Errorf("remove review worktree: %w", err)
	}
	fmt.Printf("removed review worktree: %s\n", path)
	return nil
}

func createReviewWorktree(repo, ref, version string) (result reviewWorktree, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return result, usageErrorf("review-worktree REF must not be empty")
	}
	if strings.HasPrefix(ref, "-") {
		return result, usageErrorf("review-worktree REF must not begin with '-': %q", ref)
	}
	if strings.TrimSpace(repo) == "" {
		repo = "."
	}
	repository, err := gitOutput(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, fmt.Errorf("resolve repository: %w", err)
	}
	repository = strings.TrimSpace(repository)
	commit, err := gitOutput(repository, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return result, usageErrorf("REF %q does not resolve to a commit: %v", ref, err)
	}
	commit = strings.TrimSpace(commit)
	tree, err := gitOutput(repository, "show", "-s", "--format=%T", commit)
	if err != nil {
		return result, fmt.Errorf("resolve tree for %s: %w", commit, err)
	}
	tree = strings.TrimSpace(tree)

	tempPath, err := os.MkdirTemp("", reviewWorktreeTempPrefix)
	if err != nil {
		return result, fmt.Errorf("allocate review worktree path: %w", err)
	}
	// Git accepts the existing empty mktemp directory. Once registered, every
	// cleanup path below goes exclusively through `git worktree remove --force`.
	if _, err := gitOutput(repository, "worktree", "add", "--detach", tempPath, commit); err != nil {
		return result, fmt.Errorf("create detached review worktree: %w", err)
	}
	added := true
	defer func() {
		if err == nil || !added {
			return
		}
		if _, cleanupErr := gitOutput(repository, "worktree", "remove", "--force", tempPath); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup failed worktree: %w", cleanupErr))
		}
	}()

	worktree, err := gitOutput(tempPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, fmt.Errorf("resolve created worktree: %w", err)
	}
	worktree = strings.TrimSpace(worktree)
	actualCommit, err := gitOutput(worktree, "rev-parse", "HEAD")
	if err != nil {
		return result, fmt.Errorf("verify review commit: %w", err)
	}
	actualTree, err := gitOutput(worktree, "show", "-s", "--format=%T", "HEAD")
	if err != nil {
		return result, fmt.Errorf("verify review tree: %w", err)
	}
	if strings.TrimSpace(actualCommit) != commit || strings.TrimSpace(actualTree) != tree {
		return result, fmt.Errorf("created worktree provenance mismatch: want commit %s tree %s, got commit %s tree %s", commit, tree, strings.TrimSpace(actualCommit), strings.TrimSpace(actualTree))
	}
	status, err := gitOutput(worktree, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return result, fmt.Errorf("verify review worktree state: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return result, fmt.Errorf("created review worktree is unexpectedly dirty; refusing it: %s", strings.TrimSpace(status))
	}

	goVersion, err := toolVersion(worktree, "go", "version")
	if err != nil {
		return result, err
	}
	amqVersion, err := toolVersion(worktree, "amq", "version")
	if err != nil {
		return result, err
	}
	manifest := reviewWorktreeManifest{
		SchemaVersion:   1,
		Commit:          commit,
		Tree:            tree,
		CreatedAt:       reviewWorktreeNow().UTC().Format(time.RFC3339Nano),
		GoVersion:       goVersion,
		AMQSquadVersion: versionOrUnknown(version),
		AMQVersion:      amqVersion,
		Repository:      repository,
		Worktree:        worktree,
		SourceRef:       ref,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode review manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(worktree, reviewWorktreeManifestName), manifestData, 0o644); err != nil {
		return result, fmt.Errorf("write review manifest: %w", err)
	}
	added = false
	return reviewWorktree{Path: worktree, Manifest: manifest}, nil
}

func printReviewWorktreeCreated(wt reviewWorktree) {
	fmt.Printf("review worktree: %s\n", wt.Path)
	fmt.Printf("commit: %s\n", wt.Manifest.Commit)
	fmt.Printf("tree: %s\n", wt.Manifest.Tree)
	fmt.Printf("manifest: %s\n", filepath.Join(wt.Path, reviewWorktreeManifestName))
	fmt.Printf("cleanup: amq-squad review-worktree remove %s\n", shellQuote(wt.Path))
}

func toolVersion(dir, name string, args ...string) (string, error) {
	out, err := commandOutput(dir, sanitizedReviewEnv(os.Environ()), name, args...)
	if err != nil {
		return "", fmt.Errorf("read %s version for review manifest: %w", name, err)
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", fmt.Errorf("read %s version for review manifest: empty output", name)
	}
	return version, nil
}

func runSanitizedReviewProcess(dir, name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = sanitizedReviewEnv(os.Environ())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func sanitizedReviewEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "PWD" || strings.HasPrefix(key, "AM_") || strings.HasPrefix(key, "AMQ_SQUAD_") || strings.HasPrefix(key, "TMUX") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func commandOutput(dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, detail)
		}
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

func gitOutput(repo string, args ...string) (string, error) {
	gitArgs := make([]string, 0, len(args)+2)
	if repo != "" {
		gitArgs = append(gitArgs, "-C", repo)
	}
	gitArgs = append(gitArgs, args...)
	out, err := commandOutput("", sanitizedReviewEnv(os.Environ()), "git", gitArgs...)
	return string(out), err
}

func validateReviewWorktreeForRemoval(rawPath string) (string, error) {
	path, err := filepath.Abs(rawPath)
	if err != nil {
		return "", usageErrorf("invalid review worktree path %q: %v", rawPath, err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", usageErrorf("review worktree path %q is not accessible: %v", rawPath, err)
	}
	tempRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("resolve system temporary directory: %w", err)
	}
	if filepath.Dir(path) != tempRoot || !strings.HasPrefix(filepath.Base(path), reviewWorktreeTempPrefix) {
		return "", usageErrorf("refusing to remove %q: not an amq-squad mktemp review-worktree path", path)
	}
	manifestPath := filepath.Join(path, reviewWorktreeManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", usageErrorf("refusing to remove %q: valid %s is required: %v", path, reviewWorktreeManifestName, err)
	}
	var manifest reviewWorktreeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", usageErrorf("refusing to remove %q: invalid review manifest: %v", path, err)
	}
	if manifest.SchemaVersion != 1 || manifest.Worktree != path || strings.TrimSpace(manifest.Repository) == "" || strings.TrimSpace(manifest.Commit) == "" || strings.TrimSpace(manifest.Tree) == "" {
		return "", usageErrorf("refusing to remove %q: review manifest identity does not match", path)
	}
	repository, err := gitOutput(manifest.Repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("verify manifest repository: %w", err)
	}
	if strings.TrimSpace(repository) != manifest.Repository {
		return "", usageErrorf("refusing to remove %q: manifest repository identity does not match", path)
	}
	listed, err := gitOutput(manifest.Repository, "worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("list repository worktrees: %w", err)
	}
	registered := false
	for _, line := range strings.Split(listed, "\n") {
		if strings.TrimPrefix(line, "worktree ") == path && strings.HasPrefix(line, "worktree ") {
			registered = true
			break
		}
	}
	if !registered {
		return "", usageErrorf("refusing to remove %q: path is not a registered worktree of %s", path, manifest.Repository)
	}
	return path, nil
}
