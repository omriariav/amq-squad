package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestRealAMQCompatibility is intentionally opt-in. Ordinary internal/cli
// tests must never discover or invoke a host AMQ binary; CI supplies this
// exact binary via AMQ_SQUAD_REAL_AMQ for the focused floor/latest lane only.
func TestRealAMQCompatibility(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run disposable real-AMQ compatibility checks")
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatalf("stat AMQ_SQUAD_REAL_AMQ %q: %v", binary, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("AMQ_SQUAD_REAL_AMQ %q is not an executable file", binary)
	}
	version := strings.TrimSpace(realAMQCommand(t, binary, t.TempDir(), nil, "version"))
	if !semverMeetsStableFloor(version, doctorMinAMQVersion) {
		t.Fatalf("real AMQ %q is below supported floor %s", version, doctorMinAMQVersion)
	}
	if expected := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ_VERSION")); expected != "" && expected != "latest" && strings.TrimPrefix(version, "v") != strings.TrimPrefix(expected, "v") {
		t.Fatalf("real AMQ version = %q, expected requested %q", version, expected)
	}
	t.Logf("real AMQ binary=%s version=%s requested=%s", binary, version, os.Getenv("AMQ_SQUAD_REAL_AMQ_VERSION"))

	t.Run("sessionful default profile", func(t *testing.T) {
		project := t.TempDir()
		root := filepath.Join(project, ".agent-mail", "issue-449")
		realAMQInit(t, binary, project, root)
		ctx := amqContext{
			ProjectDir: project,
			Profile:    team.DefaultProfile,
			Env:        amqEnv{BaseRoot: filepath.Dir(root)},
			Root:       root,
			Me:         "lead",
			Session:    "issue-449",
			PinMode:    amqPinSessionful,
		}
		realAMQRoundTrip(t, binary, ctx, root, "sessionful")
	})

	t.Run("exact root named profile", func(t *testing.T) {
		project := t.TempDir()
		root := filepath.Join(project, "root with spaces", ".agent-mail", "review", "issue-449")
		realAMQInit(t, binary, project, root)
		ctx := amqContext{
			ProjectDir: project,
			Profile:    "review",
			Root:       root,
			Me:         "lead",
			Session:    "issue-449",
			PinMode:    amqPinExactRoot,
		}
		realAMQRoundTrip(t, binary, ctx, root, "exact-root")
	})
}

func realAMQInit(t *testing.T, binary, project, root string) {
	t.Helper()
	realAMQCommand(t, binary, project, amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ())), "init", "--root", root, "--agents", "lead,worker")
}

func realAMQRoundTrip(t *testing.T, binary string, lead amqContext, root, label string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "must-not-be-used")
	t.Setenv("AMQ_GLOBAL_ROOT", outside)
	leadEnv := amqCommandEnv(lead)
	if lead.PinMode == amqPinExactRoot && envHasPrefix(leadEnv, "AM_SESSION", "") {
		t.Fatalf("%s exact-root tuple leaked AM_SESSION: %#v", label, leadEnv)
	}
	if lead.PinMode == amqPinSessionful && !envHas(leadEnv, "AM_SESSION", lead.Session) {
		t.Fatalf("%s sessionful tuple omitted AM_SESSION=%q: %#v", label, lead.Session, leadEnv)
	}
	if envHasPrefix(leadEnv, "AMQ_GLOBAL_ROOT", "") {
		t.Fatalf("%s tuple leaked stale AMQ_GLOBAL_ROOT: %#v", label, leadEnv)
	}

	var got amqEnv
	if err := json.Unmarshal([]byte(realAMQCommand(t, binary, lead.ProjectDir, leadEnv, "env", "--json")), &got); err != nil {
		t.Fatalf("%s bare amq env JSON: %v", label, err)
	}
	if !sameResolvedDir(got.Root, root) {
		t.Fatalf("%s bare amq env root = %q, want %q", label, got.Root, root)
	}

	body := "real AMQ " + label + " round trip"
	realAMQCommand(t, binary, lead.ProjectDir, leadEnv, "send", "--to", "worker", "--subject", "compatibility", "--body", body, "--kind", "todo")
	worker := lead
	worker.Me = "worker"
	drained := realAMQCommand(t, binary, worker.ProjectDir, amqCommandEnv(worker), "drain", "--include-body")
	if !strings.Contains(drained, body) {
		t.Fatalf("%s bare amq drain did not contain delivered body:\n%s", label, drained)
	}
	if again := strings.TrimSpace(realAMQCommand(t, binary, worker.ProjectDir, amqCommandEnv(worker), "drain", "--include-body")); again != "" {
		t.Fatalf("%s second bare amq drain = %q, want empty", label, again)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("%s command touched stale global root %q: %v", label, outside, err)
	}
}

func realAMQCommand(t *testing.T, binary, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	if env == nil {
		env = amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	}
	cmd.Env = amqexec.NoUpdateCheckEnv(env)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("real amq %s: %v\nstderr:\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}
