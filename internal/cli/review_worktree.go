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

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
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

var reviewWorktreeAdd = func(repository, path, commit string) error {
	_, err := gitOutput(repository, "worktree", "add", "--detach", path, commit)
	return err
}

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
If AMQ is absent or its version command fails, creation continues and records
an explicit "unavailable: <reason>" value; Go and amq-squad versions remain
required.

exec and shell clear all AM_*, AMQ_SQUAD_*, TMUX*, and GIT_* variables before
starting the reviewer process. This includes agent identity, tmux identity, and
Git repository/index/object/namespace/replace-ref overrides. GOCACHE, TMPDIR,
PATH, HOME, and other ordinary process settings are retained. Helper-owned Git
commands use the same sanitized environment, so --repo wins over ambient Git
overrides. The worktree is deliberately kept after the process exits so its
manifest and evidence remain inspectable. Use the printed remove command when
review is complete; registered-worktree cleanup is performed only by
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
	path, manifest, err := validateReviewWorktreeForRemoval(fs.Arg(0))
	if err != nil {
		return err
	}
	if _, err := gitOutput(manifest.Repository, "worktree", "remove", "--force", path); err != nil {
		return fmt.Errorf("remove review worktree: %w", err)
	}
	fmt.Printf("removed review worktree: %s\n", path)
	return nil
}

func createReviewWorktree(repo, ref, version string) (result reviewWorktree, err error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return result, fmt.Errorf("running amq-squad version is required for the review manifest")
	}
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
	resolvedTempPath, resolveErr := filepath.EvalSymlinks(tempPath)
	if resolveErr != nil {
		if cleanupErr := os.Remove(tempPath); cleanupErr != nil {
			return result, errors.Join(fmt.Errorf("resolve review worktree path: %w", resolveErr), fmt.Errorf("remove empty unresolved review directory: %w", cleanupErr))
		}
		return result, fmt.Errorf("resolve review worktree path: %w", resolveErr)
	}
	tempPath = resolvedTempPath
	// Git accepts the existing empty mktemp directory. If add fails before it
	// registers or populates the worktree, remove that still-empty placeholder
	// with os.Remove (never recursively). Once registered, every cleanup path
	// below goes exclusively through `git worktree remove --force`.
	if addErr := reviewWorktreeAdd(repository, tempPath, commit); addErr != nil {
		registered, probeErr := gitWorktreeRegistered(repository, tempPath)
		if probeErr != nil {
			return result, errors.Join(fmt.Errorf("create detached review worktree: %w", addErr), fmt.Errorf("determine whether failed worktree add registered: %w", probeErr))
		}
		if registered {
			if _, cleanupErr := gitOutput(repository, "worktree", "remove", "--force", tempPath); cleanupErr != nil {
				return result, errors.Join(fmt.Errorf("create detached review worktree: %w", addErr), fmt.Errorf("cleanup registered failed worktree: %w", cleanupErr))
			}
		} else if cleanupErr := os.Remove(tempPath); cleanupErr != nil {
			return result, errors.Join(fmt.Errorf("create detached review worktree: %w", addErr), fmt.Errorf("remove empty unregistered review directory: %w", cleanupErr))
		}
		return result, fmt.Errorf("create detached review worktree: %w", addErr)
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
	amqVersion := optionalToolVersion(worktree, "amq", "version")
	manifest := reviewWorktreeManifest{
		SchemaVersion:   1,
		Commit:          commit,
		Tree:            tree,
		CreatedAt:       reviewWorktreeNow().UTC().Format(time.RFC3339Nano),
		GoVersion:       goVersion,
		AMQSquadVersion: version,
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
	env := sanitizedReviewEnv(os.Environ())
	if name == "amq" {
		env = amqexec.NoUpdateCheckEnv(env)
	}
	out, err := commandOutput(dir, env, name, args...)
	if err != nil {
		return "", fmt.Errorf("read %s version for review manifest: %w", name, err)
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", fmt.Errorf("read %s version for review manifest: empty output", name)
	}
	return version, nil
}

func optionalToolVersion(dir, name string, args ...string) string {
	version, err := toolVersion(dir, name, args...)
	if err == nil {
		return version
	}
	reason := strings.Join(strings.Fields(err.Error()), " ")
	const maxReasonBytes = 200
	if len(reason) > maxReasonBytes {
		reason = reason[:maxReasonBytes] + "..."
	}
	return "unavailable: " + reason
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
		if key == "PWD" || strings.HasPrefix(key, "AM_") || strings.HasPrefix(key, "AMQ_SQUAD_") || strings.HasPrefix(key, "TMUX") || strings.HasPrefix(key, "GIT_") {
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

func validateReviewWorktreeForRemoval(rawPath string) (string, reviewWorktreeManifest, error) {
	path, err := filepath.Abs(rawPath)
	if err != nil {
		return "", reviewWorktreeManifest{}, usageErrorf("invalid review worktree path %q: %v", rawPath, err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", reviewWorktreeManifest{}, usageErrorf("review worktree path %q is not accessible: %v", rawPath, err)
	}
	tempRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("resolve system temporary directory: %w", err)
	}
	if filepath.Dir(path) != tempRoot || !strings.HasPrefix(filepath.Base(path), reviewWorktreeTempPrefix) {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: not an amq-squad mktemp review-worktree path", path)
	}
	manifestPath := filepath.Join(path, reviewWorktreeManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: valid %s is required: %v", path, reviewWorktreeManifestName, err)
	}
	var manifest reviewWorktreeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: invalid review manifest: %v", path, err)
	}
	if manifest.SchemaVersion != 1 || manifest.Worktree != path || strings.TrimSpace(manifest.Repository) == "" || strings.TrimSpace(manifest.Commit) == "" || strings.TrimSpace(manifest.Tree) == "" {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: review manifest identity does not match", path)
	}
	repository, err := gitOutput(manifest.Repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("verify manifest repository: %w", err)
	}
	if strings.TrimSpace(repository) != manifest.Repository {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: manifest repository identity does not match", path)
	}
	targetRoot, err := gitOutput(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: target is not a readable Git worktree: %v", path, err)
	}
	if strings.TrimSpace(targetRoot) != path {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: target Git worktree root resolves to %q", path, strings.TrimSpace(targetRoot))
	}
	headKind, err := gitOutput(path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("verify review worktree HEAD identity: %w", err)
	}
	if strings.TrimSpace(headKind) != "HEAD" {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: review worktree HEAD is attached to %q; expected detached HEAD", path, strings.TrimSpace(headKind))
	}
	actualCommit, err := gitOutput(path, "rev-parse", "HEAD")
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("verify review worktree commit: %w", err)
	}
	actualCommit = strings.TrimSpace(actualCommit)
	if actualCommit != manifest.Commit {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: actual HEAD commit %s does not match manifest commit %s", path, actualCommit, manifest.Commit)
	}
	actualTree, err := gitOutput(path, "show", "-s", "--format=%T", "HEAD")
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("verify review worktree tree: %w", err)
	}
	actualTree = strings.TrimSpace(actualTree)
	if actualTree != manifest.Tree {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: actual HEAD tree %s does not match manifest tree %s", path, actualTree, manifest.Tree)
	}
	manifestCommon, err := canonicalGitCommonDir(manifest.Repository)
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("resolve manifest common Git directory: %w", err)
	}
	targetCommon, err := canonicalGitCommonDir(path)
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("resolve target common Git directory: %w", err)
	}
	if targetCommon != manifestCommon {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: target common Git directory %q does not match manifest repository %q", path, targetCommon, manifestCommon)
	}
	registered, err := gitWorktreeRegistered(manifest.Repository, path)
	if err != nil {
		return "", reviewWorktreeManifest{}, fmt.Errorf("list repository worktrees: %w", err)
	}
	if !registered {
		return "", reviewWorktreeManifest{}, usageErrorf("refusing to remove %q: path is not a registered worktree of %s", path, manifest.Repository)
	}
	return path, manifest, nil
}

func gitWorktreeRegistered(repository, path string) (bool, error) {
	listed, err := gitOutput(repository, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(listed, "\n") {
		if strings.HasPrefix(line, "worktree ") && strings.TrimPrefix(line, "worktree ") == path {
			return true, nil
		}
	}
	return false, nil
}

func canonicalGitCommonDir(worktree string) (string, error) {
	raw, err := gitOutput(worktree, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(raw)
	if !filepath.IsAbs(path) {
		path = filepath.Join(worktree, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
