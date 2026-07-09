package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func withAMQCommandSeams(t *testing.T, env amqEnv, output string) *[]amqCommandRequest {
	t.Helper()
	var calls []amqCommandRequest
	prevEnv := resolveAMQEnvForAMQCommand
	prevRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		got := env
		if strings.TrimSpace(rootFlag) != "" {
			got.Root = rootFlag
		} else {
			got.Root = strings.ReplaceAll(got.Root, "{session}", session)
		}
		got.SessionName = session
		got.Me = handle
		if got.BaseRoot == "" {
			got.BaseRoot = ".agent-mail"
		}
		return got, nil
	}
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		calls = append(calls, req)
		return []byte(output), nil
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = prevEnv
		runAMQCommand = prevRun
	})
	return &calls
}

func TestAMQRouteBuildsRouteExplain(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, `{"routable":true}`+"\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"route", "--session", "issue-96", "--me", "cto", "--to", "fullstack", "--target-session", "review", "--json"})
	})
	if err != nil {
		t.Fatalf("amq route: %v", err)
	}
	if !strings.Contains(stdout, `"routable":true`) {
		t.Fatalf("route output = %q", stdout)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"route explain", "--from-root", ".agent-mail/issue-96", "--me cto", "--to fullstack", "--session review", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("route args missing %q: %s", want, got)
		}
	}
}

func TestResolveAMQContextForProjectIsPrimaryResolver(t *testing.T) {
	dir := t.TempDir()
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail", AMQVersion: "0.38.0"}, "ok\n")
	ctx, err := resolveAMQContextForProject(dir, "issue-96", "cto")
	if err != nil {
		t.Fatalf("resolveAMQContextForProject: %v", err)
	}
	if !sameResolvedDir(ctx.ProjectDir, dir) {
		t.Fatalf("ProjectDir = %q, want %q", ctx.ProjectDir, dir)
	}
	if ctx.Me != "cto" || ctx.Env.SessionName != "issue-96" {
		t.Fatalf("identity/session not resolved: %+v", ctx)
	}
	if !strings.HasSuffix(ctx.Root, ".agent-mail/issue-96") {
		t.Fatalf("Root = %q, want session root", ctx.Root)
	}
	base, err := resolveAMQBaseRootForProject(dir, "issue-96", "cto")
	if err != nil {
		t.Fatalf("resolveAMQBaseRootForProject: %v", err)
	}
	if !strings.HasSuffix(base, ".agent-mail") {
		t.Fatalf("BaseRoot = %q, want .agent-mail container", base)
	}
}

func TestAMQRouteAddsJSONByDefault(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, `{"routable":true}`+"\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"route", "--session", "issue-96", "--me", "cto", "--to", "fullstack"})
	})
	if err != nil {
		t.Fatalf("amq route: %v", err)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"route explain", "--to fullstack", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("route args missing %q: %s", want, got)
		}
	}
}

func TestAMQReceiptsWaitBuildsCommand(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "receipt ok\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"receipts", "wait", "--session", "issue-96", "--me", "qa", "--msg-id", "msg_123", "--stage", "dlq", "--timeout", "5s"})
	})
	if err != nil {
		t.Fatalf("amq receipts wait: %v", err)
	}
	if stdout != "receipt ok\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"receipts wait", ".agent-mail/issue-96", "--me qa", "--msg-id msg_123", "--stage dlq", "--timeout 5s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("receipts wait args missing %q: %s", want, got)
		}
	}
}

func TestAMQDLQRetryPreviewAndYesExecutes(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "retried\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"dlq", "retry", "--session", "issue-96", "--me", "qa", "--id", "dlq_1", "--yes"})
	})
	if err != nil {
		t.Fatalf("amq dlq retry: %v", err)
	}
	for _, want := range []string{"AMQ command preview", "amq dlq retry", "retried"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"dlq retry", ".agent-mail/issue-96", "--me qa", "--id dlq_1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dlq retry args missing %q: %s", want, got)
		}
	}
}

func TestAMQDLQRetryAllUsesRetryAllFlag(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "retried all\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"dlq", "retry-all", "--session", "issue-96", "--me", "qa", "--yes"})
	})
	if err != nil {
		t.Fatalf("amq dlq retry-all: %v", err)
	}
	if !strings.Contains(stdout, "amq dlq retry") || !strings.Contains(stdout, "--all") {
		t.Fatalf("stdout should preview retry --all:\n%s", stdout)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"dlq retry", ".agent-mail/issue-96", "--me qa", "--all"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dlq retry-all args missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "retry-all") {
		t.Fatalf("underlying AMQ command should be retry --all, got: %s", got)
	}
}

func TestAMQCleanupDryRunDoesNotExecute(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "cleaned\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"cleanup", "--session", "issue-96", "--tmp-older-than", "36h", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("amq cleanup dry-run: %v", err)
	}
	for _, want := range []string{"AMQ command preview", "amq cleanup", "--tmp-older-than 36h", "Dry run: command not executed."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run calls = %d, want 0", len(*calls))
	}
}

func TestAMQCleanupRequiresSession(t *testing.T) {
	chdir(t, t.TempDir())
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "cleaned\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"cleanup", "--tmp-older-than", "36h", "--dry-run"})
	})
	if err == nil || !strings.Contains(err.Error(), "amq cleanup requires --session") {
		t.Fatalf("cleanup without session error = %v", err)
	}
}

func TestAMQRejectsUnknownSubcommand(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"frobnicate"})
	})
	if err == nil || !strings.Contains(err.Error(), "unknown amq subcommand") {
		t.Fatalf("unknown subcommand error = %v", err)
	}
}

func TestAMQSendResolvesRootAndForwards(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "lead", "--to", "worker", "--kind", "todo", "--subject", "go"})
	})
	if err != nil {
		t.Fatalf("amq send: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	req := (*calls)[0]
	got := strings.Join(req.Arg, " ")
	// send is the verb; the resolved root is injected; the rest is forwarded
	// verbatim. --session/--me are consumed for resolution, NOT forwarded.
	for _, want := range []string{"send", "--root", ".agent-mail/issue-96", "--to worker", "--kind todo", "--subject go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("send args missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "--session issue-96") || strings.Contains(got, "--me lead") {
		t.Fatalf("resolution flags must not be forwarded to amq: %s", got)
	}
	// The acting handle reaches amq via AM_ME, and the root via AM_ROOT.
	if !envHas(req.Env, "AM_ME", "lead") {
		t.Fatalf("AM_ME=lead not injected: %v", req.Env)
	}
	if !envHasPrefix(req.Env, "AM_ROOT", ".agent-mail/issue-96") {
		t.Fatalf("AM_ROOT not injected with resolved root: %v", req.Env)
	}
}

func TestAMQSendRejectsSelfSendOnP2PThread(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "release-lead", "--to", "release-lead", "--thread", "p2p/release-lead__user", "--kind", "status", "--subject", "ACK"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing self-send on p2p thread") || !strings.Contains(err.Error(), "--to user") {
		t.Fatalf("self-send error = %v, want actionable rejection", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("self-send should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQSendAllowsOrdinaryP2PReplyToOtherParticipant(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "release-lead", "--to", "user", "--thread", "p2p/release-lead__user", "--kind", "status", "--subject", "ACK"})
	})
	if err != nil {
		t.Fatalf("send to other participant should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
}

func TestAMQSendAsTeamRoleRequiresBoundIdentity(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")
	t.Setenv("AM_ME", "orchestrator")
	t.Setenv("AM_ROOT", filepath.Join(base, "issue-96"))

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "cto", "--to", "user", "--kind", "status", "--subject", "gate"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing amq send as team role") {
		t.Fatalf("send-as error = %v, want authority rejection", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected send-as should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQSendAsRejectsNonLeadingMeWithoutAuthority(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--to", "user", "--me", "cto", "--kind", "status", "--subject", "gate"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing amq send as team role") {
		t.Fatalf("send-as error = %v, want authority rejection", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected send-as should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQSendAsRejectsNonLeadingFromWithoutAuthority(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--to", "user", "--from", "cto", "--kind", "status", "--subject", "gate"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing amq send as team role") {
		t.Fatalf("send-as error = %v, want authority rejection", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected send-as should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQSendAsOperatorHandleRequiresUnsafeOverride(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", team.DefaultOperatorHandle, "--to", "cto", "--kind", "answer", "--subject", "APPROVED: tag"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing amq send as operator handle") {
		t.Fatalf("operator send-as error = %v, want authority rejection", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected operator send-as should not call amq, calls = %d", len(*calls))
	}

	if _, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", team.DefaultOperatorHandle, "--unsafe-send-as", "--reason", "repair imported gate answer", "--to", "cto", "--kind", "answer", "--subject", "APPROVED: tag"})
	}); err != nil {
		t.Fatalf("unsafe operator send-as with reason should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	audit := filepath.Join(dir, ".amq-squad", "boundary-audit", "issue-96.jsonl")
	b, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(b), `"target":"user"`) || !strings.Contains(string(b), "repair imported gate answer") {
		t.Fatalf("operator send-as audit = %s", b)
	}
}

func TestAMQReplyAsOperatorHandleRequiresUnsafeOverride(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"reply", "--session", "issue-96", "--from", team.DefaultOperatorHandle, "--id", "q1", "--subject", "APPROVED: tag", "--body", "Action: tag\nTarget: main"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing amq reply as operator handle") {
		t.Fatalf("operator reply-as error = %v, want authority rejection", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected operator reply-as should not call amq, calls = %d", len(*calls))
	}

	if _, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"reply", "--session", "issue-96", "--from", team.DefaultOperatorHandle, "--unsafe-send-as", "--reason", "repair imported gate answer", "--id", "q1", "--subject", "APPROVED: tag", "--body", "Action: tag\nTarget: main"})
	}); err != nil {
		t.Fatalf("unsafe operator reply-as with reason should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	if strings.Contains(got, "--unsafe-send-as") || strings.Contains(got, "--reason") {
		t.Fatalf("guard flags should be stripped before bare amq reply: %s", got)
	}
	audit := filepath.Join(dir, ".amq-squad", "boundary-audit", "issue-96.jsonl")
	b, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(b), `"target":"user"`) || !strings.Contains(string(b), "repair imported gate answer") {
		t.Fatalf("operator reply-as audit = %s", b)
	}
}

func TestAMQReplyAsTeamRoleRequiresBoundIdentity(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"reply", "--session", "issue-96", "--from", "cto", "--id", "q1", "--subject", "APPROVED: tag"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing amq reply as team role") {
		t.Fatalf("team reply-as error = %v, want authority rejection", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("rejected team reply-as should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQSendAsTeamRolePassesWithVerifiedRecord(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	root := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:          dir,
		Binary:       "codex",
		Handle:       "cto",
		Role:         "cto",
		Session:      "issue-96",
		Root:         root,
		TeamProfile:  team.DefaultProfile,
		External:     true,
		AdoptionMode: adoptionModeExternalProjectLead,
	})
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")

	if _, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "cto", "--to", "user", "--kind", "status", "--subject", "gate"})
	}); err != nil {
		t.Fatalf("verified send-as should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
}

func TestAMQSendAsUnsafeOverrideRequiresReasonAndAudits(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	base := filepath.Join(dir, ".agent-mail")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(base, "{session}"), BaseRoot: base}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "cto", "--unsafe-send-as", "--to", "user", "--kind", "status", "--subject", "gate"})
	})
	if err == nil || !strings.Contains(err.Error(), "--unsafe-send-as requires --reason") {
		t.Fatalf("unsafe send-as without reason err = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("unsafe send-as without reason should not call amq, calls = %d", len(*calls))
	}

	if _, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "cto", "--unsafe-send-as", "--reason", "recover stuck gate", "--to", "user", "--kind", "status", "--subject", "gate"})
	}); err != nil {
		t.Fatalf("unsafe send-as with reason should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	audit := filepath.Join(dir, ".amq-squad", "boundary-audit", "issue-96.jsonl")
	b, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(b), `"subcommand":"send-as"`) || !strings.Contains(string(b), "recover stuck gate") {
		t.Fatalf("audit record = %s", b)
	}

	if _, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--to", "user", "--me", "cto", "--unsafe-send-as", "--reason", "recover stuck gate again", "--kind", "status", "--subject", "gate"})
	}); err != nil {
		t.Fatalf("unsafe send-as with non-leading actor should pass: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(*calls))
	}
	got := strings.Join((*calls)[1].Arg, " ")
	if strings.Contains(got, "--me cto") {
		t.Fatalf("passthrough actor flag should be stripped before bare amq call: %s", got)
	}
	if !envHas((*calls)[1].Env, "AM_ME", "cto") {
		t.Fatalf("AM_ME=cto not injected for non-leading actor: %v", (*calls)[1].Env)
	}
	b, err = os.ReadFile(audit)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(b), "recover stuck gate again") {
		t.Fatalf("audit record missing non-leading unsafe reason = %s", b)
	}
}

func TestAMQDrainResolvesRootAndForwards(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--session", "issue-96", "--me", "lead", "--include-body", "--json"})
	})
	if err != nil {
		t.Fatalf("amq drain: %v", err)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"drain", "--root", ".agent-mail/issue-96", "--include-body", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("drain args missing %q: %s", want, got)
		}
	}
}

func TestAMQDrainAllowsMailboxOwnerInProjectTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "qa")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--session", "issue-96", "--me", "qa", "--include-body"})
	})
	if err != nil {
		t.Fatalf("owner drain should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("owner drain calls = %d, want 1", len(*calls))
	}
}

func TestAMQDrainAllowsExternalLeadMailboxInProjectTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--session", "issue-96", "--me", "cto", "--include-body"})
	})
	if err != nil {
		t.Fatalf("external lead drain should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("external lead drain calls = %d, want 1", len(*calls))
	}
}

func TestAMQDrainBlocksNonOwnerMailboxInProjectTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--session", "issue-96", "--me", "qa", "--include-body"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "refusing amq drain") ||
		!strings.Contains(err.Error(), "lead-owned mailbox") ||
		!strings.Contains(err.Error(), "list/read/thread") ||
		!strings.Contains(err.Error(), "--override-boundary --reason") {
		t.Fatalf("boundary error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("blocked drain should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQDrainBlocksNonOwnerMailboxInNamedProfile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeamProfile(t, dir, "review")
	t.Setenv("AM_ME", "cto")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--profile", "review", "--session", "issue-96", "--me", "qa", "--include-body"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "refusing amq drain") ||
		!strings.Contains(err.Error(), "lead-owned mailbox") {
		t.Fatalf("named-profile boundary error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("blocked named-profile drain should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQDrainInfersNamedProfileFromResolvedRoot(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeamProfile(t, dir, "review")
	t.Setenv("AM_ME", "cto")
	calls := withAMQCommandSeams(t, amqEnv{Root: filepath.Join(".agent-mail", "review", "issue-96"), BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--me", "qa", "--include-body"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "refusing amq drain") ||
		!strings.Contains(err.Error(), "lead-owned mailbox") {
		t.Fatalf("root-inferred named-profile boundary error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("blocked root-inferred named-profile drain should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQDrainOverrideRequiresReason(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--session", "issue-96", "--me", "qa", "--override-boundary"})
	})
	if err == nil || !strings.Contains(err.Error(), "--override-boundary requires --reason") {
		t.Fatalf("override without reason error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("missing-reason override should not call amq, calls = %d", len(*calls))
	}
}

func TestAMQDrainOverrideWritesAuditAndExecutes(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--session", "issue-96", "--me", "qa", "--override-boundary", "--reason", "recover stuck report", "--include-body"})
	})
	if err != nil {
		t.Fatalf("audited override drain should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("override drain calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	if strings.Contains(got, "override-boundary") || strings.Contains(got, "recover stuck report") {
		t.Fatalf("wrapper override flags must not be forwarded to amq: %s", got)
	}
	auditPath := filepath.Join(dir, team.DirName, "boundary-audit", "issue-96.jsonl")
	b, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"subcommand":"drain"`, `"actor":"cto"`, `"target":"qa"`, `"reason":"recover stuck report"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("audit missing %q:\n%s", want, string(b))
		}
	}
}

func TestAMQWatchBlocksNonOwnerMailboxInProjectTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")
	var streamed []string
	prev := runAMQStreaming
	runAMQStreaming = func(ctx amqContext, cmd []string) error {
		streamed = cmd
		return nil
	}
	t.Cleanup(func() { runAMQStreaming = prev })

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"watch", "--session", "issue-96", "--me", "qa", "--poll"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing amq watch") {
		t.Fatalf("watch boundary error = %v", err)
	}
	if len(streamed) != 0 {
		t.Fatalf("blocked watch should not stream, got %v", streamed)
	}
}

func writeAMQBoundaryTeam(t *testing.T, dir string) {
	t.Helper()
	writeAMQBoundaryTeamProfile(t, dir, team.DefaultProfile)
}

func writeAMQBoundaryTeamProfile(t *testing.T, dir, profile string) {
	t.Helper()
	if err := team.WriteProfile(dir, profile, team.Team{
		Project:       dir,
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectTeam,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAMQReadVerbsResolveRootAndForward(t *testing.T) {
	// An external lead inspecting the bus must hit the SESSION root, not the
	// default .agent-mail — so the read verbs are root-resolving passthroughs too.
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"thread", []string{"thread", "--session", "issue-96", "--me", "lead", "--id", "p2p/lead__qa", "--include-body"},
			[]string{"thread", "--root", ".agent-mail/issue-96", "--id p2p/lead__qa", "--include-body"}},
		{"list", []string{"list", "--session", "issue-96", "--me", "qa"},
			[]string{"list", "--root", ".agent-mail/issue-96"}},
		{"read", []string{"read", "--session", "issue-96", "--me", "qa", "--id", "msg1"},
			[]string{"read", "--root", ".agent-mail/issue-96", "--id msg1"}},
		{"reply", []string{"reply", "--session", "issue-96", "--me", "lead", "--id", "msg1", "--body", "ok"},
			[]string{"reply", "--root", ".agent-mail/issue-96", "--id msg1", "--body ok"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chdir(t, t.TempDir())
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")
			if _, _, err := captureOutput(t, func() error { return runAMQ(tc.args) }); err != nil {
				t.Fatalf("amq %s: %v", tc.name, err)
			}
			got := strings.Join((*calls)[0].Arg, " ")
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("amq %s args missing %q: %s", tc.name, want, got)
				}
			}
			if strings.Contains(got, "--session issue-96") || strings.Contains(got, "--me ") {
				t.Fatalf("resolution flags must not be forwarded: %s", got)
			}
		})
	}
}

func TestAMQReadOnlyVerbAllowsNamedProfileInspection(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeamProfile(t, dir, "review")
	t.Setenv("AM_ME", "cto")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"read", "--profile", "review", "--session", "issue-96", "--me", "qa", "--id", "msg1"})
	})
	if err != nil {
		t.Fatalf("named-profile read-only inspection should pass: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("read calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"read", filepath.Join(".agent-mail", "review", "issue-96"), "--id msg1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("named-profile read args missing %q: %s", want, got)
		}
	}
}

func TestAMQPassthroughRejectsRoot(t *testing.T) {
	chdir(t, t.TempDir())
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--root", ".agent-mail", "--to", "worker"})
	})
	if err == nil || !strings.Contains(err.Error(), "do not pass --root") {
		t.Fatalf("send --root should be rejected, got %v", err)
	}
}

func TestAMQWatchStreams(t *testing.T) {
	chdir(t, t.TempDir())
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "")
	var streamed []string
	prev := runAMQStreaming
	runAMQStreaming = func(ctx amqContext, cmd []string) error {
		streamed = cmd
		return nil
	}
	t.Cleanup(func() { runAMQStreaming = prev })

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"watch", "--session", "issue-96", "--me", "lead", "--poll"})
	})
	if err != nil {
		t.Fatalf("amq watch: %v", err)
	}
	got := strings.Join(streamed, " ")
	for _, want := range []string{"watch", "--root", ".agent-mail/issue-96", "--poll"} {
		if !strings.Contains(got, want) {
			t.Fatalf("watch streamed args missing %q: %s", want, got)
		}
	}
}

func TestSplitAMQPassthroughArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantProject string
		wantSession string
		wantMe      string
		wantSet     bool
		wantPass    []string
		wantErr     string
	}{
		{
			name:        "space form consumed, rest forwarded",
			args:        []string{"--session", "work", "--me", "lead", "--to", "worker", "--kind", "todo"},
			wantSession: "work", wantMe: "lead",
			wantPass: []string{"--to", "worker", "--kind", "todo"},
		},
		{
			name:        "equals form and single dash",
			args:        []string{"-session=work", "--me=lead", "--to=worker"},
			wantSession: "work", wantMe: "lead",
			wantPass: []string{"--to=worker"},
		},
		{
			name:        "project sets flag",
			args:        []string{"--project", "/repo", "--to", "x"},
			wantProject: "/repo", wantSet: true,
			wantPass: []string{"--to", "x"},
		},
		{
			name:        "profile consumed for wrapper resolution",
			args:        []string{"--profile", "review", "--session", "work", "--to", "x"},
			wantSession: "work",
			wantPass:    []string{"--to", "x"},
		},
		{
			name:        "terminator forwards target flags verbatim",
			args:        []string{"--session", "work", "--", "--session", "target", "--to", "codex"},
			wantSession: "work",
			wantPass:    []string{"--session", "target", "--to", "codex"},
		},
		{
			// A passthrough flag whose VALUE equals a wrapper flag name must NOT be
			// re-read as a wrapper flag: parsing stops at the first non-wrapper
			// token (--to), so --subject's value "--session" is forwarded verbatim.
			name:        "passthrough value equal to a wrapper flag is not consumed",
			args:        []string{"--session", "work", "--to", "qa", "--subject", "--session", "--body", "x"},
			wantSession: "work",
			wantPass:    []string{"--to", "qa", "--subject", "--session", "--body", "x"},
		},
		{
			// Likewise, a --root appearing AFTER the leading wrapper run is a
			// passthrough value/flag, forwarded to amq, never a false rejection.
			name:        "root after the leading run is forwarded, not rejected",
			args:        []string{"--session", "work", "--subject", "--root"},
			wantSession: "work",
			wantPass:    []string{"--subject", "--root"},
		},
		{
			name:    "root rejected in the wrapper position",
			args:    []string{"--session", "work", "--root", ".agent-mail"},
			wantErr: "do not pass --root",
		},
		{
			name:    "dangling wrapper value flag",
			args:    []string{"--me", "lead", "--session"},
			wantErr: "needs a value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, session, me, set, pass, err := splitAMQPassthroughArgs("send", tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if project != tc.wantProject || session != tc.wantSession || me != tc.wantMe || set != tc.wantSet {
				t.Fatalf("got project=%q session=%q me=%q set=%v; want %q/%q/%q/%v",
					project, session, me, set, tc.wantProject, tc.wantSession, tc.wantMe, tc.wantSet)
			}
			if strings.Join(pass, " ") != strings.Join(tc.wantPass, " ") {
				t.Fatalf("passthrough = %v, want %v", pass, tc.wantPass)
			}
		})
	}
}

func envHas(env []string, key, val string) bool {
	return containsString(env, key+"="+val)
}

func envHasPrefix(env []string, key, valSubstr string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") && strings.Contains(e, valSubstr) {
			return true
		}
	}
	return false
}

// TestSplitAMQPassthroughArgsParityFlags covers the flag-parity fixes from
// #178: --from aliases --me and --body-file rewrites to --body @<path> for
// send/reply ONLY. Both are forwarded verbatim for other verbs (drain, list…).
func TestSplitAMQPassthroughArgsParityFlags(t *testing.T) {
	cases := []struct {
		name     string
		sub      string
		args     []string
		wantMe   string
		wantPass []string
	}{
		{
			name:   "--from aliases --me for send",
			sub:    "send",
			args:   []string{"--from", "lead", "--to", "worker"},
			wantMe: "lead", wantPass: []string{"--to", "worker"},
		},
		{
			name:   "--from aliases --me for reply",
			sub:    "reply",
			args:   []string{"--from=lead", "--to", "worker"},
			wantMe: "lead", wantPass: []string{"--to", "worker"},
		},
		{
			// --from for drain is NOT a wrapper flag; stops parsing and forwards
			// --from verbatim (amq will reject it, but not our wrapper).
			name:     "--from forwarded verbatim for drain (not aliased)",
			sub:      "drain",
			args:     []string{"--from", "lead", "--me", "other"},
			wantMe:   "",
			wantPass: []string{"--from", "lead", "--me", "other"},
		},
		{
			name:     "--body-file rewrites to --body @path for send",
			sub:      "send",
			args:     []string{"--body-file", "/tmp/msg.txt", "--to", "worker"},
			wantPass: []string{"--body", "@/tmp/msg.txt", "--to", "worker"},
		},
		{
			name:     "--body-file - rewrites to --body - (stdin) for send",
			sub:      "send",
			args:     []string{"--body-file", "-", "--to", "worker"},
			wantPass: []string{"--body", "-", "--to", "worker"},
		},
		{
			name:     "--body-file= inline rewrite for reply",
			sub:      "reply",
			args:     []string{"--body-file=/tmp/msg.txt", "--to", "worker"},
			wantPass: []string{"--body", "@/tmp/msg.txt", "--to", "worker"},
		},
		{
			// --body-file after --to (real-world shape): leading scan stops at --to,
			// but normalizeBodyFileFlag post-processes the full passthrough.
			name:     "--body-file after --to rewrites for send",
			sub:      "send",
			args:     []string{"--session", "work", "--to", "worker", "--body-file", "-"},
			wantPass: []string{"--to", "worker", "--body", "-"},
		},
		{
			// --body-file for list is NOT rewritten; forwarded verbatim.
			name:     "--body-file forwarded verbatim for list (not rewritten)",
			sub:      "list",
			args:     []string{"--body-file", "/tmp/f"},
			wantPass: []string{"--body-file", "/tmp/f"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, me, _, pass, err := splitAMQPassthroughArgs(tc.sub, tc.args)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if me != tc.wantMe {
				t.Fatalf("me = %q, want %q", me, tc.wantMe)
			}
			if strings.Join(pass, " ") != strings.Join(tc.wantPass, " ") {
				t.Fatalf("passthrough = %v, want %v", pass, tc.wantPass)
			}
		})
	}
}

func TestPassthroughNeedsStdin(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--to", "worker", "--body", "-"}, true},
		{[]string{"--body=-"}, true},
		{[]string{"--to", "worker", "--body", "@/tmp/file"}, false},
		{[]string{"--to", "worker"}, false},
		// value "--body" at end (no value) should not panic
		{[]string{"--body"}, false},
	}
	for _, tc := range cases {
		if got := passthroughNeedsStdin(tc.args); got != tc.want {
			t.Errorf("passthroughNeedsStdin(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// TestAMQPassthroughSendForwardsStdin covers both stdin paths (#178):
// --body - (explicit stdin flag) and --body-file - (rewritten to --body -).
func TestAMQPassthroughSendForwardsStdin(t *testing.T) {
	chdir(t, t.TempDir())

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"--body -", []string{"send", "--session", "work", "--me", "lead", "--to", "worker", "--kind", "status", "--body", "-"}},
		// --body-file before --to (leading position, same result).
		{"--body-file - (leading)", []string{"send", "--session", "work", "--me", "lead", "--body-file", "-", "--to", "worker", "--kind", "status"}},
		// --body-file after --to: real-world shape; normalizeBodyFileFlag rewrites
		// it in the full passthrough even though leading scan stopped at --to.
		{"--body-file - (after --to)", []string{"send", "--session", "work", "--me", "lead", "--to", "worker", "--kind", "status", "--body-file", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capturedReq amqCommandRequest
			_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/work", BaseRoot: ".agent-mail"}, "msg-001\n")
			prevRun := runAMQCommand
			runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
				capturedReq = req
				return prevRun(req)
			}
			t.Cleanup(func() { runAMQCommand = prevRun })

			_, _, err := captureOutput(t, func() error { return runAMQ(tc.args) })
			if err != nil {
				t.Fatalf("amq send: %v", err)
			}
			if capturedReq.Stdin == nil {
				t.Errorf("stdin should be forwarded for %s", tc.name)
			}
		})
	}
}
