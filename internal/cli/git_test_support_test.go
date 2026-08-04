package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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
