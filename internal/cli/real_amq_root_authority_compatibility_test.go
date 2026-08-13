package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestRealAMQRootAuthorityCompatibility is the disposable regression for
// AMQ's canonical-root authority contract. It intentionally constructs the
// same tuple that historically conflicted in live squads:
//
//   - cwd has a repo-local .amqrc naming an initialized base root
//   - inherited AM_ROOT names a different initialized exact session subroot
//   - the old exact-root launch pin sets AM_BASE_ROOT to that root, carries
//     matching physical identity pins, and leaves AM_SESSION absent
//
// The proof uses a write verb for the config-demand assertion so a successful
// list/read cannot conceal a still-broken send path.
func TestRealAMQRootAuthorityCompatibility(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run the disposable root-authority compatibility proof")
	}
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("AMQ_SQUAD_REAL_AMQ %q is unavailable or not executable: %v", binary, err)
	}
	version := strings.TrimSpace(realAMQCommand(t, binary, t.TempDir(), nil, "version"))
	expected := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ_VERSION"))
	if expected != "" && expected != "latest" && strings.TrimPrefix(version, "v") != strings.TrimPrefix(expected, "v") {
		t.Fatalf("real AMQ version = %q, expected requested %q", version, expected)
	}
	t.Logf("real AMQ binary=%s version=%s requested=%s", binary, version, expected)
	realAMQRootAuthorityCompatibilityContract(t, binary)
}

func realAMQRootAuthorityCompatibilityContract(t *testing.T, binary string) {
	t.Helper()
	project := t.TempDir()
	base := filepath.Join(project, ".agent-mail")
	session := "root-authority"
	activeRoot := filepath.Join(base, "review", "active-root")
	root := filepath.Join(base, "review", "configless-root")
	realAMQInitAgents(t, binary, project, base, "lead", "worker", team.DefaultOperatorHandle)
	realAMQInitAgents(t, binary, project, activeRoot, "lead", "worker", team.DefaultOperatorHandle)
	rc := []byte("{\n  \"root\": \".agent-mail\",\n  \"project\": \"root-authority-compat\"\n}\n")
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), rc, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "meta", "config.json")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("fixture exact root unexpectedly has config: %v", err)
	}

	cleanEnv := realAMQRootAuthorityCleanEnv(os.Environ())
	pinOut := realAMQCommand(t, binary, project, cleanEnv,
		"env", "--root", activeRoot, "--me", "lead")
	rootID := realAMQExportValue(pinOut, "AM_ROOT_ID")
	baseRootID := realAMQExportValue(pinOut, "AM_BASE_ROOT_ID")
	if rootID == "" || baseRootID == "" || rootID != baseRootID {
		t.Fatalf("AMQ env did not emit matching exact-root identity pins:\n%s", pinOut)
	}
	inherited := append(append([]string(nil), cleanEnv...),
		"AM_ROOT="+activeRoot,
		"AM_BASE_ROOT="+activeRoot,
		"AM_ROOT_ID="+rootID,
		"AM_BASE_ROOT_ID="+baseRootID,
		"AM_ME=lead",
	)
	const body = "root authority write proof"
	bareOut, bareErr := realAMQRootAuthorityTry(binary, project, inherited,
		"send", "--to", "worker", "--subject", "bare cwd authority", "--body", body, "--json")
	if bareErr != nil {
		t.Fatalf("bare repo-cwd send did not honor inherited exact-root authority: %v\n%s", bareErr, bareOut)
	}
	var bareResult struct {
		Root       string `json:"root"`
		SourceRoot string `json:"source_root"`
		Outbox     struct {
			Written bool `json:"written"`
		} `json:"outbox"`
	}
	if err := json.Unmarshal([]byte(bareOut), &bareResult); err != nil {
		t.Fatalf("decode bare repo-cwd send result: %v\n%s", err, bareOut)
	}
	if bareResult.Root != activeRoot || bareResult.SourceRoot != activeRoot || !bareResult.Outbox.Written {
		t.Fatalf("bare repo-cwd send authority = %+v, want root/source_root %q and outbox written\n%s", bareResult, activeRoot, bareOut)
	}

	// Force routeCommandFor through its deterministic fallback instead of
	// consulting an unrelated host "amq" from the test runner's PATH.
	t.Setenv("PATH", t.TempDir())
	currentProject := projectIdentity{Name: "root-authority-compat", Dir: base, Known: true}
	printed, routeErr := routeCommandFor(activeRoot, session, currentProject, currentProject, true, "lead", "worker", session)
	if routeErr != "" {
		t.Fatalf("rooted printed route is not routable: %s", routeErr)
	}
	for _, want := range []string{"amq send", "--root " + shellQuote(activeRoot), "--me lead", "--to worker"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("printed route %q omitted %q", printed, want)
		}
	}

	refusedOut, refusedErr := realAMQRootAuthorityTry(binary, project, cleanEnv,
		"send", "--root", root, "--me", "lead", "--to", "worker",
		"--subject", "config demand", "--body", body, "--json")
	if refusedErr == nil {
		t.Fatalf("configless --root + --me write unexpectedly succeeded:\n%s", refusedOut)
	}
	lowerRefused := strings.ToLower(refusedOut)
	for _, want := range []string{"not initialized", "meta/config.json"} {
		if !strings.Contains(lowerRefused, want) {
			t.Fatalf("configless write refusal omitted attribution token %q:\n%s", want, refusedOut)
		}
	}

	// repairAMQRootAuthority (used just below, and again for coopRoot) shells
	// out to the literal "amq" on PATH via runAMQCommand, but PATH was just
	// cleared above to force routeCommandFor's fallback. Point runAMQCommand
	// at the exact test binary so both calls in this test still exercise the
	// real AMQ compatibility contract instead of failing to find "amq".
	previousRun := runAMQCommand
	runAMQCommand = func(request amqCommandRequest) ([]byte, error) {
		cmd := exec.Command(binary, request.Arg...)
		if request.Context != nil {
			cmd = exec.CommandContext(request.Context, binary, request.Arg...)
		}
		cmd.Dir = request.Dir
		cmd.Env = request.Env
		cmd.Stdin = request.Stdin
		out, err := cmd.CombinedOutput()
		if err != nil {
			return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return out, nil
	}
	t.Cleanup(func() { runAMQCommand = previousRun })

	// Fresh launch prepares the exact config AND the agents/ mailbox layout
	// before coop exec (#491, AMQ 0.60.5+): coop provisioning now refuses a
	// root that has meta/config.json but no agents/ directory instead of
	// self-provisioning it on first coop exec, so repairAMQRootAuthority
	// (config + doctor --fix-mailboxes) must run first, exactly as
	// internal/cli/launch.go now does before its own coop exec.
	coopRoot := filepath.Join(base, "review", "coop-authority")
	if _, err := repairAMQRootAuthority(project, coopRoot, []string{"lead", team.DefaultOperatorHandle}); err != nil {
		t.Fatalf("prepare fresh coop root authority: %v", err)
	}
	coopOut, coopErr := realAMQRootAuthorityTry(binary, project, cleanEnv,
		"coop", "exec", "--root", coopRoot, "--me", "lead", "--no-wake", "env")
	if coopErr != nil {
		t.Fatalf("config-authorized coop exec failed: %v\n%s", coopErr, coopOut)
	}
	if !strings.Contains(coopOut, "AM_ROOT="+coopRoot) || !strings.Contains(coopOut, "AM_ME=lead") {
		t.Fatalf("config-authorized coop child identity mismatch:\n%s", coopOut)
	}
	if _, err := os.Stat(filepath.Join(coopRoot, "agents", "lead", "inbox", "new")); err != nil {
		t.Fatalf("coop exec did not materialize configured lead mailbox: %v", err)
	}

	repair, err := repairAMQRootAuthority(project, root, []string{"lead", "worker", team.DefaultOperatorHandle})
	if err != nil {
		t.Fatalf("repair root authority: %v", err)
	}
	if !repair.Config.Changed || repair.Config.Path != configPath {
		t.Fatalf("config repair = %+v", repair.Config)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config amqRootConfigDocument
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if want := []string{"lead", "user", "worker"}; !reflect.DeepEqual(config.Agents, want) {
		t.Fatalf("repaired config agents = %v, want %v", config.Agents, want)
	}

	sentOut, sentErr := realAMQRootAuthorityTry(binary, project, cleanEnv,
		"send", "--root", root, "--me", "lead", "--to", "worker",
		"--subject", "rooted write", "--body", body, "--json")
	if sentErr != nil {
		t.Fatalf("repaired rooted write failed: %v\n%s", sentErr, sentOut)
	}
	if parseSentMessageID(sentOut) == "" {
		t.Fatalf("repaired rooted write omitted message id:\n%s", sentOut)
	}
	drained, drainErr := realAMQRootAuthorityTry(binary, project, cleanEnv,
		"drain", "--root", root, "--me", "worker", "--include-body")
	if drainErr != nil || !strings.Contains(drained, body) {
		t.Fatalf("rooted drain did not receive write (err=%v):\n%s", drainErr, drained)
	}
}

func realAMQRootAuthorityCleanEnv(env []string) []string {
	return amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(env))
}

func realAMQRootAuthorityTry(binary, dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = amqexec.NoUpdateCheckEnv(env)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func realAMQExportValue(output, key string) string {
	prefix := "export " + key + "="
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		return value
	}
	return ""
}
