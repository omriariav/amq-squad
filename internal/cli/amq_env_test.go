package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAMQEnvUsesV1Fields(t *testing.T) {
	setupFakeAMQEnv(t, `{"schema_version":1,"amq_version":"0.34.0","root":"/tmp/mail/stream1","base_root":"/tmp/mail","session_name":"stream1","me":"cto","project":"amq-squad","root_source":"project_amqrc"}`)

	got, err := resolveAMQEnv("/ignored", "stream1", "codex")
	if err != nil {
		t.Fatalf("resolveAMQEnv: %v", err)
	}
	if got.SchemaVersion != 1 || got.AMQVersion != "0.34.0" ||
		got.Root != "/tmp/mail/stream1" || got.BaseRoot != "/tmp/mail" ||
		got.SessionName != "stream1" || got.Me != "cto" ||
		got.Project != "amq-squad" || got.RootSource != "project_amqrc" {
		t.Fatalf("resolveAMQEnv = %+v", got)
	}
}

func TestResolveAMQEnvBackfillsOlderRootOnlyOutput(t *testing.T) {
	setupFakeAMQEnv(t, `{"root":"/tmp/mail/stream1"}`)

	got, err := resolveAMQEnv("", "stream1", "cto")
	if err != nil {
		t.Fatalf("resolveAMQEnv: %v", err)
	}
	if got.Root != "/tmp/mail/stream1" || got.BaseRoot != "/tmp/mail/stream1" ||
		got.SessionName != "stream1" || got.Me != "cto" {
		t.Fatalf("resolveAMQEnv = %+v", got)
	}
}

func setupFakeAMQEnv(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"env\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"  printf '%s\\n' \"$AMQ_FAKE_ENV\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unexpected amq command: $*\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_ENV", body)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
