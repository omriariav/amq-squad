package commandevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const subjectRecovery = "invoke git, make, or go directly with one deterministic -C target"

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	if explicit && controlErr != nil && result.Git {
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
		return args[0], args[1:], true, nil
	}
	switch strings.ToLower(filepath.Base(command)) {
	case "sh", "bash", "zsh", "fish", "sudo", "xargs", "find":
		return "", nil, false, fmt.Errorf("nested wrapper %s cannot be bound to one command subject; recovery: %s", filepath.Base(command), subjectRecovery)
	}
	return command, args, false, nil
}

func subjectTarget(control, name string, args []string) (string, string, bool, error) {
	switch name {
	case "git":
		return parseSequentialC(control, args, "git", func(arg string) (string, bool) {
			if strings.HasPrefix(arg, "-C") && arg != "-C" {
				return strings.TrimPrefix(arg, "-C"), true
			}
			return "", false
		}, []string{"--git-dir", "--work-tree"})
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
		target, found := control, false
		for i := 0; i < len(args); i++ {
			arg := args[i]
			var value string
			switch {
			case arg == "-C":
				if i+1 >= len(args) || found {
					return "", "go-C", false, fmt.Errorf("go command has missing or duplicate -C target; recovery: %s", subjectRecovery)
				}
				i++
				value = args[i]
			case strings.HasPrefix(arg, "-C="):
				if found {
					return "", "go-C", false, fmt.Errorf("go command has duplicate -C target; recovery: %s", subjectRecovery)
				}
				value = strings.TrimPrefix(arg, "-C=")
			default:
				continue
			}
			if strings.TrimSpace(value) == "" {
				return "", "go-C", false, fmt.Errorf("go command has empty -C target; recovery: %s", subjectRecovery)
			}
			target, found = resolveTarget(control, value), true
		}
		return target, "go-C", found, nil
	default:
		return control, "execution-cwd", false, nil
	}
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
		return CommandSubject{Mode: mode, ControlCWD: control, SubjectCWD: subject}, nil
	}
	git.Mode, git.ControlCWD, git.SubjectCWD = mode, control, subject
	return git, nil
}

func snapshotGit(subject string) (CommandSubject, error) {
	run := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", subject}, args...)...).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	top, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return CommandSubject{}, err
	}
	top, err = canonicalExistingDir(top)
	if err != nil {
		return CommandSubject{}, err
	}
	common, err := run("rev-parse", "--git-common-dir")
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
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return CommandSubject{}, err
	}
	tree, err := run("rev-parse", "HEAD^{tree}")
	if err != nil {
		return CommandSubject{}, err
	}
	status, err := run("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return CommandSubject{}, err
	}
	sum := sha256.Sum256([]byte(status))
	return CommandSubject{Git: true, GitTopLevel: top, GitCommonDir: filepath.Clean(common), GitHead: head, GitTree: tree,
		Dirty: status != "", StatusSHA256: "sha256:" + hex.EncodeToString(sum[:])}, nil
}
