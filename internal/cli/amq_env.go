package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type amqEnv struct {
	SchemaVersion int               `json:"schema_version"`
	AMQVersion    string            `json:"amq_version"`
	Root          string            `json:"root"`
	BaseRoot      string            `json:"base_root"`
	SessionName   string            `json:"session_name"`
	InSession     bool              `json:"in_session"`
	Me            string            `json:"me"`
	Project       string            `json:"project"`
	RootSource    string            `json:"root_source"`
	Peers         map[string]string `json:"peers"`
}

func resolveAMQEnv(rootFlag, session, handle string) (amqEnv, error) {
	return resolveAMQEnvInDir("", rootFlag, session, handle)
}

func resolveAMQEnvInDir(cwd, rootFlag, session, handle string) (amqEnv, error) {
	args := []string{"env", "--json"}
	if handle != "" {
		args = append(args, "--me", handle)
	}
	if rootFlag != "" {
		args = append(args, "--root", rootFlag)
	}
	if session != "" {
		args = append(args, "--session", session)
	}
	cmd := exec.Command("amq", args...)
	if cwd != "" {
		cmd.Dir = cwd
		cmd.Env = envWithoutAMQIdentity(os.Environ())
	}
	out, err := cmd.Output()
	if err != nil {
		return amqEnv{}, fmt.Errorf("amq env: %w", err)
	}
	var parsed amqEnv
	if err := json.Unmarshal(out, &parsed); err != nil {
		return amqEnv{}, fmt.Errorf("parse amq env output: %w", err)
	}
	if parsed.Root == "" {
		return amqEnv{}, fmt.Errorf("amq env returned empty root")
	}
	if parsed.BaseRoot == "" {
		parsed.BaseRoot = parsed.Root
	}
	if parsed.Me == "" {
		parsed.Me = handle
	}
	if parsed.SessionName == "" {
		parsed.SessionName = session
	}
	return parsed, nil
}

func envWithoutAMQIdentity(env []string) []string {
	remove := map[string]bool{
		"AM_ROOT":      true,
		"AM_BASE_ROOT": true,
		"AM_ME":        true,
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !remove[key] {
			out = append(out, entry)
		}
	}
	return out
}

func scanBaseRootForProject(projectDir string) (string, error) {
	env, err := resolveAMQEnvInDir(projectDir, "", "", "amq-squad")
	if err != nil {
		return "", err
	}
	if env.BaseRoot != "" {
		return env.BaseRoot, nil
	}
	return env.Root, nil
}
