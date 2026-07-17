package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// taskSelection is the immutable identity selected before a task-scoped
// mutation. TaskPath and FileSHA256 are re-read under namespace admission so
// a caller can never discover one task and mutate another profile's copy.
type taskSelection struct {
	ProjectDir string
	Profile    string
	Session    string
	Namespace  squadnamespace.Ref
	TaskPath   string
	FileSHA256 string
	Task       taskstore.Task
}

type exactTaskLaunchNamespace struct {
	ProjectDir string
	Profile    string
	Session    string
}

func selectTaskForMutation(id, sessionFlag, projectFlag, profileFlag string, fs *flag.FlagSet) (taskSelection, error) {
	id = strings.TrimSpace(id)
	if err := validateTaskIDLeaf(id); err != nil {
		return taskSelection{}, err
	}
	if fs.NArg() > 0 {
		return taskSelection{}, usageErrorf("unexpected argument %q", fs.Arg(0))
	}

	launchNS, launched := taskNamespaceFromExactLaunch()
	session := strings.TrimSpace(sessionFlag)
	if session == "" {
		if !launched {
			return taskSelection{}, usageErrorf("--session is required (tasks are per-workstream)")
		}
		session = launchNS.Session
	}
	if err := team.ValidateSessionName(session); err != nil {
		return taskSelection{}, usageErrorf("invalid --session: %v", err)
	}

	projectDir := ""
	if flagWasSet(fs, "project") {
		cwd, err := os.Getwd()
		if err != nil {
			return taskSelection{}, err
		}
		resolved, err := resolveProjectDirFlag(cwd, projectFlag, true)
		if err != nil {
			return taskSelection{}, err
		}
		projectDir = resolved
	} else if launched {
		projectDir = launchNS.ProjectDir
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return taskSelection{}, err
		}
		projectDir = cwd
	}
	absoluteProject, absErr := filepath.Abs(filepath.Clean(projectDir))
	if absErr != nil {
		return taskSelection{}, fmt.Errorf("resolve task project: %w", absErr)
	}
	projectDir = absoluteProject

	profileExplicit := flagWasSet(fs, "profile")
	if profileExplicit {
		profile := squadnamespace.NormalizeProfile(profileFlag)
		if err := team.ValidateProfileName(profile); err != nil {
			return taskSelection{}, usageErrorf("invalid --profile: %v", err)
		}
		return readTaskSelection(projectDir, profile, session, id)
	}
	if launched {
		if session != launchNS.Session || !rootsMatch(projectDir, launchNS.ProjectDir) {
			return taskSelection{}, usageErrorf("task namespace contradicts exact launch identity; pass the exact --project, --profile, and --session")
		}
		return readTaskSelection(projectDir, launchNS.Profile, session, id)
	}

	// Omitted-profile discovery is deliberately read-only. A named-profile
	// match is never adopted implicitly, even when it is the only match.
	profiles, err := team.ListProfiles(projectDir)
	if err != nil {
		return taskSelection{}, err
	}
	sort.Strings(profiles)
	var named []taskSelection
	for _, profile := range profiles {
		candidate, err := readTaskSelection(projectDir, profile, session, id)
		if err == nil {
			named = append(named, candidate)
			continue
		}
		if !os.IsNotExist(taskSelectionCause(err)) {
			return taskSelection{}, err
		}
	}
	if len(named) > 0 {
		names := make([]string, 0, len(named))
		for _, candidate := range named {
			names = append(names, candidate.Profile)
		}
		return taskSelection{}, usageErrorf("task %s in session %s is pinned to named profile %s; rerun with explicit --profile before mutation", id, session, strings.Join(names, ","))
	}
	return readTaskSelection(projectDir, team.DefaultProfile, session, id)
}

type taskSelectionReadError struct {
	path string
	err  error
}

func (e *taskSelectionReadError) Error() string {
	return fmt.Sprintf("read selected task %s: %v", e.path, e.err)
}
func (e *taskSelectionReadError) Unwrap() error { return e.err }

func taskSelectionCause(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok || u.Unwrap() == nil {
			return err
		}
		err = u.Unwrap()
	}
}

func readTaskSelection(projectDir, profile, session, id string) (taskSelection, error) {
	if err := validateTaskIDLeaf(id); err != nil {
		return taskSelection{}, err
	}
	profile = squadnamespace.NormalizeProfile(profile)
	path := filepath.Join(taskstore.DirForProfile(projectDir, profile, session), strings.TrimSpace(id)+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return taskSelection{}, &taskSelectionReadError{path: path, err: err}
	}
	var selected taskstore.Task
	if err := json.Unmarshal(b, &selected); err != nil {
		return taskSelection{}, &taskSelectionReadError{path: path, err: err}
	}
	if strings.TrimSpace(selected.ID) != strings.TrimSpace(id) {
		return taskSelection{}, fmt.Errorf("selected task file %s contains task %q, expected %q", path, selected.ID, id)
	}
	sum := sha256.Sum256(b)
	return taskSelection{
		ProjectDir: projectDir,
		Profile:    profile,
		Session:    session,
		Namespace:  squadnamespace.Resolve(projectDir, profile, session),
		TaskPath:   path,
		FileSHA256: hex.EncodeToString(sum[:]),
		Task:       selected,
	}, nil
}

func validateTaskIDLeaf(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || filepath.IsAbs(id) || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return usageErrorf("invalid task id %q: expected canonical t<N> leaf", id)
	}
	if !strings.HasPrefix(id, "t") || len(id) == 1 {
		return usageErrorf("invalid task id %q: expected canonical t<N> leaf", id)
	}
	n, err := strconv.Atoi(id[1:])
	if err != nil || n <= 0 || "t"+strconv.Itoa(n) != id {
		return usageErrorf("invalid task id %q: expected canonical t<N> leaf", id)
	}
	return nil
}

func revalidateTaskSelection(selected taskSelection) (taskSelection, error) {
	current, err := readTaskSelection(selected.ProjectDir, selected.Profile, selected.Session, selected.Task.ID)
	if err != nil {
		return taskSelection{}, fmt.Errorf("selected task changed before mutation: %w", err)
	}
	if current.TaskPath != selected.TaskPath || current.FileSHA256 != selected.FileSHA256 || !squadnamespace.ProfilesEqual(current.Profile, selected.Profile) {
		return taskSelection{}, fmt.Errorf("selected task changed before mutation: path/digest no longer matches accepted task identity")
	}
	return current, nil
}

func validateTaskSelectionNamespace(selected taskSelection) error {
	expectedNS := squadnamespace.Resolve(selected.ProjectDir, selected.Profile, selected.Session)
	expectedPath := filepath.Join(expectedNS.Paths.Tasks, selected.Task.ID+".json")
	if filepath.Clean(selected.TaskPath) != filepath.Clean(expectedPath) || selected.Namespace.ID != expectedNS.ID ||
		!rootsMatch(selected.Namespace.AMQRoot, expectedNS.AMQRoot) {
		return fmt.Errorf("selected task namespace/path is inconsistent with exact profile/session identity")
	}
	rel, err := filepath.Rel(selected.ProjectDir, selected.TaskPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("selected task path escapes project root")
	}
	return nil
}
