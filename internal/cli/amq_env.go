package cli

import (
	"bytes"
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
	// amq treats --session NAME as shorthand for --root .agent-mail/<name>
	// and refuses both together. Prefer --session when both are supplied,
	// since session is the more specific selector. Callers like restore
	// historically passed both; we filter at the boundary so any other
	// future caller cannot trip the same trap.
	if session != "" {
		args = append(args, "--session", session)
	} else if rootFlag != "" {
		args = append(args, "--root", rootFlag)
	}
	cmd := exec.Command("amq", args...)
	// Strip AMQ identity vars unconditionally: the operator has already passed
	// --root/--session/--me on the wire, and a stale AM_ROOT/AM_ME from a
	// previous shell session must not silently override them.
	cmd.Env = envWithoutAMQIdentity(os.Environ())
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return amqEnv{}, fmt.Errorf("amq env: %w: %s", err, msg)
		}
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
