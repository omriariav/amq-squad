package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

type namespaceMigrationFixture struct {
	project string
	source  squadnamespace.Ref
	target  squadnamespace.Ref
	message string
	task    string
	brief   string
}

func assertMigrationPayload(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("payload %s = %q, want %q", path, b, want)
	}
}

func TestRepositoryIgnoresPersistentNamespaceAdmissionLocks(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	// Either the blanket workspace rule or the specific admission-locks rule
	// satisfies the contract: persistent coordination locks never dirty
	// focused-test or operator worktrees.
	for _, want := range []string{".amq-squad/", ".amq-squad/namespace-admission/"} {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == want {
				return
			}
		}
	}
	t.Fatalf("repository .gitignore must ignore .amq-squad/namespace-admission/ (directly or via a blanket .amq-squad/ rule) so persistent coordination locks never dirty focused-test or operator worktrees")
}

func newNamespaceMigrationFixtureProfiles(t *testing.T, sourceProfile, targetProfile string) namespaceMigrationFixture {
	t.Helper()
	project := t.TempDir()
	var err error
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	source := squadnamespace.Resolve(project, sourceProfile, "source-run")
	target := squadnamespace.Resolve(project, targetProfile, "target-run")
	member := team.Member{Role: "qa", Handle: "qa", Binary: "codex"}
	sourceTeam := team.Team{Members: []team.Member{member}}
	sourceTeam.Members[0].Session = source.Session
	targetTeam := team.Team{Members: []team.Member{member}}
	targetTeam.Members[0].Session = target.Session
	if err := team.Write(project, sourceTeam); err != nil {
		t.Fatal(err)
	}
	if err := team.Write(project, targetTeam); err != nil {
		t.Fatal(err)
	}
	messagePath := filepath.Join(source.AMQRoot, "agents", "qa", "inbox", "cur", "message.md")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	message := "From: cto\nTo: qa\n\nexact durable message\n"
	if err := os.WriteFile(messagePath, []byte(message), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"new", "tmp"} {
		if err := os.MkdirAll(filepath.Join(source.AMQRoot, "agents", "qa", "inbox", dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	taskPath := filepath.Join(source.Paths.Tasks, "t1.json")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	task := `{"id":"t1","status":"in_progress","assigned_to":"qa"}` + "\n"
	if err := os.WriteFile(taskPath, []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(source.Paths.Brief), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := "# Durable brief\n"
	if err := os.WriteFile(source.Paths.Brief, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteUnderRecordLock(filepath.Join(source.AMQRoot, "agents", "qa"), launch.Record{
		CWD: project, Binary: "codex", Argv: []string{"codex"}, Session: source.Session,
		SharedWorkstream: true, Handle: "qa", Role: "qa", Root: source.AMQRoot,
		BaseRoot: namespaceMigrationBaseRoot(source), StartedAt: time.Unix(1, 0).UTC(),
		TeamProfile: source.Profile, TeamHome: project,
	}); err != nil {
		t.Fatal(err)
	}
	return namespaceMigrationFixture{project: project, source: source, target: target, message: message, task: task, brief: brief}
}

func newNamespaceMigrationFixture(t *testing.T) namespaceMigrationFixture {
	return newNamespaceMigrationFixtureProfiles(t, squadnamespace.DefaultProfile, "recovery")
}

func TestNamespaceWriterUnchangedContextProceedsAfterPreAdmissionPause(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	resolved, release := make(chan struct{}), make(chan struct{})
	originalHook := namespaceWriterBeforeAdmission
	var once sync.Once
	namespaceWriterBeforeAdmission = func(project, profile, session string) error {
		if filepath.Clean(project) == filepath.Clean(fx.project) && squadnamespace.ProfilesEqual(profile, fx.source.Profile) && session == fx.source.Session {
			once.Do(func() {
				close(resolved)
				<-release
			})
		}
		return nil
	}
	t.Cleanup(func() { namespaceWriterBeforeAdmission = originalHook })

	done := make(chan error, 1)
	go func() {
		done <- runTaskAdd([]string{
			"--project", fx.project, "--profile", fx.source.Profile,
			"--session", fx.source.Session, "--title", "unchanged context", "--class", "script",
		})
	}()
	<-resolved
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("unchanged namespace writer refused: %v", err)
	}
}
