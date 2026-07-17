package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

const issue470Session = "issue-470"
const issue470Goal = "Execute the accepted fresh AMQ fixture"

func issue470RunArgs(project, profile string, extras ...string) []string {
	args := []string{
		"--project", project, "--session", issue470Session,
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
	}
	if profile != team.DefaultProfile {
		args = append(args, "--profile", profile)
	}
	return append(args, extras...)
}

func prepareIssue470Run(t *testing.T, project, profile string, extras ...string) {
	t.Helper()
	args := issue470RunArgs(project, profile, extras...)
	args = append(args, "--goal", issue470Goal, "--prepare")
	if _, _, err := captureOutput(t, func() error { return runRunStart(args, "test") }); err != nil {
		t.Fatalf("prepare accepted issue-470 fixture: %v", err)
	}
	stubSuccessfulRunStartGoalDelivery(t)
}

func setupStrictFreshProjectAMQ(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "amq-calls")
	script := `#!/bin/sh
printf '%s\n' "$PWD :: $*" >> "$AMQ_STRICT_LOG"
if [ "$1" != "env" ]; then
  echo "unexpected amq command: $*" >&2
  exit 2
fi
shift
root=""
session=""
me=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --root) root="$2"; shift 2 ;;
    --session) session="$2"; shift 2 ;;
    --me) me="$2"; shift 2 ;;
    --json) shift ;;
    *) shift ;;
  esac
done
if [ "$AMQ_STRICT_FORCE_FAIL" = "1" ]; then
  echo "cannot determine root: configured root is invalid" >&2
  exit 1
fi
source=""
if [ -n "$root" ]; then
  case "$root" in /*) ;; *) root="$PWD/$root" ;; esac
  base="$root"
  source="explicit"
else
  configured=""
  if [ -f .amqrc ]; then
    configured=$(sed -n 's/.*"root":"\([^"]*\)".*/\1/p' .amqrc)
  fi
  if [ -n "$configured" ]; then
    case "$configured" in /*) base="$configured" ;; *) base="$PWD/$configured" ;; esac
    source="project_amqrc"
  elif [ -d .agent-mail ]; then
    base="$PWD/.agent-mail"
    source="project_agent_mail"
  else
    echo "cannot determine root: no .amqrc found, no .agent-mail/ directory, AM_ROOT not set, and no global config" >&2
    exit 1
  fi
  root="$base"
  if [ -n "$session" ]; then root="$base/$session"; fi
fi
printf '{"schema_version":1,"amq_version":"0.43.1","root":"%s","base_root":"%s","session_name":"%s","me":"%s","root_source":"%s"}\n' "$root" "$base" "$session" "$me" "$source"
`
	path := filepath.Join(binDir, "amq")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_STRICT_LOG", logPath)
	t.Setenv("AMQ_STRICT_FORCE_FAIL", "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func issue470Team(project, profile string) team.Team {
	return team.Team{
		Project:      project,
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{{
			Role: "cto", Binary: "codex", Handle: "cto", Session: issue470Session,
		}},
	}
}

func writeIssue470Team(t *testing.T, project, profile string) {
	t.Helper()
	if err := team.WriteProfile(project, profile, issue470Team(project, profile)); err != nil {
		t.Fatal(err)
	}
}

func TestIssue470SelectedRootExistenceIsProfileAware(t *testing.T) {
	logPath := setupStrictFreshProjectAMQ(t)
	project := t.TempDir()
	cfg := issue470Team(project, team.DefaultProfile)

	exists, root, err := teamWorkstreamExists(cfg, team.DefaultProfile, issue470Session)
	if err != nil || exists || root != "" {
		t.Fatalf("fresh default exists=%v root=%q err=%v", exists, root, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agent-mail")); !os.IsNotExist(err) {
		t.Fatalf("read-only default existence check created .agent-mail: %v", err)
	}

	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	exists, root, err = teamWorkstreamExists(cfg, "review", issue470Session)
	if err != nil || exists || root != "" {
		t.Fatalf("fresh named exists=%v root=%q err=%v", exists, root, err)
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logBody), "--session") || !strings.Contains(string(logBody), "--root "+filepath.Join(project, ".agent-mail", "review", issue470Session)) {
		t.Fatalf("named existence probe must use only its exact root:\n%s", logBody)
	}

	legacy := filepath.Join(project, ".agent-mail", issue470Session)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if exists, root, err := teamWorkstreamExists(cfg, "review", issue470Session); err != nil || exists || root != "" {
		t.Fatalf("named profile probed legacy default root: exists=%v root=%q err=%v", exists, root, err)
	}
	selected := filepath.Join(project, ".agent-mail", "review", issue470Session)
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	if exists, root, err := teamWorkstreamExists(cfg, "review", issue470Session); err != nil || !exists || root != selected {
		t.Fatalf("selected named root not detected: exists=%v root=%q err=%v", exists, root, err)
	}
}

func TestIssue470ConfiguredDefaultRootIsPreserved(t *testing.T) {
	setupStrictFreshProjectAMQ(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), []byte(`{"project":"custom","root":"custom-mail"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, issue470Session, "cto")
	if err != nil {
		t.Fatal(err)
	}
	wantBase := filepath.Join(project, "custom-mail")
	realProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	wantBase = filepath.Join(realProject, "custom-mail")
	if env.BaseRoot != wantBase || env.Root != filepath.Join(wantBase, issue470Session) || env.RootSource != "project_amqrc" {
		t.Fatalf("configured context = %+v, want base %s", env, wantBase)
	}
	if _, err := os.Stat(wantBase); !os.IsNotExist(err) {
		t.Fatalf("configured-root readiness must remain read-only: %v", err)
	}
}

func TestIssue470ConfiguredDefaultRootLivePreparesOnlySelectedBase(t *testing.T) {
	setupStrictFreshProjectAMQ(t)
	project := t.TempDir()
	writeIssue470Team(t, project, team.DefaultProfile)
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), []byte(`{"project":"custom","root":"custom-mail"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	backend := useFakeTmuxBackend(t)
	prepareIssue470Run(t, project, team.DefaultProfile, "--visibility", "detached")
	_, _, err := captureOutput(t, func() error {
		return runRunStart(append(issue470RunArgs(project, team.DefaultProfile, "--visibility", "detached"), "--go"), "test")
	})
	if err != nil {
		t.Fatalf("configured-root live launch: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("backend launches=%d, want 1", len(backend.launches))
	}
	if info, err := os.Stat(filepath.Join(project, "custom-mail")); err != nil || !info.IsDir() {
		t.Fatalf("configured selected base not prepared: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agent-mail", issue470Session)); !os.IsNotExist(err) {
		t.Fatalf("configured-root launch touched default AMQ session root: %v", err)
	}
}

func TestIssue470RunStartRefusesExistingSelectedRootBeforeBackend(t *testing.T) {
	for _, profile := range []string{team.DefaultProfile, "review"} {
		t.Run(profile, func(t *testing.T) {
			setupStrictFreshProjectAMQ(t)
			project := t.TempDir()
			writeIssue470Team(t, project, profile)
			prepareIssue470Run(t, project, profile, "--visibility", "detached")
			selected := filepath.Join(project, ".agent-mail", issue470Session)
			if profile != team.DefaultProfile {
				selected = filepath.Join(project, ".agent-mail", profile, issue470Session)
				// A legacy default root must not affect which named root is selected.
				if err := os.MkdirAll(filepath.Join(project, ".agent-mail", issue470Session), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(selected, 0o755); err != nil {
				t.Fatal(err)
			}
			backend := useFakeTmuxBackend(t)
			args := append(issue470RunArgs(project, profile, "--visibility", "detached"), "--go")
			_, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
			if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), selected) {
				t.Fatalf("existing selected-root refusal=%v", err)
			}
			if len(backend.dryRuns) != 0 || len(backend.launches) != 0 {
				t.Fatalf("backend was reached on refusal: dryRuns=%d launches=%d", len(backend.dryRuns), len(backend.launches))
			}
		})
	}
}

func TestIssue470RunStartFreshAMQMatrix(t *testing.T) {
	profiles := []string{team.DefaultProfile, "review"}
	rosters := []string{"fresh", "existing"}
	paths := []string{"canonical", "layout-finalization"}
	for _, profile := range profiles {
		for _, roster := range rosters {
			for _, launchPath := range paths {
				name := profile + "/" + roster + "/" + launchPath
				t.Run(name, func(t *testing.T) {
					setupStrictFreshProjectAMQ(t)
					project := t.TempDir()
					if roster == "existing" {
						writeIssue470Team(t, project, profile)
					}
					backend := useFakeTmuxBackend(t)
					args := issue470RunArgs(project, profile)
					if roster == "fresh" {
						args = append(args, "--roles", "cto", "--lead", "cto")
					}
					if launchPath == "layout-finalization" {
						stubCurrentRunStartPane(t, "%42")
						args = append(args, "--layout-preset", layoutPresetOneWindowPerAgent)
					} else {
						args = append(args, "--visibility", "detached")
					}

					preview, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
					if err != nil {
						t.Fatalf("preview: %v\n%s", err, preview)
					}
					if roster == "existing" && !strings.Contains(preview, "Preview OK") {
						t.Fatalf("existing preview did not report validated readiness:\n%s", preview)
					}
					if roster == "fresh" && !strings.Contains(preview, "Spawn (up) validation is deferred") {
						t.Fatalf("fresh preview did not report truthful deferred validation:\n%s", preview)
					}
					if _, err := os.Stat(filepath.Join(project, ".agent-mail")); !os.IsNotExist(err) {
						t.Fatalf("preview mutated AMQ state: %v", err)
					}

					prepareArgs := append(append([]string{}, args...), "--goal", issue470Goal, "--prepare")
					_, _, err = captureOutput(t, func() error { return runRunStart(prepareArgs, "test") })
					if err != nil {
						t.Fatalf("prepare: %v", err)
					}
					liveArgs := issue470RunArgs(project, profile)
					if launchPath == "layout-finalization" {
						liveArgs = append(liveArgs, "--layout-preset", layoutPresetOneWindowPerAgent)
					} else {
						liveArgs = append(liveArgs, "--visibility", "detached")
					}
					stubSuccessfulRunStartGoalDelivery(t)
					_, _, err = captureOutput(t, func() error { return runRunStart(append(liveArgs, "--go"), "test") })
					if err != nil {
						t.Fatalf("live run: %v", err)
					}
					if len(backend.launches) != 1 {
						t.Fatalf("backend launches=%d, want 1", len(backend.launches))
					}
					if _, err := os.Stat(briefPathForProfile(project, profile, issue470Session)); err != nil {
						t.Fatalf("successful launch brief: %v", err)
					}
					legacy := filepath.Join(project, ".agent-mail", issue470Session)
					if profile == team.DefaultProfile {
						if info, err := os.Stat(filepath.Join(project, ".agent-mail")); err != nil || !info.IsDir() {
							t.Fatalf("default selected base not prepared: %v", err)
						}
						if _, err := os.Stat(legacy); !os.IsNotExist(err) {
							t.Fatalf("fake backend unexpectedly created a default mailbox: %v", err)
						}
					} else {
						selected := filepath.Join(project, ".agent-mail", profile, issue470Session)
						if info, err := os.Stat(selected); err != nil || !info.IsDir() {
							t.Fatalf("named selected root not prepared: %v", err)
						}
						if _, err := os.Stat(legacy); !os.IsNotExist(err) {
							t.Fatalf("named launch touched legacy default root: %v", err)
						}
					}
				})
			}
		}
	}
}

type issue470FailingBackend struct {
	launches int
}

func (b *issue470FailingBackend) Name() string                              { return "tmux" }
func (b *issue470FailingBackend) Validate(teamLaunchOptions) error          { return nil }
func (b *issue470FailingBackend) DryRun(team.Team, teamLaunchOptions) error { return nil }
func (b *issue470FailingBackend) Launch(team.Team, teamLaunchOptions) error {
	b.launches++
	return errors.New("injected immediately before backend launch")
}
func (b *issue470FailingBackend) LaunchWithResult(t team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	return teamLaunchResult{}, b.Launch(t, opts)
}
func (*issue470FailingBackend) preparedResultBeforeDispatch() {}

func TestIssue470LaunchFailureRollsBackPreparedState(t *testing.T) {
	for _, profile := range []string{team.DefaultProfile, "review"} {
		t.Run(profile, func(t *testing.T) {
			setupStrictFreshProjectAMQ(t)
			project := t.TempDir()
			writeIssue470Team(t, project, profile)
			backend := &issue470FailingBackend{}
			previous := teamLaunchBackends["tmux"]
			teamLaunchBackends["tmux"] = backend
			t.Cleanup(func() { teamLaunchBackends["tmux"] = previous })

			prepareIssue470Run(t, project, profile, "--visibility", "detached")
			args := append(issue470RunArgs(project, profile, "--visibility", "detached"), "--go")
			_, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
			if err == nil || !strings.Contains(err.Error(), "injected immediately before backend launch") {
				t.Fatalf("launch error=%v", err)
			}
			if backend.launches != 1 {
				t.Fatalf("backend attempts=%d, want 1", backend.launches)
			}
			if _, err := os.Stat(briefPathForProfile(project, profile, issue470Session)); err != nil {
				t.Fatalf("failed launch erased the prepared brief: %v", err)
			}
			if _, err := os.Stat(preparedRunPath(project, profile, issue470Session)); err != nil {
				t.Fatalf("failed launch erased the accepted preparation manifest: %v", err)
			}
			if _, err := os.Stat(filepath.Join(project, ".agent-mail")); !os.IsNotExist(err) {
				t.Fatalf("failed launch left reversible AMQ launch state: %v", err)
			}
			if _, err := os.Stat(notificationWatcherRuntimePath(project, profile, issue470Session)); !os.IsNotExist(err) {
				t.Fatalf("failed launch left a notification watcher: %v", err)
			}
			root := filepath.Join(project, ".agent-mail", issue470Session)
			if profile != team.DefaultProfile {
				root = filepath.Join(project, ".agent-mail", profile, issue470Session)
			}
			if _, err := launch.Read(filepath.Join(root, "agents", "cto")); !os.IsNotExist(err) {
				t.Fatalf("failed launch left a live agent record: %v", err)
			}
		})
	}
}

func TestIssue470PreviewDoesNotMaskConfiguredRootFailure(t *testing.T) {
	setupStrictFreshProjectAMQ(t)
	project := t.TempDir()
	writeIssue470Team(t, project, team.DefaultProfile)
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), []byte(`{"project":"broken","root":"broken"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_STRICT_FORCE_FAIL", "1")
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"--project", project, "--session", issue470Session, "--visibility", "detached"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "cannot determine root") {
		t.Fatalf("preview error=%v\n%s", err, out)
	}
	if strings.Contains(out, "Preview OK") {
		t.Fatalf("preview claimed success over configured-root failure:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, ".agent-mail")); !os.IsNotExist(err) {
		t.Fatalf("failing preview mutated AMQ state: %v", err)
	}
}
