package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
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

func resolveAMQEnvForTeamProfile(cwd, profile, session, handle string) (amqEnv, error) {
	if squadnamespace.NormalizeProfile(profile) == team.DefaultProfile {
		return resolveAMQEnvInDir(cwd, "", session, handle)
	}
	root := squadnamespace.AMQRoot(cwd, profile, session)
	return resolveAMQEnvForLaunch(cwd, root, session, profile, handle)
}

func resolveAMQEnvInDir(cwd, rootFlag, session, handle string) (amqEnv, error) {
	args := []string{"env", "--json"}
	if handle != "" {
		args = append(args, "--me", handle)
	}
	// amq treats --session NAME as shorthand for --root .agent-mail/<name>
	// and refuses both together. Prefer --session when both are supplied,
	// since session is the more specific selector, and warn so the dropped
	// --root is visible.
	if session != "" && rootFlag != "" {
		fmt.Fprintf(os.Stderr, "warning: amq-squad: --session %q implies --root .agent-mail/%s; ignoring conflicting --root %q.\n", session, session, rootFlag)
	}
	if session != "" {
		args = append(args, "--session", session)
	} else if rootFlag != "" {
		args = append(args, "--root", rootFlag)
	}
	cmd := exec.Command("amq", args...)
	// Strip AMQ identity vars unconditionally: the operator has already passed
	// --root/--session/--me on the wire, and a stale AM_ROOT/AM_ME from a
	// previous shell session must not silently override them.
	cmd.Env = amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
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

func absoluteAMQRoot(cwd, root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		abs, err := filepath.Abs(root)
		if err == nil {
			return abs
		}
		return filepath.Clean(root)
	}
	return filepath.Clean(filepath.Join(base, root))
}

func envWithoutAMQIdentity(env []string) []string {
	remove := map[string]bool{
		"AM_ROOT":         true,
		"AM_ROOT_ID":      true,
		"AM_BASE_ROOT":    true,
		"AM_BASE_ROOT_ID": true,
		"AM_SESSION":      true,
		"AM_ME":           true,
		"AMQ_GLOBAL_ROOT": true,
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		// AMQ v0.51.1 exports both the owner token and private descriptor
		// state under AMQ_WAKE_. Scrub the namespace so new wake internals
		// cannot silently reintroduce live managed-agent authority.
		if ok && (remove[key] || strings.HasPrefix(key, "AMQ_WAKE_") || strings.HasPrefix(key, "AMQ_SQUAD_TERMINAL_")) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func scanBaseRootForProject(projectDir string) (string, error) {
	return resolveAMQBaseRootForProject(projectDir, "", "amq-squad")
}

// chooseProjectBaseRoot picks the directory the status board should scan for
// sessions (it walks <chosen>/<session>/agents).
func chooseProjectBaseRoot(projectDir string, env amqEnv) string {
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(projectDir, p)
	}

	candidates := []string{
		abs(env.BaseRoot),
		abs(env.Root),
		abs(filepath.Join(projectDir, defaultBaseRootName)),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if filepath.Base(c) != defaultBaseRootName {
			continue
		}
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}

	// No `.agent-mail` container found: preserve graceful degradation by
	// returning the resolved base_root (or root when base_root is empty).
	if fallback := abs(env.BaseRoot); fallback != "" {
		return fallback
	}
	return abs(env.Root)
}

// defaultBaseRootName is the basename of the conventional AMQ base root
// (<project>/.agent-mail).
const defaultBaseRootName = ".agent-mail"
