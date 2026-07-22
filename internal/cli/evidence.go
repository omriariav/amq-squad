package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/commandevidence"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

const (
	evidenceLookupScanCap = 10000
	evidenceLookupBodyCap = 64 << 10
	evidenceRenderBodyCap = 4 << 10
	evidenceResultLimit   = 100
	evidenceReplicaCap    = 8
	evidenceWarningCap    = 100
)

type evidenceRunData struct {
	TaskID string                 `json:"task_id"`
	Result commandevidence.Result `json:"result"`
	Linked bool                   `json:"linked"`
	Report evidenceReport         `json:"report"`
}

type evidenceReport struct {
	State     string `json:"state"`
	To        string `json:"to,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type evidenceLookupRow struct {
	MessageID      string    `json:"message_id"`
	ContentSHA256  string    `json:"content_sha256"`
	Conflict       bool      `json:"conflict,omitempty"`
	ReplicaCount   int       `json:"replica_count"`
	StructuredTask bool      `json:"structured_task_context,omitempty"`
	From           string    `json:"from,omitempty"`
	To             []string  `json:"to,omitempty"`
	Thread         string    `json:"thread,omitempty"`
	Subject        string    `json:"subject,omitempty"`
	Body           string    `json:"body,omitempty"`
	Created        time.Time `json:"created,omitempty"`
	Paths          []string  `json:"paths,omitempty"`
}

type evidenceLookupData struct {
	TaskID       string              `json:"task_id"`
	Total        int                 `json:"total"`
	Returned     int                 `json:"returned"`
	Rows         []evidenceLookupRow `json:"rows"`
	ScanWarnings []state.Warning     `json:"scan_warnings,omitempty"`
}

type evidenceAttemptRow struct {
	AttemptID         string    `json:"attempt_id"`
	Subject           string    `json:"subject"`
	StartedAt         time.Time `json:"started_at"`
	ProcessState      string    `json:"process_state"`
	FinalizationState string    `json:"finalization_state"`
	Classification    string    `json:"classification"`
	ManifestPath      string    `json:"manifest_path"`
	ManifestSHA256    string    `json:"manifest_sha256"`
	OutcomePath       string    `json:"outcome_path,omitempty"`
	OutcomeSHA256     string    `json:"outcome_sha256,omitempty"`
	SummaryPath       string    `json:"summary_path,omitempty"`
	SummarySHA256     string    `json:"summary_sha256,omitempty"`
	FindingCount      int       `json:"finding_count"`
}

func runEvidence(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad evidence - immutable task-scoped command evidence

Usage:
  amq-squad evidence run TASK [flags] -- COMMAND [ARG...]
  amq-squad evidence show TASK ATTEMPT [scope flags] [--json]
  amq-squad evidence list TASK [scope flags] [--limit N] [--json]
  amq-squad evidence recover TASK ATTEMPT --me ACTOR [scope flags] [--json]
  amq-squad evidence lookup TASK [scope flags] [filters] [--json]

run requires --me and --subject. It executes without a shell, records only the
fixed baseline plus explicit --env/--pass-env values, and links an immutable
outcome to the exact active task before sending a bounded report on the task's
recorded dispatch route.
`)
		return nil
	}
	switch args[0] {
	case "run":
		return runEvidenceRun(args[1:])
	case "show":
		return runEvidenceShow(args[1:])
	case "list":
		return runEvidenceList(args[1:])
	case "recover":
		return runEvidenceRecover(args[1:])
	case "lookup":
		return runEvidenceLookup(args[1:])
	default:
		return usageErrorf("unknown evidence subcommand %q; use run, show, list, recover, or lookup", args[0])
	}
}

func runEvidenceRun(args []string) error {
	dash := -1
	for i, arg := range args {
		if arg == "--" {
			dash = i
			break
		}
	}
	if dash < 0 || dash+1 >= len(args) || dash == 0 {
		return usageErrorf("evidence run requires TASK [flags] -- COMMAND [ARG...]")
	}
	taskID, flagArgs, argv := args[0], args[1:dash], append([]string(nil), args[dash+1:]...)
	fs := flag.NewFlagSet("evidence run", flag.ContinueOnError)
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile namespace")
	session := fs.String("session", "", "workstream/session")
	actor := fs.String("me", "", "active task actor")
	subject := fs.String("subject", "", "bounded evidence subject")
	attemptID := fs.String("attempt-id", "", "explicit idempotency key")
	retryOf := fs.String("retry-of", "", "parent attempt id")
	cwdFlag := fs.String("cwd", "", "command cwd inside project (default project root)")
	seed := fs.String("seed", "", "exact diagnostic/test seed")
	jsonOut := fs.Bool("json", false, "emit JSON envelope")
	noReport := fs.Bool("no-report", false, "do not send the bounded dispatch-derived AMQ report")
	var envFlags, passEnvFlags, redactArgFlags stringListFlag
	fs.Var(&envFlags, "env", "explicit non-secret NAME=VALUE child environment (repeatable)")
	fs.Var(&passEnvFlags, "pass-env", "copy one ambient NAME into the child; secret-class names auto-redact (repeatable)")
	fs.Var(&redactArgFlags, "redact-arg", "zero-based argv index to redact in evidence (repeatable)")
	if err := parseFlags(fs, flagArgs); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*actor) == "" || strings.TrimSpace(*subject) == "" {
		return usageErrorf("evidence run requires TASK, --me, --subject, and -- COMMAND")
	}
	selected, err := selectEvidenceTaskForMutation(taskID, *session, *project, *profile, fs)
	if err != nil {
		return err
	}
	request, err := buildEvidenceRequest(selected, strings.TrimSpace(*actor), strings.TrimSpace(*subject), *attemptID, *retryOf, *cwdFlag, *seed, argv, envFlags, passEnvFlags, redactArgFlags)
	if err != nil {
		return err
	}
	store, err := commandevidence.NewStore(selected.ProjectDir, selected.Profile, selected.Session)
	if err != nil {
		return err
	}
	if request.AttemptID != "" {
		if existing, found, err := store.CheckExisting(request); found || err != nil {
			if err != nil {
				return err
			}
			return renderEvidenceRun(evidenceRunData{TaskID: selected.Task.ID, Result: existing, Report: evidenceReport{State: "not_repeated"}}, *jsonOut)
		}
	}
	if err := validateEvidenceActor(selected, strings.TrimSpace(*actor)); err != nil {
		return err
	}
	current, err := revalidateTaskSelection(selected)
	if err != nil {
		return err
	}
	if err := validateEvidenceActor(current, strings.TrimSpace(*actor)); err != nil {
		return err
	}
	request.TaskSHA256 = "sha256:" + current.FileSHA256
	result, runErr := store.Run(request)
	data := evidenceRunData{TaskID: current.Task.ID, Result: result, Report: evidenceReport{State: "not_sent"}}
	if result.Outcome == nil || result.Summary == nil {
		return runErr
	}
	if result.Outcome.SubjectMutation != "" {
		return fmt.Errorf("immutable command outcome records subject mutation and cannot be accepted as task evidence: %s", result.Outcome.SubjectMutation)
	}
	_, linked, linkErr := taskstore.LinkCommandEvidenceForProfile(current.ProjectDir, current.Profile, current.Session, current.Task.ID, current.FileSHA256, strings.TrimSpace(*actor), evidenceTaskLink(result, strings.TrimSpace(*actor)), time.Now().UTC())
	data.Linked = linked
	if linkErr != nil {
		return fmt.Errorf("immutable command outcome %s exists but task link failed: %w", result.Manifest.AttemptID, linkErr)
	}
	if *noReport {
		data.Report.State = "suppressed"
	} else {
		data.Report, err = sendEvidenceReport(current, strings.TrimSpace(*actor), result)
		if err != nil {
			_ = renderEvidenceRun(data, *jsonOut)
			return fmt.Errorf("immutable command evidence persisted and linked; report failed separately: %w", err)
		}
	}
	if err := renderEvidenceRun(data, *jsonOut); err != nil {
		return err
	}
	return runErr
}

func runEvidenceShow(args []string) error {
	if len(args) < 2 {
		return usageErrorf("evidence show requires TASK ATTEMPT")
	}
	taskID, attemptID := args[0], args[1]
	fs, project, profile, session, jsonOut := evidenceReadFlags("evidence show")
	if err := parseFlags(fs, args[2:]); err != nil {
		return err
	}
	selected, err := selectEvidenceTaskForMutation(taskID, *session, *project, *profile, fs)
	if err != nil {
		return err
	}
	store, err := commandevidence.NewStore(selected.ProjectDir, selected.Profile, selected.Session)
	if err != nil {
		return err
	}
	result, err := store.Read(selected.Task.ID, attemptID)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("evidence_show", evidenceAttemptProjection(result))
	}
	fmt.Printf("Evidence %s task=%s process=%s finalization=%s summary=%s\n", result.Manifest.AttemptID, result.Manifest.TaskID, evidenceProcessState(result), evidenceFinalizationState(result), result.SummaryPath)
	return nil
}

func runEvidenceList(args []string) error {
	if len(args) < 1 {
		return usageErrorf("evidence list requires TASK")
	}
	taskID := args[0]
	fs, project, profile, session, jsonOut := evidenceReadFlags("evidence list")
	limit := fs.Int("limit", 50, "maximum concise attempts (1-100)")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if *limit < 1 || *limit > evidenceResultLimit {
		return usageErrorf("evidence list --limit must be between 1 and %d", evidenceResultLimit)
	}
	selected, err := selectEvidenceTaskForMutation(taskID, *session, *project, *profile, fs)
	if err != nil {
		return err
	}
	store, err := commandevidence.NewStore(selected.ProjectDir, selected.Profile, selected.Session)
	if err != nil {
		return err
	}
	results, err := store.List(selected.Task.ID, *limit)
	if err != nil {
		return err
	}
	if *jsonOut {
		rows := make([]evidenceAttemptRow, 0, len(results))
		for _, result := range results {
			rows = append(rows, evidenceAttemptProjection(result))
		}
		return printJSONEnvelope("evidence_list", struct {
			TaskID string               `json:"task_id"`
			Rows   []evidenceAttemptRow `json:"rows"`
		}{selected.Task.ID, rows})
	}
	for _, result := range results {
		fmt.Printf("%s\t%s\t%s\t%s\n", result.Manifest.AttemptID, result.Manifest.Subject, evidenceProcessState(result), evidenceFinalizationState(result))
	}
	return nil
}

func runEvidenceRecover(args []string) error {
	if len(args) < 2 {
		return usageErrorf("evidence recover requires TASK ATTEMPT --me ACTOR")
	}
	taskID, attemptID := args[0], args[1]
	fs := flag.NewFlagSet("evidence recover", flag.ContinueOnError)
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile namespace")
	session := fs.String("session", "", "workstream/session")
	actor := fs.String("me", "", "active task actor")
	jsonOut := fs.Bool("json", false, "emit JSON envelope")
	if err := parseFlags(fs, args[2:]); err != nil {
		return err
	}
	selected, err := selectEvidenceTaskForMutation(taskID, *session, *project, *profile, fs)
	if err != nil {
		return err
	}
	if err := validateEvidenceActor(selected, strings.TrimSpace(*actor)); err != nil {
		return err
	}
	store, err := commandevidence.NewStore(selected.ProjectDir, selected.Profile, selected.Session)
	if err != nil {
		return err
	}
	result, recoverErr := store.Recover(selected.Task.ID, attemptID, time.Now().UTC())
	if result.Outcome != nil && result.Summary != nil {
		_, _, linkErr := taskstore.LinkCommandEvidenceForProfile(selected.ProjectDir, selected.Profile, selected.Session, selected.Task.ID, selected.FileSHA256, strings.TrimSpace(*actor), evidenceTaskLink(result, strings.TrimSpace(*actor)), time.Now().UTC())
		if linkErr != nil {
			return linkErr
		}
	}
	if *jsonOut {
		if err := printJSONEnvelope("evidence_recover", result); err != nil {
			return err
		}
	} else {
		fmt.Printf("Evidence recovery %s: %s\n", attemptID, result.Classification)
	}
	return recoverErr
}

func runEvidenceLookup(args []string) error {
	if len(args) < 1 {
		return usageErrorf("evidence lookup requires TASK")
	}
	taskID := args[0]
	fs, project, profile, session, jsonOut := evidenceReadFlags("evidence lookup")
	limit := fs.Int("limit", 50, "maximum rows after filter/sort (1-100)")
	subjectFilter := fs.String("subject", "", "case-insensitive subject substring")
	bodyPattern := fs.String("body-pattern", "", "bounded RE2 body pattern")
	sinceText := fs.String("since", "", "RFC3339 lower bound")
	subjectsOnly := fs.Bool("subjects-only", false, "omit message bodies")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if *limit < 1 || *limit > evidenceResultLimit || len(*bodyPattern) > 256 || len(*subjectFilter) > 512 {
		return usageErrorf("lookup bounds exceeded")
	}
	selected, err := selectEvidenceTaskForMutation(taskID, *session, *project, *profile, fs)
	if err != nil {
		return err
	}
	var since time.Time
	if strings.TrimSpace(*sinceText) != "" {
		since, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(*sinceText))
		if err != nil {
			return usageErrorf("invalid --since: %v", err)
		}
	}
	var pattern *regexp.Regexp
	if *bodyPattern != "" {
		pattern, err = regexp.Compile(*bodyPattern)
		if err != nil {
			return usageErrorf("invalid --body-pattern: %v", err)
		}
	}
	data, err := lookupTaskMessages(selected, *limit, *subjectFilter, pattern, since, *subjectsOnly)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("evidence_lookup", data)
	}
	for _, row := range data.Rows {
		marker := ""
		if row.Conflict {
			marker = " CONFLICT"
		}
		fmt.Printf("%s\t%s\t%s%s\n", row.MessageID, row.Created.Format(time.RFC3339Nano), row.Subject, marker)
	}
	return nil
}

func evidenceReadFlags(name string) (*flag.FlagSet, *string, *string, *string, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile namespace")
	session := fs.String("session", "", "workstream/session")
	jsonOut := fs.Bool("json", false, "emit JSON envelope")
	return fs, project, profile, session, jsonOut
}

func buildEvidenceRequest(selected taskSelection, actor, subject, attemptID, retryOf, cwdFlag, seed string, argv []string, envFlags, passEnvFlags, redactArgFlags []string) (commandevidence.Request, error) {
	cwd := selected.ProjectDir
	if strings.TrimSpace(cwdFlag) != "" {
		var err error
		cwd, err = filepath.Abs(filepath.Clean(cwdFlag))
		if err != nil {
			return commandevidence.Request{}, err
		}
	}
	resolvedCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return commandevidence.Request{}, fmt.Errorf("resolve command cwd: %w", err)
	}
	environment, err := evidenceEnvironment(envFlags, passEnvFlags)
	if err != nil {
		return commandevidence.Request{}, err
	}
	executable, err := resolveEvidenceExecutable(argv[0], resolvedCWD, evidencePathValue(environment))
	if err != nil {
		return commandevidence.Request{}, err
	}
	b, err := os.ReadFile(executable)
	if err != nil {
		return commandevidence.Request{}, fmt.Errorf("hash resolved executable: %w", err)
	}
	redacted := map[int]bool{}
	for _, raw := range redactArgFlags {
		index, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || index < 0 || index >= len(argv) {
			return commandevidence.Request{}, usageErrorf("invalid --redact-arg %q", raw)
		}
		redacted[index] = true
	}
	commandSubject, err := commandevidence.ResolveCommandSubject(resolvedCWD, argv)
	if err != nil {
		return commandevidence.Request{}, fmt.Errorf("resolve command subject: %w", err)
	}
	return commandevidence.Request{
		ProjectDir: selected.ProjectDir, Profile: selected.Profile, Session: selected.Session,
		NamespaceID: selected.Namespace.ID, TaskID: selected.Task.ID, TaskPath: selected.TaskPath,
		TaskSHA256: "sha256:" + selected.FileSHA256, Actor: actor, Subject: subject,
		Argv: argv, ArgvEvidence: commandevidence.BuildArguments(argv, redacted),
		Executable: executable, ExecutableSHA256: "sha256:" + hex.EncodeToString(sha256Bytes(b)),
		CWD: resolvedCWD, CommandSubject: commandSubject, Environment: environment, StartedAt: time.Now().UTC(), Seed: seed,
		GitHead: commandSubject.GitHead, AttemptID: strings.TrimSpace(attemptID), RetryOf: strings.TrimSpace(retryOf),
	}, nil
}

func evidenceEnvironment(envFlags, passEnvFlags []string) ([]commandevidence.EnvironmentEntry, error) {
	values, redacted, sources := map[string]string{}, map[string]bool{}, map[string]string{}
	for _, raw := range envFlags {
		name, value, ok := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || evidenceSecretName(name) {
			return nil, usageErrorf("--env requires non-secret NAME=VALUE; use --pass-env for secret-class names")
		}
		if _, exists := values[name]; exists {
			return nil, usageErrorf("duplicate environment name %s", name)
		}
		values[name], sources[name] = value, "pass_env"
	}
	for _, raw := range passEnvFlags {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, usageErrorf("--pass-env requires NAME")
		}
		if _, exists := values[name]; exists {
			return nil, usageErrorf("duplicate environment name %s", name)
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil, usageErrorf("--pass-env %s is not set", name)
		}
		values[name], sources[name], redacted[name] = value, "pass_env", evidenceSecretName(name)
	}
	baselineNames := []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TZ", "USER", "LOGNAME", "SHELL", "GOCACHE", "GOMODCACHE", "GOPATH", "XDG_CACHE_HOME"}
	baselineValues := map[string]string{}
	for _, name := range baselineNames {
		if _, exists := values[name]; exists {
			return nil, usageErrorf("baseline environment %s cannot be overridden", name)
		}
		if value, ok := os.LookupEnv(name); ok {
			baselineValues[name] = value
		}
	}
	baseline, err := commandevidence.BuildBaselineEnvironment(baselineValues)
	if err != nil {
		return nil, err
	}
	explicit := commandevidence.BuildEnvironment(values, redacted)
	for i := range explicit {
		explicit[i].Source = sources[explicit[i].Name]
	}
	return append(baseline, explicit...), nil
}

func resolveEvidenceExecutable(name, cwd, recordedPath string) (string, error) {
	var candidate string
	if strings.ContainsRune(name, filepath.Separator) {
		candidate = name
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cwd, candidate)
		}
	} else {
		if recordedPath == "" {
			return "", fmt.Errorf("resolve executable %q: recorded PATH is empty", name)
		}
		for _, dir := range filepath.SplitList(recordedPath) {
			if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
				return "", fmt.Errorf("recorded PATH contains relative or non-canonical entry %q", dir)
			}
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err == nil && info.Mode().IsRegular() && (info.Mode().Perm()&0o111 != 0) {
				candidate = path
				break
			}
		}
		if candidate == "" {
			return "", fmt.Errorf("resolve executable %q from recorded PATH", name)
		}
	}
	abs, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve executable identity: %w", err)
	}
	return resolved, nil
}

func evidencePathValue(environment []commandevidence.EnvironmentEntry) string {
	for _, entry := range environment {
		if entry.Name == "PATH" {
			return entry.RuntimeValue
		}
	}
	return ""
}

// Command-evidence records use one canonical project identity so an accepted
// task path cannot differ from its immutable store merely because the caller
// supplied a filesystem alias (notably /var versus /private/var on macOS).
// Re-reading through the resolved root also preserves the exact task-byte
// binding used by the later compare-and-swap link.
func selectEvidenceTaskForMutation(id, sessionFlag, projectFlag, profileFlag string, fs *flag.FlagSet) (taskSelection, error) {
	selected, err := selectTaskForMutation(id, sessionFlag, projectFlag, profileFlag, fs)
	if err != nil {
		return taskSelection{}, err
	}
	realProject, err := filepath.EvalSymlinks(selected.ProjectDir)
	if err != nil {
		return taskSelection{}, fmt.Errorf("resolve evidence project identity: %w", err)
	}
	realProject, err = filepath.Abs(filepath.Clean(realProject))
	if err != nil {
		return taskSelection{}, fmt.Errorf("resolve evidence project identity: %w", err)
	}
	if realProject == selected.ProjectDir {
		return selected, nil
	}
	return readTaskSelection(realProject, selected.Profile, selected.Session, selected.Task.ID)
}

func validateEvidenceActor(selected taskSelection, actor string) error {
	if actor == "" || selected.Task.Status != taskstore.StatusInProgress || strings.TrimSpace(selected.Task.AssignedTo) != actor {
		return fmt.Errorf("task %s must be in_progress for active assignee %s", selected.Task.ID, actor)
	}
	if authority := taskstore.AuthorityActor(selected.Task); authority != "" && authority != actor {
		return fmt.Errorf("task %s authority actor is %s, not %s", selected.Task.ID, authority, actor)
	}
	return validateTaskSelectionNamespace(selected)
}

func evidenceTaskLink(result commandevidence.Result, actor string) taskstore.CommandEvidenceLink {
	return taskstore.CommandEvidenceLink{
		AttemptID: result.Manifest.AttemptID, Actor: actor, Subject: evidenceTruncate(result.Manifest.Subject, 240),
		ProcessState: evidenceProcessState(result), FinalizationState: evidenceFinalizationState(result),
		ManifestPath: result.ManifestPath, ManifestSHA256: result.ManifestSHA256,
		OutcomePath: result.OutcomePath, OutcomeSHA256: result.Outcome.OutcomeSHA256,
		SummaryPath: result.SummaryPath, SummarySHA256: result.Summary.SummarySHA256,
	}
}

func sendEvidenceReport(selected taskSelection, actor string, result commandevidence.Result) (evidenceReport, error) {
	report := evidenceReport{State: "not_configured"}
	dispatch := selected.Task.Dispatch
	if dispatch == nil || strings.TrimSpace(dispatch.Sender) == "" || strings.TrimSpace(dispatch.Thread) == "" {
		return report, nil
	}
	if strings.TrimSpace(dispatch.Assignee) != actor {
		return report, fmt.Errorf("task dispatch assignee %s does not match evidence actor %s", dispatch.Assignee, actor)
	}
	report.To, report.Thread = strings.TrimSpace(dispatch.Sender), strings.TrimSpace(dispatch.Thread)
	ctx, err := resolveAMQContextForNamespace(selected.ProjectDir, selected.Profile, selected.Session, actor)
	if err != nil {
		report.State, report.Error = "failed", err.Error()
		return report, err
	}
	subject := evidenceTruncate(fmt.Sprintf("EVIDENCE: %s %s", selected.Task.ID, result.Manifest.AttemptID), 240)
	body := evidenceTruncate(fmt.Sprintf("task=%s attempt=%s process=%s finalization=%s summary=%s summary_sha256=%s", selected.Task.ID, result.Manifest.AttemptID, evidenceProcessState(result), evidenceFinalizationState(result), result.SummaryPath, result.Summary.SummarySHA256), 1000)
	args := dispatchSendArgs(ctx.Root, actor, report.To, report.Thread, "status", subject, body, "", "", 0)
	contextJSON, err := json.Marshal(map[string]any{
		"task_id":    selected.Task.ID,
		"attempt_id": result.Manifest.AttemptID,
		"evidence": map[string]string{
			"summary_path":   result.SummaryPath,
			"summary_sha256": result.Summary.SummarySHA256,
		},
	})
	if err != nil || len(contextJSON) > 4096 {
		return report, fmt.Errorf("build bounded evidence report context")
	}
	args = append(args, "--context", string(contextJSON))
	out, err := runAMQCommand(amqCommandRequest{Dir: selected.ProjectDir, Env: amqCommandEnv(ctx), Arg: args})
	if err != nil {
		report.State, report.Error = "failed", evidenceTruncate(err.Error(), 1000)
		return report, err
	}
	report.State, report.MessageID = "sent", parseSentMessageID(string(out))
	return report, nil
}

func lookupTaskMessages(selected taskSelection, limit int, subjectFilter string, pattern *regexp.Regexp, since time.Time, subjectsOnly bool) (evidenceLookupData, error) {
	messages, warnings := state.ScanSessionMessages(selected.Namespace.AMQRoot, time.Now)
	if len(messages) > evidenceLookupScanCap {
		return evidenceLookupData{}, fmt.Errorf("AMQ lookup exceeds %d-message scan cap", evidenceLookupScanCap)
	}
	type candidate struct {
		message    state.Message
		digest     string
		structured bool
	}
	groups := map[string]map[string][]candidate{}
	for _, message := range messages {
		matches, structured := evidenceMessageMatchesTask(message, selected.Task.ID)
		if !matches || !since.IsZero() && message.Created.Before(since) || subjectFilter != "" && !strings.Contains(strings.ToLower(message.RawSubject), strings.ToLower(subjectFilter)) {
			continue
		}
		body := evidenceTruncate(message.RawBody, evidenceLookupBodyCap)
		if pattern != nil && !pattern.MatchString(body) {
			continue
		}
		digest, err := canonicalTaskMessageSHA256(message)
		if err != nil {
			return evidenceLookupData{}, err
		}
		if groups[message.ID] == nil {
			groups[message.ID] = map[string][]candidate{}
		}
		groups[message.ID][digest] = append(groups[message.ID][digest], candidate{message, digest, structured})
	}
	rows := []evidenceLookupRow{}
	for id, digests := range groups {
		for digest, replicas := range digests {
			sort.Slice(replicas, func(i, j int) bool { return replicas[i].message.Path < replicas[j].message.Path })
			chosen := replicas[0]
			for _, replica := range replicas {
				if replica.structured {
					chosen = replica
					break
				}
			}
			paths := make([]string, 0, min(len(replicas), evidenceReplicaCap))
			for _, replica := range replicas {
				if len(paths) == evidenceReplicaCap {
					break
				}
				paths = append(paths, evidenceTruncate(replica.message.Path, 1024))
			}
			body := ""
			if !subjectsOnly {
				body = evidenceTruncate(chosen.message.RawBody, evidenceRenderBodyCap)
			}
			rows = append(rows, evidenceLookupRow{MessageID: id, ContentSHA256: digest, Conflict: len(digests) > 1, ReplicaCount: len(replicas), StructuredTask: chosen.structured, From: chosen.message.From, To: append([]string(nil), chosen.message.To...), Thread: chosen.message.RawThread, Subject: evidenceTruncate(chosen.message.RawSubject, 512), Body: body, Created: chosen.message.Created, Paths: paths})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Created.Equal(rows[j].Created) {
			return rows[i].Created.After(rows[j].Created)
		}
		if rows[i].MessageID != rows[j].MessageID {
			return rows[i].MessageID < rows[j].MessageID
		}
		return rows[i].ContentSHA256 < rows[j].ContentSHA256
	})
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	if len(warnings) > evidenceWarningCap {
		warnings = warnings[:evidenceWarningCap]
	}
	for i := range warnings {
		warnings[i].Path = evidenceTruncate(warnings[i].Path, 1024)
		warnings[i].Reason = evidenceTruncate(warnings[i].Reason, 512)
	}
	return evidenceLookupData{TaskID: selected.Task.ID, Total: total, Returned: len(rows), Rows: rows, ScanWarnings: warnings}, nil
}

func evidenceMessageMatchesTask(message state.Message, taskID string) (bool, bool) {
	if message.Context != nil {
		if raw, exists := message.Context["task_id"]; exists {
			value, ok := raw.(string)
			return ok && strings.TrimSpace(value) == taskID, true
		}
	}
	if strings.TrimSpace(message.ExternalTaskID) != "" {
		return strings.TrimSpace(message.ExternalTaskID) == taskID, true
	}
	return messageContainsExactTaskID(message, taskID), false
}

func renderEvidenceRun(data evidenceRunData, jsonOut bool) error {
	if jsonOut {
		return printJSONEnvelope("evidence_run", data)
	}
	fmt.Printf("Evidence %s task=%s process=%s finalization=%s linked=%t report=%s summary=%s\n", data.Result.Manifest.AttemptID, data.TaskID, evidenceProcessState(data.Result), evidenceFinalizationState(data.Result), data.Linked, data.Report.State, data.Result.SummaryPath)
	return nil
}

func evidenceAttemptProjection(result commandevidence.Result) evidenceAttemptRow {
	row := evidenceAttemptRow{
		AttemptID: result.Manifest.AttemptID, Subject: evidenceTruncate(result.Manifest.Subject, 512),
		StartedAt: result.Manifest.StartedAt, ProcessState: evidenceProcessState(result),
		FinalizationState: evidenceFinalizationState(result), Classification: result.Classification,
		ManifestPath: evidenceTruncate(result.ManifestPath, 4096), ManifestSHA256: result.ManifestSHA256,
		OutcomePath: evidenceTruncate(result.OutcomePath, 4096), SummaryPath: evidenceTruncate(result.SummaryPath, 4096),
		FindingCount: len(result.Findings),
	}
	if result.Outcome != nil {
		row.OutcomeSHA256 = result.Outcome.OutcomeSHA256
	}
	if result.Summary != nil {
		row.SummarySHA256 = result.Summary.SummarySHA256
	}
	return row
}

func evidenceProcessState(result commandevidence.Result) string {
	if result.Process == nil {
		return "unknown"
	}
	return result.Process.State
}
func evidenceFinalizationState(result commandevidence.Result) string {
	if result.Outcome == nil {
		return "incomplete"
	}
	return result.Outcome.FinalizationState
}
func evidenceTruncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && value[limit]&0xc0 == 0x80 {
		limit--
	}
	return value[:limit]
}
func evidenceSecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY", "API_KEY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
func sha256Bytes(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}
func evidenceGitHead(projectDir string) string {
	cmd := exec.Command("git", "-C", projectDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(out))
	if len(value) != 40 && len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return strings.ToLower(value)
}
