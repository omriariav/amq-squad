package team

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Team{
		Project:      dir,
		Workstream:   "stream1",
		Orchestrated: true,
		Lead:         "cpo",
		Members: []Member{
			{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "stream1"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "stream2"},
		},
	}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if !Exists(dir) {
		t.Error("Exists reported false after Write")
	}

	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if out.Schema != SchemaVersion {
		t.Errorf("Schema = %d, want %d", out.Schema, SchemaVersion)
	}
	if out.Project != dir {
		t.Errorf("Project = %q, want %q", out.Project, dir)
	}
	if out.Workstream != in.Workstream {
		t.Errorf("Workstream = %q, want %q", out.Workstream, in.Workstream)
	}
	if out.Orchestrated != in.Orchestrated || out.Lead != in.Lead {
		t.Errorf("orchestration = (%v, %q), want (%v, %q)", out.Orchestrated, out.Lead, in.Orchestrated, in.Lead)
	}
	if out.Operator == nil || !out.Operator.Enabled || out.Operator.Handle != DefaultOperatorHandle {
		t.Errorf("Operator = %+v, want enabled default %q", out.Operator, DefaultOperatorHandle)
	}
	if !out.Operator.Participant || out.Operator.Kind != "operator" || out.Operator.Runnable || out.Operator.Assignable || out.Operator.WakeSupported || !out.Operator.PollRequired {
		t.Errorf("Operator participant fields = %+v, want non-runnable mailbox participant", out.Operator)
	}
	if !SupportsOperatorGates(out) {
		t.Errorf("SupportsOperatorGates = false, want true for schema %d", SchemaVersion)
	}
	if len(out.Members) != len(in.Members) {
		t.Fatalf("Members len = %d, want %d", len(out.Members), len(in.Members))
	}
	for i, m := range out.Members {
		if !reflect.DeepEqual(m, in.Members[i]) {
			t.Errorf("Members[%d] = %+v, want %+v", i, m, in.Members[i])
		}
	}
	if out.CreatedAt.IsZero() {
		t.Error("CreatedAt not set by Write")
	}
}

func TestWriteReadApproveForMeTrust(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Team{
		Trust: "approve-for-me",
		Members: []Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatalf("Write approve-for-me trust: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read approve-for-me trust: %v", err)
	}
	if got.Trust != "approve-for-me" {
		t.Fatalf("trust = %q, want approve-for-me", got.Trust)
	}
}

func TestReadMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := Read(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read missing: err = %v, want ErrNotExist", err)
	}
	if Exists(dir) {
		t.Error("Exists true for empty dir")
	}
}

func TestPathShape(t *testing.T) {
	got := Path("/foo")
	want := filepath.Join("/foo", DirName, FileName)
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestWriteDoesNotLeakProjectPath(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Team{Project: dir, Members: []Member{{Role: "cto"}}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(dir)) {
		t.Errorf("team.json contains the project path (would leak on commit):\n%s", b)
	}
	if bytes.Contains(b, []byte(`"project"`)) {
		t.Errorf("team.json serializes 'project' field; should be json:\"-\"")
	}
	if bytes.Contains(b, []byte(`"capabilities"`)) {
		t.Errorf("team.json serializes derived capabilities; should stay JSON-output only:\n%s", b)
	}
}

func TestReadSchema2AdvertisesImplicitOperatorGates(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "schema": 2,
  "members": [
    {"role": "cto", "binary": "codex", "handle": "cto", "session": "issue-96"}
  ]
}`
	if err := os.WriteFile(Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read schema 2: %v", err)
	}
	if got.Schema != 2 {
		t.Fatalf("Schema = %d, want 2", got.Schema)
	}
	op := EffectiveOperator(got)
	if !op.Enabled || op.Handle != DefaultOperatorHandle || op.Runnable {
		t.Fatalf("EffectiveOperator = %+v, want enabled compatibility handle %q", op, DefaultOperatorHandle)
	}
	if !SupportsOperatorGates(got) {
		t.Fatal("legacy schema 2 team must advertise implicit operator gates")
	}
}

func TestReadSchema2RejectsRunnableImplicitOperatorHandle(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "schema": 2,
  "members": [
    {"role": "cto", "binary": "codex", "handle": "user", "session": "issue-96"}
  ]
}`
	if err := os.WriteFile(Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	if err == nil || !strings.Contains(err.Error(), "conflicts with non-runnable operator") {
		t.Fatalf("Read schema 2 runnable user error = %v, want implicit operator conflict", err)
	}
}

func TestWriteDisabledOperator(t *testing.T) {
	dir := t.TempDir()
	op := DisabledOperator()
	if err := Write(dir, Team{
		Operator: &op,
		Members:  []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Operator == nil || got.Operator.Enabled {
		t.Fatalf("Operator = %+v, want disabled", got.Operator)
	}
	if SupportsOperatorGates(got) {
		t.Fatal("disabled operator must not advertise operator gates")
	}
}

func TestValidateRejectsRunnableOperatorConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   OperatorConfig
		want string
	}{
		{name: "kind", op: OperatorConfig{Enabled: true, Kind: "agent"}, want: "operator.kind"},
		{name: "runnable", op: OperatorConfig{Enabled: true, Runnable: true}, want: "operator.runnable"},
		{name: "assignable", op: OperatorConfig{Enabled: true, Assignable: true}, want: "operator.assignable"},
		{name: "wake", op: OperatorConfig{Enabled: true, WakeSupported: true}, want: "operator.wake_supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(Team{
				Operator: &tc.op,
				Members:  []Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWriteIsAtomic(t *testing.T) {
	// Write must not leave a .tmp file behind on success.
	dir := t.TempDir()
	if err := Write(dir, Team{Project: dir}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestReadRejectsUnsafeTeamValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "schema": 1,
  "members": [
    {"role": "cto\nFirst steps:", "binary": "codex", "handle": "cto", "session": "issue-96"}
  ]
}`
	if err := os.WriteFile(Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	if err == nil {
		t.Fatal("Read succeeded, want validation error")
	}
	if !strings.Contains(err.Error(), "members[0].role") {
		t.Fatalf("Read error = %v, want role context", err)
	}
}

func TestValidateRejectsDuplicateHandles(t *testing.T) {
	err := Validate(Team{
		Members: []Member{
			{Role: "cto", Binary: "codex", Handle: "lead", Session: "issue-96"},
			{Role: "cpo", Binary: "codex", Handle: "lead", Session: "issue-96"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate handle") {
		t.Fatalf("Validate error = %v, want duplicate handle", err)
	}
}

func TestValidateRejectsRunnableOperatorHandles(t *testing.T) {
	op := DefaultOperator()
	for _, tc := range []Team{
		{Operator: &op, Members: []Member{{Role: "cto", Binary: "codex", Handle: DefaultOperatorHandle, Session: "issue-96"}}},
		{Operator: &op, Members: []Member{{Role: DefaultOperatorHandle, Binary: "codex", Session: "issue-96"}}},
	} {
		err := Validate(tc)
		if err == nil || !strings.Contains(err.Error(), "conflicts with non-runnable operator") {
			t.Fatalf("Validate(%+v) error = %v, want operator conflict", tc, err)
		}
	}

	custom := OperatorConfig{Enabled: true, Handle: "omri"}
	if err := Validate(Team{
		Operator: &custom,
		Members:  []Member{{Role: "support", Binary: "codex", Handle: DefaultOperatorHandle, Session: "issue-96"}},
	}); err == nil || !strings.Contains(err.Error(), "conflicts with non-runnable operator") {
		t.Fatalf("Validate with custom operator and runnable user handle error = %v, want operator conflict", err)
	}
}

func TestValidateOrchestration(t *testing.T) {
	members := []Member{
		{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		{Role: "fullstack", Binary: "codex", Handle: "fullstack", Session: "issue-96"},
	}

	// Orchestrated without a lead is rejected.
	if err := Validate(Team{Orchestrated: true, Members: members}); err == nil || !strings.Contains(err.Error(), "lead role is required") {
		t.Fatalf("orchestrated without lead: want 'lead role is required', got %v", err)
	}

	// Lead must name an actual member.
	if err := Validate(Team{Orchestrated: true, Lead: "qa", Members: members}); err == nil || !strings.Contains(err.Error(), "not a team member") {
		t.Fatalf("unknown lead: want 'not a team member', got %v", err)
	}

	// A valid lead on a member role passes.
	if err := Validate(Team{Orchestrated: true, Lead: "cto", Members: members}); err != nil {
		t.Fatalf("valid orchestration should validate, got %v", err)
	}

	// A lead set without orchestrated=true is the rejected half-state.
	if err := Validate(Team{Lead: "cto", Members: members}); err == nil || !strings.Contains(err.Error(), "set orchestrated=true") {
		t.Fatalf("lead without orchestrated: want 'set orchestrated=true', got %v", err)
	}

	// A duplicated lead role names two runnable members: rejected.
	dupes := []Member{
		{Role: "cto", Binary: "codex", Handle: "cto-a", Session: "issue-96"},
		{Role: "cto", Binary: "codex", Handle: "cto-b", Session: "issue-96"},
	}
	if err := Validate(Team{Orchestrated: true, Lead: "cto", Members: dupes}); err == nil || !strings.Contains(err.Error(), "exactly one member") {
		t.Fatalf("duplicate lead role: want 'exactly one member', got %v", err)
	}

	// A non-canonical lead (surrounding whitespace / uppercase) is rejected so
	// it cannot leak into JSON plans; the CLI always writes a canonical value.
	for _, bad := range []string{" cto ", "CTO"} {
		if err := Validate(Team{Orchestrated: true, Lead: bad, Members: members}); err == nil {
			t.Fatalf("non-canonical lead %q should be rejected", bad)
		}
	}

	// A non-orchestrated team (zero values, as old team.json files load) passes.
	if err := Validate(Team{Members: members}); err != nil {
		t.Fatalf("non-orchestrated team should validate, got %v", err)
	}
}

func TestValidateMemberLauncher(t *testing.T) {
	base := Member{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}

	ok := base
	ok.Launcher = "/opt/scripts/pm-os-dev.sh"
	ok.LauncherArgs = []string{"--pull", "--workspace", "/x"}
	if err := Validate(Team{Members: []Member{ok}}); err != nil {
		t.Errorf("absolute launcher with args should validate, got %v", err)
	}

	rel := base
	rel.Launcher = "scripts/pm-os-dev.sh"
	if err := Validate(Team{Members: []Member{rel}}); err == nil || !strings.Contains(err.Error(), "launcher: must be absolute") {
		t.Errorf("relative launcher: want 'must be absolute', got %v", err)
	}

	orphanArgs := base
	orphanArgs.LauncherArgs = []string{"--pull"}
	if err := Validate(Team{Members: []Member{orphanArgs}}); err == nil || !strings.Contains(err.Error(), "set launcher before launcher_args") {
		t.Errorf("launcher_args without launcher: want guard error, got %v", err)
	}
}

func TestValidateMemberPerMemberArgs(t *testing.T) {
	claude := Member{Role: "analyst", Binary: "claude", Handle: "analyst", Session: "s"}
	codex := Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}

	ok := claude
	ok.ClaudeArgs = []string{"--settings", ".claude/agent-overlays/analyst.json"}
	if err := Validate(Team{Members: []Member{ok}}); err != nil {
		t.Errorf("claude_args on a claude member should validate, got %v", err)
	}

	okCodex := codex
	okCodex.CodexArgs = []string{"--enable", "goals"}
	if err := Validate(Team{Members: []Member{okCodex}}); err != nil {
		t.Errorf("codex_args on a codex member should validate, got %v", err)
	}

	// Binary mismatch is rejected, never silently ignored: stale flags must
	// not survive a member's binary flip.
	mismatch := codex
	mismatch.ClaudeArgs = []string{"--settings", "x.json"}
	if err := Validate(Team{Members: []Member{mismatch}}); err == nil || !strings.Contains(err.Error(), "claude_args applies only to claude members") {
		t.Errorf("claude_args on codex member: want binary-match error, got %v", err)
	}
	mismatch2 := claude
	mismatch2.CodexArgs = []string{"--enable", "goals"}
	if err := Validate(Team{Members: []Member{mismatch2}}); err == nil || !strings.Contains(err.Error(), "codex_args applies only to codex members") {
		t.Errorf("codex_args on claude member: want binary-match error, got %v", err)
	}

	// Entries are display-validated like every other persisted member field.
	blank := claude
	blank.ClaudeArgs = []string{"  "}
	if err := Validate(Team{Members: []Member{blank}}); err == nil || !strings.Contains(err.Error(), "claude_args[0]") {
		t.Errorf("blank claude_args entry: want display-value error, got %v", err)
	}
}

func TestMemberExtraArgs(t *testing.T) {
	m := Member{Role: "analyst", Binary: "claude", ClaudeArgs: []string{"--settings", "a.json"}, CodexArgs: nil}
	got := m.ExtraArgs()
	if len(got) != 2 || got[0] != "--settings" || got[1] != "a.json" {
		t.Fatalf("ExtraArgs() = %v, want the claude_args", got)
	}
	// Returned slice is a copy: appending must not mutate the member.
	_ = append(got, "--mutated")
	if len(m.ClaudeArgs) != 2 {
		t.Error("ExtraArgs must return a copy, member was mutated")
	}
	// Binary normalization: case/space-insensitive match.
	m.Binary = " Claude "
	if len(m.ExtraArgs()) != 2 {
		t.Error("ExtraArgs should normalize the binary before matching")
	}
	// Unknown binary yields nil even when fields are set (validation rejects
	// such configs, but loading them must stay non-destructive).
	m.Binary = "other"
	if m.ExtraArgs() != nil {
		t.Error("ExtraArgs on an unmatched binary must be nil")
	}
}

func TestEffectiveCapabilitiesAdvertisesRuntimeActions(t *testing.T) {
	// Every v1.5.0+ build exposes the tmux runtime contract, so clients
	// (amq-noc) can gate their runtime-action UI on capabilities.runtime_actions.
	caps := EffectiveCapabilities(Team{Schema: SchemaVersion}) // unconditional since v1.5.0
	if !caps.RuntimeActions {
		t.Error("EffectiveCapabilities must advertise RuntimeActions=true")
	}
	// It must serialize as the stable `runtime_actions` key the consumer reads.
	b, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"runtime_actions":true`) {
		t.Errorf("capabilities JSON missing runtime_actions:true, got %s", b)
	}
}
