package commandevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const subjectRecovery = "invoke git, make, or go directly with one deterministic -C target"

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var errNotGitRepository = errors.New("not a Git repository/worktree")

var runSubjectGit = func(subject string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", subject}, args...)...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

// CommandSubject is the physical repository/worktree operated on by a command.
// ControlCWD remains the task/evidence store checkout and may differ.
type CommandSubject struct {
	Mode         string `json:"mode"`
	ControlCWD   string `json:"control_cwd"`
	SubjectCWD   string `json:"subject_cwd"`
	Git          bool   `json:"git"`
	GitTopLevel  string `json:"git_top_level,omitempty"`
	GitCommonDir string `json:"git_common_dir,omitempty"`
	GitHead      string `json:"git_head,omitempty"`
	GitTree      string `json:"git_tree,omitempty"`
	Dirty        bool   `json:"dirty,omitempty"`
	StatusSHA256 string `json:"status_sha256,omitempty"`
}

// ResolveCommandSubject parses the direct command (or the narrow, deterministic
// /usr/bin/env COMMAND form), resolves supported -C semantics, and snapshots
// the actual physical repository/worktree before execution.
func ResolveCommandSubject(controlCWD string, argv []string) (CommandSubject, error) {
	control, err := canonicalExistingDir(controlCWD)
	if err != nil {
		return CommandSubject{}, fmt.Errorf("resolve control cwd: %w", err)
	}
	command, args, wrapped, err := effectiveSubjectCommand(argv)
	if err != nil {
		return CommandSubject{}, err
	}
	name := strings.ToLower(filepath.Base(command))
	target, mode, explicit, err := subjectTarget(control, name, args)
	if err != nil {
		return CommandSubject{}, err
	}
	if wrapped {
		mode = "env:" + mode
	}
	subject, err := canonicalExistingDir(target)
	if err != nil {
		return CommandSubject{}, fmt.Errorf("resolve command subject: %w; recovery: %s", err, subjectRecovery)
	}
	result, err := snapshotCommandSubject(control, subject, mode)
	if err != nil {
		return CommandSubject{}, err
	}
	if explicit && !result.Git {
		return CommandSubject{}, fmt.Errorf("explicit %s target is not a Git repository/worktree; recovery: %s", mode, subjectRecovery)
	}
	controlGit, controlErr := snapshotGit(control)
	if explicit && controlErr == nil && result.Git && controlGit.GitCommonDir != result.GitCommonDir {
		return CommandSubject{}, fmt.Errorf("command subject repository differs from the task control repository; recovery: use a linked worktree of %s", controlGit.GitTopLevel)
	}
	if explicit && controlErr != nil && !errors.Is(controlErr, errNotGitRepository) {
		return CommandSubject{}, fmt.Errorf("snapshot task control Git identity: %w", controlErr)
	}
	if explicit && errors.Is(controlErr, errNotGitRepository) && result.Git {
		return CommandSubject{}, fmt.Errorf("task control cwd has no Git identity while alternate subject does; recovery: run from the task repository")
	}
	return result, nil
}

// VerifyCommandSubject re-resolves the exact command and rejects any snapshot
// drift before an invocation or evidence link is accepted.
func VerifyCommandSubject(expected CommandSubject, argv []string) error {
	current, err := ResolveCommandSubject(expected.ControlCWD, argv)
	if err != nil {
		return err
	}
	return compareCommandSubject(expected, current)
}

// VerifySubjectSnapshot checks an already-recorded physical subject without
// needing to reconstruct possibly redacted argv. Task-link and lifecycle
// verification use it to detect mutation after the original receipt.
func VerifySubjectSnapshot(expected CommandSubject) error {
	if err := validateCommandSubject(expected); err != nil {
		return err
	}
	current, err := snapshotCommandSubject(expected.ControlCWD, expected.SubjectCWD, expected.Mode)
	if err != nil {
		return err
	}
	return compareCommandSubject(expected, current)
}

func compareCommandSubject(expected, current CommandSubject) error {
	if current == expected {
		return nil
	}
	return fmt.Errorf("command subject changed after receipt: expected head=%s tree=%s dirty=%t status=%s at %s; observed head=%s tree=%s dirty=%t status=%s at %s; recovery: create a new evidence attempt for the current exact snapshot",
		expected.GitHead, expected.GitTree, expected.Dirty, expected.StatusSHA256, expected.SubjectCWD,
		current.GitHead, current.GitTree, current.Dirty, current.StatusSHA256, current.SubjectCWD)
}

func validateCommandSubject(subject CommandSubject) error {
	for label, value := range map[string]string{"control cwd": subject.ControlCWD, "subject cwd": subject.SubjectCWD} {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("command subject %s is not canonical absolute", label)
		}
	}
	if strings.TrimSpace(subject.Mode) == "" {
		return fmt.Errorf("command subject mode is missing")
	}
	if !subject.Git {
		if subject.GitTopLevel != "" || subject.GitCommonDir != "" || subject.GitHead != "" || subject.GitTree != "" || subject.Dirty || subject.StatusSHA256 != "" {
			return fmt.Errorf("non-Git command subject carries Git identity")
		}
		return nil
	}
	if !filepath.IsAbs(subject.GitTopLevel) || filepath.Clean(subject.GitTopLevel) != subject.GitTopLevel || !filepath.IsAbs(subject.GitCommonDir) || filepath.Clean(subject.GitCommonDir) != subject.GitCommonDir ||
		!validGitHead(subject.GitHead) || !validGitHead(subject.GitTree) || !validSHA256(subject.StatusSHA256) {
		return fmt.Errorf("Git command subject identity is incomplete or non-canonical")
	}
	return nil
}

func effectiveSubjectCommand(argv []string) (command string, args []string, wrapped bool, err error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return "", nil, false, fmt.Errorf("command subject requires argv")
	}
	command, args = argv[0], argv[1:]
	if strings.EqualFold(filepath.Base(command), "env") {
		// Accept only env's deterministic assignment prefix. Options can mutate
		// the execution cwd, split a string into another argv, or otherwise make
		// the effective command platform-dependent, so the sole accepted option
		// is a leading -- terminator.
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		for len(args) > 0 && strings.Contains(args[0], "=") {
			name, _, _ := strings.Cut(args[0], "=")
			if !environmentNamePattern.MatchString(name) {
				return "", nil, false, fmt.Errorf("ambiguous env wrapper cannot be bound to one command subject; recovery: %s", subjectRecovery)
			}
			args = args[1:]
		}
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return "", nil, false, fmt.Errorf("ambiguous env wrapper cannot be bound to one command subject; recovery: %s", subjectRecovery)
		}
		command, args, wrapped = args[0], args[1:], true
	}
	switch strings.ToLower(filepath.Base(command)) {
	case "git", "make", "gmake", "go":
		return command, args, wrapped, nil
	default:
		return "", nil, false, fmt.Errorf("unsupported command subject executable %s; invoke git, make, or go directly; recovery: %s", filepath.Base(command), subjectRecovery)
	}
}

func subjectTarget(control, name string, args []string) (string, string, bool, error) {
	switch name {
	case "git":
		return parseGitC(control, args)
	case "make", "gmake":
		return parseSequentialC(control, args, "make", func(arg string) (string, bool) {
			switch {
			case strings.HasPrefix(arg, "-C") && arg != "-C":
				return strings.TrimPrefix(arg, "-C"), true
			case strings.HasPrefix(arg, "--directory="):
				return strings.TrimPrefix(arg, "--directory="), true
			default:
				return "", false
			}
		}, nil)
	case "go":
		return parseGoC(control, args)
	default:
		return control, "execution-cwd", false, nil
	}
}

func parseGitC(control string, args []string) (string, string, bool, error) {
	target, found := control, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			break
		}
		switch {
		case arg == "-C":
			if i+1 >= len(args) {
				return "", "git-C", false, fmt.Errorf("git command has missing -C target; recovery: %s", subjectRecovery)
			}
			i++
			value := args[i]
			if strings.TrimSpace(value) == "" {
				return "", "git-C", false, fmt.Errorf("git command has empty -C target; recovery: %s", subjectRecovery)
			}
			target, found = resolveTarget(target, value), true
		case strings.HasPrefix(arg, "-C"):
			return "", "git-C", false, fmt.Errorf("git command uses unsupported glued -C selector %s; use two-token -C DIR; recovery: %s", arg, subjectRecovery)
		case arg == "--git-dir" || strings.HasPrefix(arg, "--git-dir=") || arg == "--work-tree" || strings.HasPrefix(arg, "--work-tree="):
			return "", "git-C", false, fmt.Errorf("git command uses conflicting repository selector %s; recovery: %s", arg, subjectRecovery)
		case gitGlobalOptionConsumesValue(arg):
			if i+1 >= len(args) {
				return "", "git-C", false, fmt.Errorf("git global option %s is missing its value; recovery: %s", arg, subjectRecovery)
			}
			i++
		}
	}
	return target, "git-C", found, nil
}

func gitGlobalOptionConsumesValue(arg string) bool {
	switch arg {
	case "-c", "--config-env", "--namespace", "--super-prefix":
		return true
	default:
		return false
	}
}

func parseGoC(control string, args []string) (string, string, bool, error) {
	index := goChdirIndex(args)
	if index < 0 {
		return control, "go-C", false, nil
	}
	arg := args[index]
	var value string
	switch {
	case arg == "-C" || arg == "--C":
		if index+1 >= len(args) {
			return "", "go-C", false, fmt.Errorf("go command has missing -C target; recovery: %s", subjectRecovery)
		}
		value = args[index+1]
	case strings.HasPrefix(arg, "-C=") || strings.HasPrefix(arg, "--C="):
		_, value, _ = strings.Cut(arg, "=")
	default:
		return "", "go-C", false, fmt.Errorf("go command uses unsupported glued -C selector %s; recovery: %s", arg, subjectRecovery)
	}
	if strings.TrimSpace(value) == "" {
		return "", "go-C", false, fmt.Errorf("go command has empty -C target; recovery: %s", subjectRecovery)
	}
	return resolveTarget(control, value), "go-C", true, nil
}

func goChdirIndex(args []string) int {
	if len(args) == 0 {
		return -1
	}
	if isGoChdirToken(args[0]) {
		return 0
	}
	commandWords := 1
	if len(args) > 1 {
		subcommand := args[1]
		switch args[0] {
		case "mod":
			if stringInSet(subcommand, "download", "edit", "graph", "init", "tidy", "vendor", "verify", "why") {
				commandWords = 2
			}
		case "work":
			if stringInSet(subcommand, "edit", "init", "sync", "use", "vendor") {
				commandWords = 2
			}
		}
	}
	if commandWords < len(args) && isGoChdirToken(args[commandWords]) {
		return commandWords
	}
	return -1
}

func stringInSet(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func isGoChdirToken(arg string) bool {
	return arg == "-C" || arg == "--C" || strings.HasPrefix(arg, "-C=") || strings.HasPrefix(arg, "--C=") ||
		strings.HasPrefix(arg, "-C") || strings.HasPrefix(arg, "--C")
}

func parseSequentialC(control string, args []string, name string, joined func(string) (string, bool), conflicting []string) (string, string, bool, error) {
	target, found := control, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		for _, prefix := range conflicting {
			if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
				return "", name + "-C", false, fmt.Errorf("%s command uses conflicting repository selector %s; recovery: %s", name, arg, subjectRecovery)
			}
		}
		var value string
		if arg == "-C" || name == "make" && arg == "--directory" {
			if i+1 >= len(args) {
				return "", name + "-C", false, fmt.Errorf("%s command has missing -C target; recovery: %s", name, subjectRecovery)
			}
			i++
			value = args[i]
		} else if v, ok := joined(arg); ok {
			value = v
		} else {
			continue
		}
		if strings.TrimSpace(value) == "" {
			return "", name + "-C", false, fmt.Errorf("%s command has empty -C target; recovery: %s", name, subjectRecovery)
		}
		target, found = resolveTarget(target, value), true
	}
	return target, name + "-C", found, nil
}

func resolveTarget(base, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Join(base, target)
}

func canonicalExistingDir(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}

func snapshotCommandSubject(control, subject, mode string) (CommandSubject, error) {
	git, err := snapshotGit(subject)
	if err != nil {
		if errors.Is(err, errNotGitRepository) {
			return CommandSubject{Mode: mode, ControlCWD: control, SubjectCWD: subject}, nil
		}
		return CommandSubject{}, fmt.Errorf("snapshot command subject Git identity: %w", err)
	}
	git.Mode, git.ControlCWD, git.SubjectCWD = mode, control, subject
	return git, nil
}

func snapshotGit(subject string) (CommandSubject, error) {
	top, err := runSubjectGit(subject, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
			return CommandSubject{}, errNotGitRepository
		}
		return CommandSubject{}, err
	}
	top, err = canonicalExistingDir(top)
	if err != nil {
		return CommandSubject{}, err
	}
	common, err := runSubjectGit(subject, "rev-parse", "--git-common-dir")
	if err != nil {
		return CommandSubject{}, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(subject, common)
	}
	common, err = filepath.Abs(filepath.Clean(common))
	if err != nil {
		return CommandSubject{}, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return CommandSubject{}, err
	}
	head, err := runSubjectGit(subject, "rev-parse", "HEAD")
	if err != nil {
		return CommandSubject{}, err
	}
	tree, err := runSubjectGit(subject, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return CommandSubject{}, err
	}
	status, err := runSubjectGit(subject, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return CommandSubject{}, err
	}
	sum := sha256.Sum256([]byte(status))
	return CommandSubject{Git: true, GitTopLevel: top, GitCommonDir: filepath.Clean(common), GitHead: head, GitTree: tree,
		Dirty: status != "", StatusSHA256: "sha256:" + hex.EncodeToString(sum[:])}, nil
}
