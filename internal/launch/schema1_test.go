package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSchema1SeatFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Record{
		Handle: "cto", Root: dir, BaseRoot: filepath.Dir(dir), Session: "s1",
		Runtime: "codex", Model: "gpt-6-astra", Effort: "medium",
		RolePath: "/r/role.md", RoleSHA256: "sha256:aa", RulesPath: "/p/rules.md", RulesSHA256: "sha256:bb",
		Branch: "seat/cto", Worktree: "/p/wt", AMQPath: "/opt/homebrew/bin/amq", EnvNeeds: []string{"OPENAI_API_KEY"},
		Resume: true, LaunchNonce: "n1",
		Apply: &Apply{Outcome: "applied", At: time.Unix(1, 0).UTC()},
		Exec:  &Exec{At: time.Unix(2, 0).UTC(), PID: 7, ProviderArgv: []string{"codex"}, DrainProof: "ok"},
	}
	path := filepath.Join(dir, FileName)
	if err := WriteFile(path, in); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	in.Schema = SchemaVersion
	got, _ := json.Marshal(out)
	want, _ := json.Marshal(in)
	if string(got) != string(want) {
		t.Fatalf("round trip:\n got %s\nwant %s", got, want)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", st.Mode().Perm())
	}
}

func TestV2RecordStillDecodes(t *testing.T) {
	// A 2.x record carries fields 3.0 dropped; it must still decode.
	v2 := `{"schema":1,"cwd":"/p","binary":"claude","argv":["--x"],"session":"s","handle":"cpo","role":"cpo","root":"/r",` +
		`"root_source":"project_amqrc","amq_version":"0.34.0","codex_args":["-c"],"tool_allowlist":["Bash"],` +
		`"preauthorized_actions":["Bash(gh pr create:*)"],"agent_pid":12,"started_at":"2026-01-01T00:00:00Z",` +
		`"goal_binding":{"mode":"x"},"tmux":{"pane_id":"%1"}}`
	rec, err := Decode([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Handle != "cpo" || rec.Binary != "claude" || rec.AgentPID != 12 || rec.Tmux == nil || rec.Tmux.PaneID != "%1" {
		t.Fatalf("decoded %+v", rec)
	}
}

func TestDecodeRefusesNewerSchema(t *testing.T) {
	if _, err := Decode([]byte(`{"schema":2,"handle":"x"}`)); err == nil {
		t.Fatal("schema 2 decoded")
	}
}

func TestStagedDirIsUnderTheGuardedExtension(t *testing.T) {
	if got := StagedDir("/r/s1", "cto"); got != filepath.FromSlash("/r/s1/extensions/amq-squad/launch/cto") {
		t.Fatalf("StagedDir = %s", got)
	}
}
