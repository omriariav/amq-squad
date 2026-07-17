// Package commandevidence persists immutable, task-scoped command evidence.
// Commands are invoked directly from an argv vector; this package never uses a
// shell and never inherits an ambient environment implicitly.
package commandevidence

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	SchemaVersion     = 1
	SummaryMaxBytes   = 16 << 10
	TaskLinkMaxBytes  = 4 << 10
	manifestMaxBytes  = 1 << 20
	recordMaxBytes    = 1 << 20
	maxActorBytes     = 128
	maxSubjectBytes   = 1000
	maxArguments      = 1024
	maxArgumentBytes  = 1 << 20
	maxEnvironment    = 256
	maxEnvValueBytes  = 1 << 20
	maxProjection     = 8 << 20
	maxAttemptEntries = 1024
	maxFindings       = 256
	findingMaxBytes   = 8 << 10
	fileMode          = 0o600
	dirMode           = 0o700
	stdoutSpool       = "stdout.spool"
	stderrSpool       = "stderr.spool"
)

type EnvironmentEntry struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	Value        string `json:"value,omitempty"`
	Redacted     bool   `json:"redacted,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	ByteCount    int64  `json:"byte_count,omitempty"`
	RuntimeValue string `json:"-"`
}

type Argument struct {
	Index     int    `json:"index"`
	Value     string `json:"value,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	ByteCount int64  `json:"byte_count,omitempty"`
}

type Manifest struct {
	SchemaVersion          int                `json:"schema_version"`
	AttemptID              string             `json:"attempt_id"`
	NamespaceID            string             `json:"namespace_id"`
	Profile                string             `json:"profile"`
	Session                string             `json:"session"`
	TaskID                 string             `json:"task_id"`
	TaskPath               string             `json:"task_path"`
	TaskSHA256             string             `json:"task_sha256"`
	Actor                  string             `json:"actor"`
	Subject                string             `json:"subject"`
	Argv                   []Argument         `json:"argv"`
	Executable             string             `json:"executable"`
	ExecutableSHA256       string             `json:"executable_sha256,omitempty"`
	ExecutableUnverifiable string             `json:"executable_unverifiable,omitempty"`
	CWD                    string             `json:"cwd"`
	Environment            []EnvironmentEntry `json:"environment"`
	StartedAt              time.Time          `json:"started_at"`
	Seed                   string             `json:"seed,omitempty"`
	GitHead                string             `json:"git_head,omitempty"`
	RetryOf                string             `json:"retry_of,omitempty"`
	StdoutSpool            string             `json:"stdout_spool"`
	StderrSpool            string             `json:"stderr_spool"`
	RequestSHA256          string             `json:"request_sha256"`
}

type ProcessRecord struct {
	SchemaVersion  int       `json:"schema_version"`
	AttemptID      string    `json:"attempt_id"`
	RequestSHA256  string    `json:"request_sha256"`
	State          string    `json:"state"`
	EndedAt        time.Time `json:"ended_at"`
	ElapsedNanos   int64     `json:"elapsed_nanos"`
	ExitCode       *int      `json:"exit_code,omitempty"`
	Signal         string    `json:"signal,omitempty"`
	TransportError string    `json:"transport_error,omitempty"`
	CaptureErrors  []string  `json:"capture_errors,omitempty"`
	RecordSHA256   string    `json:"record_sha256"`
}

type ArtifactRef struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
}

type Outcome struct {
	SchemaVersion      int           `json:"schema_version"`
	AttemptID          string        `json:"attempt_id"`
	RequestSHA256      string        `json:"request_sha256"`
	Process            ProcessRecord `json:"process"`
	FinalizationState  string        `json:"finalization_state"`
	FinalizationErrors []string      `json:"finalization_errors,omitempty"`
	Stdout             *ArtifactRef  `json:"stdout,omitempty"`
	Stderr             *ArtifactRef  `json:"stderr,omitempty"`
	OutcomeSHA256      string        `json:"outcome_sha256"`
}

type Summary struct {
	SchemaVersion     int          `json:"schema_version"`
	AttemptID         string       `json:"attempt_id"`
	TaskID            string       `json:"task_id"`
	Subject           string       `json:"subject"`
	ProcessState      string       `json:"process_state"`
	FinalizationState string       `json:"finalization_state"`
	ManifestPath      string       `json:"manifest_path"`
	ManifestSHA256    string       `json:"manifest_sha256"`
	OutcomePath       string       `json:"outcome_path"`
	OutcomeSHA256     string       `json:"outcome_sha256"`
	Stdout            *ArtifactRef `json:"stdout,omitempty"`
	Stderr            *ArtifactRef `json:"stderr,omitempty"`
	SummarySHA256     string       `json:"summary_sha256"`
}

type RecoveryFinding struct {
	SchemaVersion int       `json:"schema_version"`
	AttemptID     string    `json:"attempt_id"`
	At            time.Time `json:"at"`
	Kind          string    `json:"kind"`
	Detail        string    `json:"detail"`
	FindingSHA256 string    `json:"finding_sha256"`
}

type Request struct {
	ProjectDir             string
	Profile                string
	Session                string
	NamespaceID            string
	TaskID                 string
	TaskPath               string
	TaskSHA256             string
	Actor                  string
	Subject                string
	Argv                   []string
	ArgvEvidence           []Argument
	Executable             string
	ExecutableSHA256       string
	ExecutableUnverifiable string
	CWD                    string
	Environment            []EnvironmentEntry
	StartedAt              time.Time
	Seed                   string
	GitHead                string
	AttemptID              string
	RetryOf                string
}

type Result struct {
	Manifest       Manifest          `json:"manifest"`
	ManifestPath   string            `json:"manifest_path"`
	ManifestSHA256 string            `json:"manifest_sha256"`
	Process        *ProcessRecord    `json:"process,omitempty"`
	Outcome        *Outcome          `json:"outcome,omitempty"`
	OutcomePath    string            `json:"outcome_path,omitempty"`
	Summary        *Summary          `json:"summary,omitempty"`
	SummaryPath    string            `json:"summary_path,omitempty"`
	Classification string            `json:"classification"`
	Existing       bool              `json:"existing,omitempty"`
	Findings       []RecoveryFinding `json:"findings,omitempty"`
}

type invocationPlan struct {
	Manifest Manifest
	Argv     []string
	Env      []string
	Secrets  []string
}

type Store struct {
	ProjectDir string
	Profile    string
	Session    string
	Namespace  squadnamespace.Ref
	Root       string
}

func NewStore(projectDir, profile, session string) (Store, error) {
	abs, err := filepath.Abs(filepath.Clean(projectDir))
	if err != nil {
		return Store{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Store{}, fmt.Errorf("resolve evidence project: %w", err)
	}
	profile = squadnamespace.NormalizeProfile(profile)
	if err := team.ValidateProfileName(profile); err != nil {
		return Store{}, err
	}
	if err := team.ValidateSessionName(session); err != nil {
		return Store{}, err
	}
	ns := squadnamespace.Resolve(real, profile, session)
	root := filepath.Join(real, team.DirName, "evidence", "commands", profile, session)
	return Store{ProjectDir: real, Profile: profile, Session: session, Namespace: ns, Root: root}, nil
}

// CheckExisting compares an explicit idempotency key without writing. Callers
// use this before applying active-task mutation rules.
func (s Store) CheckExisting(req Request) (Result, bool, error) {
	if req.AttemptID == "" {
		return Result{}, false, nil
	}
	plan, err := s.buildPlan(req)
	if err != nil {
		return Result{}, false, err
	}
	result, err := s.Read(req.TaskID, req.AttemptID)
	if os.IsNotExist(err) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	if result.Manifest.RequestSHA256 != plan.Manifest.RequestSHA256 {
		return Result{}, true, fmt.Errorf("attempt id %s already exists with a conflicting request digest", req.AttemptID)
	}
	result.Existing = true
	return result, true, nil
}

func (s Store) Run(req Request) (Result, error) {
	plan, err := s.buildPlan(req)
	if err != nil {
		return Result{}, err
	}
	result, err := s.reserve(plan)
	if err != nil || result.Existing {
		return result, err
	}
	attempt, err := s.openAttempt(plan.Manifest.TaskID, plan.Manifest.AttemptID, false)
	if err != nil {
		return result, err
	}
	defer attempt.Close()
	stdout, err := createExclusive(attempt, stdoutSpool)
	if err != nil {
		return result, fmt.Errorf("create stdout spool: %w", err)
	}
	stderr, err := createExclusive(attempt, stderrSpool)
	if err != nil {
		closeErr := stdout.Close()
		removeErr := attempt.Remove(stdoutSpool)
		return result, errors.Join(fmt.Errorf("create stderr spool: %w", err), closeErr, removeErr, syncRoot(attempt))
	}

	cmd := exec.Command(plan.Manifest.Executable, plan.Argv[1:]...)
	cmd.Args = append([]string(nil), plan.Argv...)
	cmd.Dir = plan.Manifest.CWD
	cmd.Env = append([]string(nil), plan.Env...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := plan.Manifest.StartedAt
	startErr := cmd.Start()
	var waitErr error
	if startErr == nil {
		waitErr = cmd.Wait()
	}
	ended := time.Now().UTC()
	captureErrs := []error{stdout.Sync(), stderr.Sync(), stdout.Close(), stderr.Close()}
	process := processRecord(plan, started, ended, startErr, waitErr, cmd.ProcessState, captureErrs)
	process.RecordSHA256 = digestJSON(processWithoutDigest(process))
	if err := writeImmutableRoot(attempt, "process.json", process, recordMaxBytes); err != nil {
		return result, fmt.Errorf("persist immutable process record: %w", err)
	}
	result.Process = &process
	return s.finalize(plan, result, attempt)
}

// Recover never invokes the command. It finalizes a durable process record and
// spools, or appends an immutable finding when invocation truth is unavailable.
func (s Store) Recover(taskID, attemptID string, now time.Time) (Result, error) {
	result, err := s.Read(taskID, attemptID)
	if err != nil {
		return Result{}, err
	}
	attempt, err := s.openAttempt(taskID, attemptID, false)
	if err != nil {
		return result, err
	}
	defer attempt.Close()
	if result.Outcome != nil {
		if result.Summary == nil {
			if err := s.writeSummary(attempt, &result); err != nil {
				return result, err
			}
		}
		finding, err := s.appendFinding(attempt, attemptID, now, "terminal_outcome_verified", "immutable terminal outcome already exists; command was not rerun")
		if err != nil {
			return result, err
		}
		result.Findings = append(result.Findings, finding)
		return s.Read(taskID, attemptID)
	}
	if result.Process == nil {
		finding, findErr := s.appendFinding(attempt, attemptID, now, "process_outcome_unknown", "manifest exists without an immutable process record; recovery refused to guess or rerun")
		if findErr != nil {
			return result, findErr
		}
		result.Findings = append(result.Findings, finding)
		result.Classification = "reserved_incomplete"
		return result, fmt.Errorf("attempt %s has no immutable process record; no-rerun recovery recorded", attemptID)
	}
	plan := invocationPlan{Manifest: result.Manifest, Secrets: manifestSecrets(result.Manifest)}
	return s.finalize(plan, result, attempt)
}

func (s Store) reserve(plan invocationPlan) (Result, error) {
	m := plan.Manifest
	if m.RetryOf != "" {
		if m.RetryOf == m.AttemptID {
			return Result{}, fmt.Errorf("retry attempt must be distinct from parent")
		}
		parent, err := s.Read(m.TaskID, m.RetryOf)
		if err != nil {
			return Result{}, fmt.Errorf("retry parent: %w", err)
		}
		if parent.Manifest.TaskID != m.TaskID {
			return Result{}, fmt.Errorf("retry parent task mismatch")
		}
	}
	attempt, err := s.openAttempt(m.TaskID, m.AttemptID, true)
	if err != nil {
		return Result{}, err
	}
	defer attempt.Close()
	if existing, err := readManifestRoot(attempt); err == nil {
		if existing.RequestSHA256 != m.RequestSHA256 {
			return Result{}, fmt.Errorf("attempt id %s already exists with a conflicting request digest", m.AttemptID)
		}
		result, err := s.Read(m.TaskID, m.AttemptID)
		result.Existing = true
		return result, err
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if err := writeImmutableRoot(attempt, "manifest.json", m, manifestMaxBytes); err != nil {
		if !os.IsExist(err) {
			return Result{}, err
		}
		existing, readErr := readManifestRoot(attempt)
		if readErr != nil || existing.RequestSHA256 != m.RequestSHA256 {
			return Result{}, fmt.Errorf("attempt id %s already exists with a conflicting or unreadable request", m.AttemptID)
		}
		result, readErr := s.Read(m.TaskID, m.AttemptID)
		result.Existing = true
		return result, readErr
	}
	path := filepath.Join(s.Root, "tasks", m.TaskID, "attempts", m.AttemptID, "manifest.json")
	digest, err := digestRootFile(attempt, "manifest.json", manifestMaxBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Manifest: m, ManifestPath: path, ManifestSHA256: digest, Classification: "reserved_incomplete"}, nil
}

func (s Store) finalize(plan invocationPlan, result Result, attempt *os.Root) (Result, error) {
	process := result.Process
	if process == nil {
		var err error
		process, err = readProcessRoot(attempt, plan.Manifest)
		if err != nil {
			return result, err
		}
		result.Process = process
	}
	outcome := Outcome{SchemaVersion: SchemaVersion, AttemptID: plan.Manifest.AttemptID, RequestSHA256: plan.Manifest.RequestSHA256, Process: *process, FinalizationState: "complete"}
	stdoutRef, stdoutErr := s.publishSpool(attempt, stdoutSpool)
	stderrRef, stderrErr := s.publishSpool(attempt, stderrSpool)
	if stdoutErr == nil {
		outcome.Stdout = &stdoutRef
	}
	if stderrErr == nil {
		outcome.Stderr = &stderrRef
	}
	for _, err := range []error{stdoutErr, stderrErr} {
		if err != nil {
			outcome.FinalizationErrors = append(outcome.FinalizationErrors, scrub(err.Error(), plan.Secrets...))
		}
	}
	if len(outcome.FinalizationErrors) > 0 {
		outcome.FinalizationState = "artifact_incomplete"
	}
	outcome.OutcomeSHA256 = digestJSON(outcomeWithoutDigest(outcome))
	if err := writeImmutableRoot(attempt, "outcome.json", outcome, recordMaxBytes); err != nil {
		if !os.IsExist(err) {
			return result, err
		}
		current, readErr := readOutcomeRoot(attempt, plan.Manifest)
		if readErr != nil || current.OutcomeSHA256 != outcome.OutcomeSHA256 {
			return result, fmt.Errorf("immutable outcome conflict")
		}
		outcome = *current
	}
	result.Outcome = &outcome
	result.OutcomePath = filepath.Join(s.Root, "tasks", plan.Manifest.TaskID, "attempts", plan.Manifest.AttemptID, "outcome.json")
	if err := s.writeSummary(attempt, &result); err != nil {
		return result, err
	}
	if outcome.FinalizationState == "complete" {
		for _, name := range []string{stdoutSpool, stderrSpool} {
			if err := attempt.Remove(name); err != nil && !os.IsNotExist(err) {
				return result, fmt.Errorf("remove committed spool %s: %w", name, err)
			}
		}
	}
	result.Classification = classify(result)
	if outcome.FinalizationState != "complete" {
		return result, fmt.Errorf("attempt %s artifact finalization incomplete", plan.Manifest.AttemptID)
	}
	return result, nil
}

func (s Store) writeSummary(attempt *os.Root, result *Result) error {
	if result == nil || result.Outcome == nil {
		return fmt.Errorf("summary requires immutable outcome")
	}
	summary := Summary{SchemaVersion: SchemaVersion, AttemptID: result.Manifest.AttemptID, TaskID: result.Manifest.TaskID,
		Subject: truncate(result.Manifest.Subject, 240), ProcessState: result.Outcome.Process.State,
		FinalizationState: result.Outcome.FinalizationState, ManifestPath: result.ManifestPath,
		ManifestSHA256: result.ManifestSHA256, OutcomePath: result.OutcomePath,
		OutcomeSHA256: result.Outcome.OutcomeSHA256, Stdout: result.Outcome.Stdout, Stderr: result.Outcome.Stderr}
	summary.SummarySHA256 = digestJSON(summaryWithoutDigest(summary))
	if err := writeImmutableRoot(attempt, "summary.json", summary, SummaryMaxBytes); err != nil {
		if !os.IsExist(err) {
			return err
		}
		current, readErr := readSummaryRoot(attempt, summaryBinding{
			Manifest: result.Manifest, Outcome: *result.Outcome,
			ManifestPath: result.ManifestPath, ManifestSHA256: result.ManifestSHA256,
			OutcomePath: result.OutcomePath,
		})
		if readErr != nil || current.SummarySHA256 != summary.SummarySHA256 {
			return fmt.Errorf("immutable summary conflict")
		}
		summary = *current
	}
	result.Summary = &summary
	result.SummaryPath = filepath.Join(filepath.Dir(result.ManifestPath), "summary.json")
	return nil
}

func (s Store) Read(taskID, attemptID string) (Result, error) {
	if err := validateTaskID(taskID); err != nil {
		return Result{}, err
	}
	if err := validateAttemptID(attemptID); err != nil {
		return Result{}, err
	}
	attempt, err := s.openAttempt(taskID, attemptID, false)
	if err != nil {
		return Result{}, err
	}
	defer attempt.Close()
	m, err := readManifestRoot(attempt)
	if err != nil {
		return Result{}, err
	}
	expectedTaskPath := filepath.Join(s.Namespace.Paths.Tasks, taskID+".json")
	if m.TaskID != taskID || m.AttemptID != attemptID || m.Profile != s.Profile || m.Session != s.Session || m.NamespaceID != s.Namespace.ID || m.TaskPath != expectedTaskPath {
		return Result{}, fmt.Errorf("manifest identity mismatch")
	}
	if !contained(s.ProjectDir, m.CWD) || !validSHA256(m.TaskSHA256) || m.GitHead != "" && !validGitHead(m.GitHead) {
		return Result{}, fmt.Errorf("manifest project or revision binding mismatch")
	}
	manifestPath := filepath.Join(s.Root, "tasks", taskID, "attempts", attemptID, "manifest.json")
	manifestSHA, err := digestRootFile(attempt, "manifest.json", manifestMaxBytes)
	if err != nil {
		return Result{}, err
	}
	result := Result{Manifest: m, ManifestPath: manifestPath, ManifestSHA256: manifestSHA, Classification: "reserved_incomplete"}
	if process, err := readProcessRoot(attempt, m); err == nil {
		result.Process = process
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if outcome, err := readOutcomeRoot(attempt, m); err == nil {
		if result.Process == nil || result.Process.RecordSHA256 != outcome.Process.RecordSHA256 {
			return Result{}, fmt.Errorf("outcome does not bind the immutable process record")
		}
		result.Outcome = outcome
		result.OutcomePath = filepath.Join(filepath.Dir(manifestPath), "outcome.json")
		for _, ref := range []*ArtifactRef{outcome.Stdout, outcome.Stderr} {
			if ref != nil {
				if err := s.verifyObject(*ref); err != nil {
					return Result{}, err
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if summary, err := readSummaryRoot(attempt, summaryBinding{
		Manifest: m, Outcome: valueOutcome(result.Outcome),
		ManifestPath: manifestPath, ManifestSHA256: manifestSHA,
		OutcomePath: result.OutcomePath,
	}); err == nil {
		result.Summary = summary
		result.SummaryPath = filepath.Join(filepath.Dir(manifestPath), "summary.json")
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	result.Findings, err = readFindingsRoot(attempt, attemptID)
	if err != nil {
		return Result{}, err
	}
	result.Classification = classify(result)
	return result, nil
}

func (s Store) List(taskID string, limit int) ([]Result, error) {
	if err := validateTaskID(taskID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		return nil, fmt.Errorf("limit must be between 1 and 500")
	}
	root, err := s.openRelative(filepath.Join("tasks", taskID, "attempts"), false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dirFile, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dirFile.ReadDir(-1)
	closeErr := dirFile.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() && validateAttemptID(entry.Name()) == nil {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > limit {
		names = names[:limit]
	}
	out := make([]Result, 0, len(names))
	for _, name := range names {
		item, err := s.Read(taskID, name)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s Store) buildPlan(req Request) (invocationPlan, error) {
	if req.ProjectDir != s.ProjectDir || req.Profile != s.Profile || req.Session != s.Session || req.NamespaceID != s.Namespace.ID {
		return invocationPlan{}, fmt.Errorf("request namespace mismatch")
	}
	if err := validateTaskID(req.TaskID); err != nil {
		return invocationPlan{}, err
	}
	expectedTask := filepath.Join(s.Namespace.Paths.Tasks, req.TaskID+".json")
	if filepath.Clean(req.TaskPath) != filepath.Clean(expectedTask) {
		return invocationPlan{}, fmt.Errorf("task path does not match exact namespace")
	}
	if req.AttemptID == "" {
		id, err := NewAttemptID(time.Now().UTC())
		if err != nil {
			return invocationPlan{}, err
		}
		req.AttemptID = id
	}
	if err := validateAttemptID(req.AttemptID); err != nil {
		return invocationPlan{}, err
	}
	if req.RetryOf != "" {
		if err := validateAttemptID(req.RetryOf); err != nil {
			return invocationPlan{}, err
		}
	}
	if req.StartedAt.IsZero() {
		req.StartedAt = time.Now().UTC()
	}
	if req.Actor == "" || req.Subject == "" || len(req.Argv) == 0 {
		return invocationPlan{}, fmt.Errorf("command request is incomplete")
	}
	if len(req.Actor) > maxActorBytes || len(req.Subject) > maxSubjectBytes || strings.ContainsRune(req.Actor, 0) || strings.ContainsRune(req.Subject, 0) {
		return invocationPlan{}, fmt.Errorf("actor or subject exceeds its bounded shape")
	}
	if len(req.Argv) > maxArguments {
		return invocationPlan{}, fmt.Errorf("argv exceeds %d entries", maxArguments)
	}
	argvBytes := 0
	for _, arg := range req.Argv {
		if len(arg) > maxArgumentBytes || strings.ContainsRune(arg, 0) {
			return invocationPlan{}, fmt.Errorf("argv exceeds its bounded shape")
		}
		argvBytes += len(arg)
		if argvBytes > maxProjection {
			return invocationPlan{}, fmt.Errorf("argv projection exceeds %d bytes", maxProjection)
		}
	}
	if !filepath.IsAbs(req.CWD) || filepath.Clean(req.CWD) != req.CWD || !contained(s.ProjectDir, req.CWD) {
		return invocationPlan{}, fmt.Errorf("cwd must be canonical and inside project")
	}
	if len(req.Executable) > 4096 || !filepath.IsAbs(req.Executable) || filepath.Clean(req.Executable) != req.Executable {
		return invocationPlan{}, fmt.Errorf("executable must be resolved absolute path")
	}
	if (req.ExecutableSHA256 == "") == (req.ExecutableUnverifiable == "") {
		return invocationPlan{}, fmt.Errorf("executable requires exactly one digest or unverifiable reason")
	}
	if req.ExecutableSHA256 != "" && !validSHA256(req.ExecutableSHA256) {
		return invocationPlan{}, fmt.Errorf("executable digest must be canonical sha256")
	}
	if len(req.ExecutableUnverifiable) > maxSubjectBytes || strings.ContainsRune(req.ExecutableUnverifiable, 0) {
		return invocationPlan{}, fmt.Errorf("executable unverifiable reason exceeds its bounded shape")
	}
	if !validSHA256(req.TaskSHA256) || req.GitHead != "" && !validGitHead(req.GitHead) {
		return invocationPlan{}, fmt.Errorf("task or git revision identity is invalid")
	}
	if err := verifyExecutableIdentity(req.Executable, req.ExecutableSHA256, req.ExecutableUnverifiable); err != nil {
		return invocationPlan{}, err
	}
	if len(req.ArgvEvidence) != len(req.Argv) {
		return invocationPlan{}, fmt.Errorf("argv evidence projection length mismatch")
	}
	secrets := []string{}
	for i, entry := range req.ArgvEvidence {
		if entry.Index != i || strings.ContainsRune(req.Argv[i], 0) {
			return invocationPlan{}, fmt.Errorf("argv projection is invalid")
		}
		if entry.Redacted {
			if entry.Value != "" || entry.SHA256 != digestBytes([]byte(req.Argv[i])) || entry.ByteCount != int64(len(req.Argv[i])) {
				return invocationPlan{}, fmt.Errorf("redacted argv projection mismatch")
			}
			secrets = appendSecret(secrets, req.Argv[i])
		} else if entry.Value != req.Argv[i] || entry.SHA256 != "" || entry.ByteCount != 0 {
			return invocationPlan{}, fmt.Errorf("argv projection mismatch")
		}
		if obviousSecretArg(req.Argv, i) && !entry.Redacted {
			return invocationPlan{}, fmt.Errorf("argv index %d appears secret-bearing and requires redaction", i)
		}
	}
	env := append([]EnvironmentEntry(nil), req.Environment...)
	sort.Slice(env, func(i, j int) bool { return env[i].Name < env[j].Name })
	seen := map[string]bool{}
	childEnv := make([]string, 0, len(env))
	envBytes := 0
	for i := range env {
		e := &env[i]
		if !validEnvName(e.Name) || seen[e.Name] || len(e.RuntimeValue) > maxEnvValueBytes || strings.ContainsRune(e.RuntimeValue, 0) {
			return invocationPlan{}, fmt.Errorf("environment projection is invalid")
		}
		if e.Source != "baseline" && e.Source != "pass_env" {
			return invocationPlan{}, fmt.Errorf("environment %s requires baseline or pass_env source", e.Name)
		}
		if e.Source == "baseline" && !baselineEnvironmentName(e.Name) {
			return invocationPlan{}, fmt.Errorf("environment %s is not in the fixed baseline allowlist", e.Name)
		}
		seen[e.Name] = true
		if secretName(e.Name) && !e.Redacted {
			return invocationPlan{}, fmt.Errorf("secret-class environment name %s must be redacted", e.Name)
		}
		if e.Redacted {
			if e.Value != "" || e.SHA256 != digestBytes([]byte(e.RuntimeValue)) || e.ByteCount != int64(len(e.RuntimeValue)) {
				return invocationPlan{}, fmt.Errorf("redacted environment projection mismatch")
			}
			secrets = appendSecret(secrets, e.RuntimeValue)
		} else if e.Value != e.RuntimeValue || e.SHA256 != "" || e.ByteCount != 0 {
			return invocationPlan{}, fmt.Errorf("environment projection mismatch")
		}
		childEnv = append(childEnv, e.Name+"="+e.RuntimeValue)
		envBytes += len(e.Name) + 1 + len(e.RuntimeValue)
		if envBytes > maxProjection {
			return invocationPlan{}, fmt.Errorf("environment projection exceeds %d bytes", maxProjection)
		}
	}
	if len(env) > maxEnvironment {
		return invocationPlan{}, fmt.Errorf("environment exceeds %d entries", maxEnvironment)
	}
	m := Manifest{SchemaVersion: SchemaVersion, AttemptID: req.AttemptID, NamespaceID: req.NamespaceID,
		Profile: req.Profile, Session: req.Session, TaskID: req.TaskID, TaskPath: expectedTask, TaskSHA256: req.TaskSHA256,
		Actor: req.Actor, Subject: scrub(req.Subject, secrets...), Argv: append([]Argument(nil), req.ArgvEvidence...),
		Executable: req.Executable, ExecutableSHA256: req.ExecutableSHA256, ExecutableUnverifiable: req.ExecutableUnverifiable,
		CWD: req.CWD, Environment: env, StartedAt: req.StartedAt.UTC(), Seed: req.Seed, GitHead: req.GitHead,
		RetryOf: req.RetryOf, StdoutSpool: stdoutSpool, StderrSpool: stderrSpool}
	m.RequestSHA256 = requestDigest(m)
	return invocationPlan{Manifest: m, Argv: append([]string(nil), req.Argv...), Env: childEnv, Secrets: secrets}, nil
}

func requestDigest(m Manifest) string {
	type identity struct {
		NamespaceID, Profile, Session, TaskID, TaskPath, Actor, Subject string
		Argv                                                            []Argument
		Executable, ExecutableSHA256, ExecutableUnverifiable, CWD       string
		Environment                                                     []EnvironmentEntry
		Seed, RetryOf, GitHead                                          string
	}
	return digestJSON(identity{m.NamespaceID, m.Profile, m.Session, m.TaskID, m.TaskPath, m.Actor, m.Subject, m.Argv, m.Executable, m.ExecutableSHA256, m.ExecutableUnverifiable, m.CWD, m.Environment, m.Seed, m.RetryOf, m.GitHead})
}

func processRecord(plan invocationPlan, started, ended time.Time, startErr, waitErr error, state *os.ProcessState, capture []error) ProcessRecord {
	r := ProcessRecord{SchemaVersion: SchemaVersion, AttemptID: plan.Manifest.AttemptID, RequestSHA256: plan.Manifest.RequestSHA256, EndedAt: ended, ElapsedNanos: ended.Sub(started).Nanoseconds()}
	switch {
	case startErr != nil:
		r.State, r.TransportError = "spawn_failed", scrub(startErr.Error(), plan.Secrets...)
	case state != nil:
		code := state.ExitCode()
		r.ExitCode = &code
		r.State = "succeeded"
		if code != 0 {
			r.State = "exited_nonzero"
		}
		if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			r.State, r.Signal = "signaled", ws.Signal().String()
		}
	case waitErr != nil:
		r.State, r.TransportError = "wait_failed", scrub(waitErr.Error(), plan.Secrets...)
	default:
		r.State, r.TransportError = "wait_failed", "process ended without exit status"
	}
	for _, err := range capture {
		if err != nil {
			r.CaptureErrors = append(r.CaptureErrors, scrub(err.Error(), plan.Secrets...))
		}
	}
	return r
}

func (s Store) publishSpool(attempt *os.Root, name string) (ArtifactRef, error) {
	src, info, err := openRegularRoot(attempt, name, -1, -1)
	if err != nil {
		return ArtifactRef{}, err
	}
	defer src.Close()
	staging, err := s.openRelative(filepath.Join("objects", "staging"), true)
	if err != nil {
		return ArtifactRef{}, err
	}
	if err := staging.Close(); err != nil {
		return ArtifactRef{}, err
	}
	session, err := s.openRelative(".", false)
	if err != nil {
		return ArtifactRef{}, err
	}
	defer session.Close()
	random, err := newRandomHex(12)
	if err != nil {
		return ArtifactRef{}, err
	}
	stageRel := filepath.Join("objects", "staging", "object-"+random+".tmp")
	tmp, err := session.OpenFile(stageRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return ArtifactRef{}, err
	}
	h := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, h), src)
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		session.Remove(stageRel)
		return ArtifactRef{}, copyErr
	}
	if size != info.Size() {
		session.Remove(stageRel)
		return ArtifactRef{}, fmt.Errorf("spool size changed during publication")
	}
	hexsum := hex.EncodeToString(h.Sum(nil))
	bucket, err := s.openRelative(filepath.Join("objects", "sha256", hexsum[:2]), true)
	if err != nil {
		session.Remove(stageRel)
		return ArtifactRef{}, err
	}
	if err := bucket.Close(); err != nil {
		session.Remove(stageRel)
		return ArtifactRef{}, err
	}
	finalRel := filepath.Join("objects", "sha256", hexsum[:2], hexsum)
	linkErr := session.Link(stageRel, finalRel)
	removeErr := session.Remove(stageRel)
	if linkErr != nil && !os.IsExist(linkErr) {
		return ArtifactRef{}, errors.Join(linkErr, removeErr)
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return ArtifactRef{}, removeErr
	}
	staging, err = s.openRelative(filepath.Join("objects", "staging"), false)
	if err != nil {
		return ArtifactRef{}, err
	}
	stagingSyncErr := syncRoot(staging)
	stagingCloseErr := staging.Close()
	bucket, err = s.openRelative(filepath.Join("objects", "sha256", hexsum[:2]), false)
	if err != nil {
		return ArtifactRef{}, errors.Join(stagingSyncErr, stagingCloseErr, err)
	}
	bucketSyncErr := syncRoot(bucket)
	bucketCloseErr := bucket.Close()
	if err := errors.Join(stagingSyncErr, stagingCloseErr, bucketSyncErr, bucketCloseErr); err != nil {
		return ArtifactRef{}, err
	}
	ref := ArtifactRef{ID: "sha256:" + hexsum, Path: filepath.Join(s.Root, "objects", "sha256", hexsum[:2], hexsum), Size: size, SHA256: "sha256:" + hexsum, MediaType: "application/octet-stream"}
	if err := s.verifyObject(ref); err != nil {
		return ArtifactRef{}, err
	}
	return ref, nil
}

func (s Store) verifyObject(ref ArtifactRef) error {
	if ref.ID != ref.SHA256 || !strings.HasPrefix(ref.SHA256, "sha256:") || len(ref.SHA256) != 71 {
		return fmt.Errorf("invalid object identity")
	}
	hexsum := strings.TrimPrefix(ref.SHA256, "sha256:")
	expected := filepath.Join(s.Root, "objects", "sha256", hexsum[:2], hexsum)
	if filepath.Clean(ref.Path) != expected {
		return fmt.Errorf("object path mismatch")
	}
	bucket, err := s.openRelative(filepath.Join("objects", "sha256", hexsum[:2]), false)
	if err != nil {
		return err
	}
	defer bucket.Close()
	f, info, err := openRegularRoot(bucket, hexsum, ref.Size, ref.Size)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, readErr := io.Copy(h, f)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || n != info.Size() || digestHash(h) != ref.SHA256 {
		return fmt.Errorf("content-addressed object corrupt: %s", ref.Path)
	}
	return nil
}

func (s Store) appendFinding(attempt *os.Root, attemptID string, now time.Time, kind, detail string) (RecoveryFinding, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	f := RecoveryFinding{SchemaVersion: SchemaVersion, AttemptID: attemptID, At: now.UTC(), Kind: kind, Detail: truncate(detail, 1000)}
	f.FindingSHA256 = digestJSON(findingWithoutDigest(f))
	random, err := newRandomHex(6)
	if err != nil {
		return RecoveryFinding{}, err
	}
	name := "recovery-" + now.UTC().Format("20060102T150405.000000000Z") + "-" + random + ".json"
	return f, writeImmutableRoot(attempt, name, f, findingMaxBytes)
}

func (s Store) openAttempt(taskID, attemptID string, create bool) (*os.Root, error) {
	if err := validateTaskID(taskID); err != nil {
		return nil, err
	}
	if err := validateAttemptID(attemptID); err != nil {
		return nil, err
	}
	return s.openRelative(filepath.Join("tasks", taskID, "attempts", attemptID), create)
}

func (s Store) openRelative(rel string, create bool) (*os.Root, error) {
	project, err := os.OpenRoot(s.ProjectDir)
	if err != nil {
		return nil, err
	}
	fullRel := filepath.Join(team.DirName, "evidence", "commands", s.Profile, s.Session, rel)
	return openComponentsNoSymlink(project, fullRel, create)
}

func openComponentsNoSymlink(root *os.Root, rel string, create bool) (*os.Root, error) {
	current := root
	for _, part := range strings.Split(filepath.Clean(rel), string(os.PathSeparator)) {
		if part == "" || part == "." || part == ".." {
			current.Close()
			return nil, fmt.Errorf("unsafe evidence path component")
		}
		info, err := current.Lstat(part)
		if os.IsNotExist(err) && create {
			if err := current.Mkdir(part, dirMode); err != nil && !os.IsExist(err) {
				current.Close()
				return nil, err
			}
			if err := syncRoot(current); err != nil {
				current.Close()
				return nil, err
			}
			info, err = current.Lstat(part)
		}
		if err != nil {
			current.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, fmt.Errorf("unsafe evidence ancestor %q", part)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			current.Close()
			return nil, fmt.Errorf("evidence ancestor changed while opening")
		}
		visible, err := current.Lstat(part)
		if err != nil || visible.Mode()&os.ModeSymlink != 0 || !os.SameFile(visible, opened) {
			next.Close()
			current.Close()
			return nil, fmt.Errorf("evidence ancestor changed or became symlink")
		}
		current.Close()
		current = next
	}
	return current, nil
}

func writeImmutableRoot(root *os.Root, name string, value any, max int64) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if int64(len(b)) > max {
		return fmt.Errorf("immutable record exceeds %d-byte cap", max)
	}
	f, err := createExclusive(root, name)
	if err != nil {
		return err
	}
	cleanup := func(primary error, closeFile bool) error {
		var closeErr error
		if closeFile {
			closeErr = f.Close()
		}
		removeErr := root.Remove(name)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return errors.Join(primary, closeErr, removeErr, syncRoot(root))
	}
	if _, err := f.Write(b); err != nil {
		return cleanup(err, true)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err, true)
	}
	if err := f.Close(); err != nil {
		return cleanup(err, false)
	}
	return syncRoot(root)
}

func createExclusive(root *os.Root, name string) (*os.File, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, fmt.Errorf("unsafe evidence file name")
	}
	return root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
}

func readJSONRoot(root *os.Root, name string, max int64, target any) error {
	f, _, err := openRegularRoot(root, name, -1, max)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || int64(len(b)) > max {
		return fmt.Errorf("bounded read failed for %s", name)
	}
	return json.Unmarshal(b, target)
}

func openRegularRoot(root *os.Root, name string, size, max int64) (*os.File, os.FileInfo, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, nil, fmt.Errorf("unsafe evidence file name")
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || (size >= 0 && info.Size() != size) {
		return nil, nil, fmt.Errorf("evidence file %s is not the expected no-symlink regular file", name)
	}
	if max >= 0 && info.Size() > max {
		return nil, nil, fmt.Errorf("evidence file %s exceeds size cap", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := f.Stat()
	visible, visibleErr := root.Lstat(name)
	if statErr != nil || visibleErr != nil || visible.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) || !os.SameFile(visible, opened) {
		closeErr := f.Close()
		return nil, nil, errors.Join(fmt.Errorf("evidence file %s changed while opening", name), statErr, visibleErr, closeErr)
	}
	return f, opened, nil
}

func readManifestRoot(root *os.Root) (Manifest, error) {
	var m Manifest
	if err := readJSONRoot(root, "manifest.json", manifestMaxBytes, &m); err != nil {
		return Manifest{}, err
	}
	if m.SchemaVersion != SchemaVersion || m.RequestSHA256 != requestDigest(m) {
		return Manifest{}, fmt.Errorf("manifest digest mismatch")
	}
	if err := validateAttemptID(m.AttemptID); err != nil {
		return Manifest{}, err
	}
	if err := validateTaskID(m.TaskID); err != nil {
		return Manifest{}, err
	}
	if err := validateManifestShape(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func validateManifestShape(m Manifest) error {
	if m.NamespaceID == "" || len(m.NamespaceID) > 512 || m.Profile == "" || len(m.Profile) > 128 || m.Session == "" || len(m.Session) > 128 {
		return fmt.Errorf("manifest namespace fields exceed bounded shape")
	}
	if len(m.Actor) == 0 || len(m.Actor) > maxActorBytes || len(m.Subject) == 0 || len(m.Subject) > maxSubjectBytes || strings.ContainsRune(m.Actor, 0) || strings.ContainsRune(m.Subject, 0) {
		return fmt.Errorf("manifest actor or subject exceeds bounded shape")
	}
	if len(m.Argv) == 0 || len(m.Argv) > maxArguments {
		return fmt.Errorf("manifest argv exceeds bounded shape")
	}
	argvBytes := 0
	for i, arg := range m.Argv {
		if arg.Index != i || arg.ByteCount < 0 || arg.ByteCount > maxArgumentBytes {
			return fmt.Errorf("manifest argv projection is invalid")
		}
		if arg.Redacted {
			if arg.Value != "" || !validSHA256(arg.SHA256) {
				return fmt.Errorf("manifest redacted argv projection is invalid")
			}
			argvBytes += int(arg.ByteCount)
		} else {
			if arg.SHA256 != "" || arg.ByteCount != 0 || len(arg.Value) > maxArgumentBytes || strings.ContainsRune(arg.Value, 0) {
				return fmt.Errorf("manifest argv projection is invalid")
			}
			argvBytes += len(arg.Value)
		}
		if argvBytes > maxProjection {
			return fmt.Errorf("manifest argv projection exceeds bounded shape")
		}
	}
	if !filepath.IsAbs(m.TaskPath) || filepath.Clean(m.TaskPath) != m.TaskPath || !filepath.IsAbs(m.CWD) || filepath.Clean(m.CWD) != m.CWD || !filepath.IsAbs(m.Executable) || filepath.Clean(m.Executable) != m.Executable || len(m.Executable) > 4096 {
		return fmt.Errorf("manifest paths are not canonical absolute paths")
	}
	if (m.ExecutableSHA256 == "") == (m.ExecutableUnverifiable == "") || m.ExecutableSHA256 != "" && !validSHA256(m.ExecutableSHA256) || len(m.ExecutableUnverifiable) > maxSubjectBytes {
		return fmt.Errorf("manifest executable identity is invalid")
	}
	if len(m.Environment) > maxEnvironment {
		return fmt.Errorf("manifest environment exceeds bounded shape")
	}
	envBytes := 0
	previous := ""
	for _, env := range m.Environment {
		if !validEnvName(env.Name) || previous != "" && env.Name <= previous || env.Source != "baseline" && env.Source != "pass_env" || env.Source == "baseline" && !baselineEnvironmentName(env.Name) || env.ByteCount < 0 || env.ByteCount > maxEnvValueBytes {
			return fmt.Errorf("manifest environment projection is invalid")
		}
		previous = env.Name
		if env.Redacted {
			if env.Value != "" || !validSHA256(env.SHA256) {
				return fmt.Errorf("manifest redacted environment projection is invalid")
			}
			envBytes += int(env.ByteCount)
		} else {
			if env.SHA256 != "" || env.ByteCount != 0 || len(env.Value) > maxEnvValueBytes || strings.ContainsRune(env.Value, 0) || secretName(env.Name) {
				return fmt.Errorf("manifest environment projection is invalid")
			}
			envBytes += len(env.Value)
		}
		envBytes += len(env.Name) + 1
		if envBytes > maxProjection {
			return fmt.Errorf("manifest environment projection exceeds bounded shape")
		}
	}
	if m.StartedAt.IsZero() || m.StdoutSpool != stdoutSpool || m.StderrSpool != stderrSpool || len(m.Seed) > 1024 || len(m.GitHead) > 128 || len(m.TaskSHA256) > 128 {
		return fmt.Errorf("manifest lifecycle fields are invalid")
	}
	if m.RetryOf != "" {
		if err := validateAttemptID(m.RetryOf); err != nil || m.RetryOf == m.AttemptID {
			return fmt.Errorf("manifest retry identity is invalid")
		}
	}
	return nil
}

func validateProcessRecord(p ProcessRecord) error {
	if p.EndedAt.IsZero() || p.ElapsedNanos < 0 || len(p.TransportError) > maxSubjectBytes || len(p.CaptureErrors) > 16 {
		return fmt.Errorf("process record exceeds bounded shape")
	}
	for _, captureErr := range p.CaptureErrors {
		if captureErr == "" || len(captureErr) > maxSubjectBytes || strings.ContainsRune(captureErr, 0) {
			return fmt.Errorf("process capture error exceeds bounded shape")
		}
	}
	switch p.State {
	case "spawn_failed", "wait_failed":
		if p.TransportError == "" || p.ExitCode != nil || p.Signal != "" {
			return fmt.Errorf("process transport state is inconsistent")
		}
	case "succeeded":
		if p.TransportError != "" || p.ExitCode == nil || *p.ExitCode != 0 || p.Signal != "" {
			return fmt.Errorf("successful process state is inconsistent")
		}
	case "exited_nonzero":
		if p.TransportError != "" || p.ExitCode == nil || *p.ExitCode == 0 || p.Signal != "" {
			return fmt.Errorf("nonzero process state is inconsistent")
		}
	case "signaled":
		if p.TransportError != "" || p.ExitCode == nil || p.Signal == "" || len(p.Signal) > 64 {
			return fmt.Errorf("signaled process state is inconsistent")
		}
	default:
		return fmt.Errorf("unknown process state %q", p.State)
	}
	return nil
}

func validateArtifactRef(ref *ArtifactRef) error {
	if ref == nil || ref.ID != ref.SHA256 || !validSHA256(ref.SHA256) || ref.Size < 0 || ref.MediaType != "application/octet-stream" || !filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path {
		return fmt.Errorf("artifact reference is invalid")
	}
	return nil
}

func validateOutcomeShape(o Outcome) error {
	if len(o.FinalizationErrors) > 16 {
		return fmt.Errorf("outcome finalization errors exceed bounded shape")
	}
	for _, finalErr := range o.FinalizationErrors {
		if finalErr == "" || len(finalErr) > maxSubjectBytes || strings.ContainsRune(finalErr, 0) {
			return fmt.Errorf("outcome finalization error exceeds bounded shape")
		}
	}
	for _, ref := range []*ArtifactRef{o.Stdout, o.Stderr} {
		if ref != nil {
			if err := validateArtifactRef(ref); err != nil {
				return err
			}
		}
	}
	switch o.FinalizationState {
	case "complete":
		if len(o.FinalizationErrors) != 0 || o.Stdout == nil || o.Stderr == nil {
			return fmt.Errorf("complete outcome is missing committed artifacts")
		}
	case "artifact_incomplete":
		if len(o.FinalizationErrors) == 0 {
			return fmt.Errorf("incomplete outcome is missing finalization errors")
		}
	default:
		return fmt.Errorf("unknown finalization state %q", o.FinalizationState)
	}
	return nil
}

func readProcessRoot(root *os.Root, m Manifest) (*ProcessRecord, error) {
	var p ProcessRecord
	if err := readJSONRoot(root, "process.json", recordMaxBytes, &p); err != nil {
		return nil, err
	}
	if p.SchemaVersion != SchemaVersion || p.AttemptID != m.AttemptID || p.RequestSHA256 != m.RequestSHA256 || p.RecordSHA256 != digestJSON(processWithoutDigest(p)) {
		return nil, fmt.Errorf("process record identity/digest mismatch")
	}
	if err := validateProcessRecord(p); err != nil {
		return nil, err
	}
	return &p, nil
}

func readOutcomeRoot(root *os.Root, m Manifest) (*Outcome, error) {
	var o Outcome
	if err := readJSONRoot(root, "outcome.json", recordMaxBytes, &o); err != nil {
		return nil, err
	}
	if o.SchemaVersion != SchemaVersion || o.AttemptID != m.AttemptID || o.RequestSHA256 != m.RequestSHA256 || o.OutcomeSHA256 != digestJSON(outcomeWithoutDigest(o)) {
		return nil, fmt.Errorf("outcome identity/digest mismatch")
	}
	if o.Process.AttemptID != m.AttemptID || o.Process.RequestSHA256 != m.RequestSHA256 || o.Process.RecordSHA256 != digestJSON(processWithoutDigest(o.Process)) {
		return nil, fmt.Errorf("outcome process record mismatch")
	}
	if err := validateProcessRecord(o.Process); err != nil {
		return nil, err
	}
	if err := validateOutcomeShape(o); err != nil {
		return nil, err
	}
	return &o, nil
}

type summaryBinding struct {
	Manifest       Manifest
	Outcome        Outcome
	ManifestPath   string
	ManifestSHA256 string
	OutcomePath    string
}

func readSummaryRoot(root *os.Root, binding summaryBinding) (*Summary, error) {
	m, o := binding.Manifest, binding.Outcome
	var s Summary
	if err := readJSONRoot(root, "summary.json", SummaryMaxBytes, &s); err != nil {
		return nil, err
	}
	if s.SchemaVersion != SchemaVersion || s.AttemptID != m.AttemptID || s.TaskID != m.TaskID || s.OutcomeSHA256 != o.OutcomeSHA256 || s.SummarySHA256 != digestJSON(summaryWithoutDigest(s)) {
		return nil, fmt.Errorf("summary identity/digest mismatch")
	}
	if s.Subject != truncate(m.Subject, 240) || s.ProcessState != o.Process.State || s.FinalizationState != o.FinalizationState || s.ManifestPath != binding.ManifestPath || s.ManifestSHA256 != binding.ManifestSHA256 || s.OutcomePath != binding.OutcomePath || s.OutcomeSHA256 != o.OutcomeSHA256 {
		return nil, fmt.Errorf("summary semantic binding mismatch")
	}
	if (s.Stdout == nil) != (o.Stdout == nil) || (s.Stderr == nil) != (o.Stderr == nil) {
		return nil, fmt.Errorf("summary artifact projection mismatch")
	}
	if s.Stdout != nil && *s.Stdout != *o.Stdout || s.Stderr != nil && *s.Stderr != *o.Stderr {
		return nil, fmt.Errorf("summary artifact identity mismatch")
	}
	return &s, nil
}

func readFindingsRoot(root *os.Root, attemptID string) ([]RecoveryFinding, error) {
	dirFile, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dirFile.ReadDir(maxAttemptEntries + 1)
	closeErr := dirFile.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > maxAttemptEntries {
		return nil, fmt.Errorf("attempt directory exceeds %d-entry read cap", maxAttemptEntries)
	}
	out := []RecoveryFinding{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "recovery-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if len(out) >= maxFindings {
			return nil, fmt.Errorf("recovery findings exceed %d-entry cap", maxFindings)
		}
		var f RecoveryFinding
		if err := readJSONRoot(root, entry.Name(), findingMaxBytes, &f); err != nil {
			return nil, err
		}
		if f.AttemptID != attemptID || f.FindingSHA256 != digestJSON(findingWithoutDigest(f)) || !validRecoveryFinding(entry.Name(), f) {
			return nil, fmt.Errorf("recovery finding identity/digest mismatch")
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].FindingSHA256 < out[j].FindingSHA256
		}
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}

func digestRootFile(root *os.Root, name string, max int64) (string, error) {
	f, _, err := openRegularRoot(root, name, -1, max)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, readErr := io.Copy(h, io.LimitReader(f, max+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return "", fmt.Errorf("digest read failed")
	}
	return digestHash(h), nil
}

func BuildEnvironment(values map[string]string, redacted map[string]bool) []EnvironmentEntry {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]EnvironmentEntry, 0, len(keys))
	for _, key := range keys {
		value := values[key]
		e := EnvironmentEntry{Name: key, Source: "pass_env", Value: value, RuntimeValue: value}
		if redacted[key] {
			e.Value = ""
			e.Redacted = true
			e.SHA256 = digestBytes([]byte(value))
			e.ByteCount = int64(len(value))
		}
		out = append(out, e)
	}
	return out
}

// BuildBaselineEnvironment projects only the fixed, non-secret environment
// used by the wrapper itself. Explicit caller inheritance uses BuildEnvironment.
func BuildBaselineEnvironment(values map[string]string) ([]EnvironmentEntry, error) {
	out := BuildEnvironment(values, nil)
	for i := range out {
		if !baselineEnvironmentName(out[i].Name) || secretName(out[i].Name) {
			return nil, fmt.Errorf("environment %s is not in the fixed baseline allowlist", out[i].Name)
		}
		out[i].Source = "baseline"
	}
	return out, nil
}

func BuildArguments(argv []string, redacted map[int]bool) []Argument {
	out := make([]Argument, len(argv))
	for i, value := range argv {
		out[i] = Argument{Index: i, Value: value}
		if redacted[i] {
			out[i] = Argument{Index: i, Redacted: true, SHA256: digestBytes([]byte(value)), ByteCount: int64(len(value))}
		}
	}
	return out
}

func NewAttemptID(now time.Time) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "attempt-" + now.UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(b[:]), nil
}

func validateTaskID(id string) error {
	if len(id) < 2 || len(id) > 22 || id[0] != 't' || id[1] == '0' {
		return fmt.Errorf("invalid canonical task id")
	}
	n, err := strconv.ParseUint(id[1:], 10, 64)
	if err != nil || n == 0 || "t"+strconv.FormatUint(n, 10) != id {
		return fmt.Errorf("invalid canonical task id")
	}
	return nil
}
func validateAttemptID(id string) error {
	if len(id) < 1 || len(id) > 128 || filepath.Base(id) != id || id == "." || id == ".." {
		return fmt.Errorf("invalid attempt id")
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return fmt.Errorf("invalid attempt id")
		}
	}
	return nil
}
func validEnvName(v string) bool {
	if v == "" || len(v) > 128 {
		return false
	}
	for i, r := range v {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' && i > 0) {
			return false
		}
	}
	return true
}
func baselineEnvironmentName(v string) bool {
	switch v {
	case "PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TZ", "USER", "LOGNAME", "SHELL", "GOCACHE", "GOMODCACHE", "GOPATH", "XDG_CACHE_HOME", "SYSTEMROOT", "COMSPEC", "PATHEXT":
		return true
	default:
		return false
	}
}

func verifyExecutableIdentity(path, expectedDigest, unverifiable string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve executable identity: %w", err)
	}
	if filepath.Clean(resolved) != path {
		return fmt.Errorf("executable path is not its canonical resolved identity")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect executable identity: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("executable identity must not be a symlink")
	}
	if !before.Mode().IsRegular() {
		if expectedDigest != "" || strings.TrimSpace(unverifiable) == "" {
			return fmt.Errorf("non-regular executable requires an explicit unverifiable reason")
		}
		return nil
	}
	if unverifiable != "" || !validSHA256(expectedDigest) {
		return fmt.Errorf("regular executable requires a canonical sha256 digest")
	}
	if runtime.GOOS != "windows" && before.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("resolved executable is not executable")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open executable identity: %w", err)
	}
	opened, statErr := f.Stat()
	visible, visibleErr := os.Lstat(path)
	if statErr != nil || visibleErr != nil || visible.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(visible, opened) {
		closeErr := f.Close()
		return errors.Join(fmt.Errorf("executable changed while verifying identity"), statErr, visibleErr, closeErr)
	}
	h := sha256.New()
	_, readErr := io.Copy(h, f)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("hash executable identity"), readErr, closeErr)
	}
	if digestHash(h) != expectedDigest {
		return fmt.Errorf("executable digest does not match resolved bytes")
	}
	return nil
}

func validSHA256(v string) bool {
	if len(v) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(v, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(v, "sha256:"))
	return err == nil
}
func validGitHead(v string) bool {
	if len(v) != 40 && len(v) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(v)
	return err == nil && hex.EncodeToString(decoded) == v
}
func validRecoveryFinding(name string, f RecoveryFinding) bool {
	if f.SchemaVersion != SchemaVersion || f.At.IsZero() || f.Detail == "" || len(f.Detail) > maxSubjectBytes || strings.ContainsRune(f.Detail, 0) {
		return false
	}
	if f.Kind != "process_outcome_unknown" && f.Kind != "terminal_outcome_verified" {
		return false
	}
	const prefix, suffix = "recovery-", ".json"
	core := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	parts := strings.Split(core, "-")
	if len(parts) != 2 || len(parts[1]) != 12 {
		return false
	}
	random, err := hex.DecodeString(parts[1])
	if err != nil || len(random) != 6 || hex.EncodeToString(random) != parts[1] {
		return false
	}
	at, err := time.Parse("20060102T150405.000000000Z", parts[0])
	return err == nil && at.Equal(f.At) && f.At.Location() == time.UTC
}
func secretName(v string) bool {
	u := strings.ToUpper(v)
	for _, p := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY", "API_KEY"} {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}
func obviousSecretArg(argv []string, i int) bool {
	u := strings.ToLower(argv[i])
	for _, p := range []string{"token=", "secret=", "password=", "passwd=", "api-key=", "api_key=", "private-key="} {
		if strings.Contains(u, p) {
			return true
		}
	}
	if i > 0 {
		p := strings.ToLower(argv[i-1])
		for _, flag := range []string{"--token", "--secret", "--password", "--api-key", "--private-key"} {
			if p == flag {
				return true
			}
		}
	}
	return false
}
func appendSecret(in []string, v string) []string {
	if v == "" {
		return in
	}
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}
func manifestSecrets(m Manifest) []string { return nil }
func scrub(v string, secrets ...string) string {
	for _, s := range secrets {
		if s != "" {
			v = strings.ReplaceAll(v, s, "[REDACTED]")
		}
	}
	v = strings.ReplaceAll(v, "\x00", "")
	return truncate(v, 1000)
}
func truncate(v string, n int) string {
	if len(v) <= n {
		return v
	}
	for n > 0 && (v[n]&0xc0) == 0x80 {
		n--
	}
	return v[:n]
}
func contained(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func digestBytes(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }
func digestHash(h interface{ Sum([]byte) []byte }) string {
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
func digestJSON(v any) string { b, _ := json.Marshal(v); return digestBytes(b) }
func newRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate evidence suffix: %w", err)
	}
	return hex.EncodeToString(b), nil
}
func processWithoutDigest(v ProcessRecord) ProcessRecord     { v.RecordSHA256 = ""; return v }
func outcomeWithoutDigest(v Outcome) Outcome                 { v.OutcomeSHA256 = ""; return v }
func summaryWithoutDigest(v Summary) Summary                 { v.SummarySHA256 = ""; return v }
func findingWithoutDigest(v RecoveryFinding) RecoveryFinding { v.FindingSHA256 = ""; return v }
func valueOutcome(v *Outcome) Outcome {
	if v == nil {
		return Outcome{}
	}
	return *v
}
func classify(r Result) string {
	if r.Outcome == nil {
		return "reserved_incomplete"
	}
	if r.Outcome.FinalizationState != "complete" || r.Summary == nil {
		return "finalization_uncertain"
	}
	return r.Outcome.Process.State
}
func syncRoot(root *os.Root) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
