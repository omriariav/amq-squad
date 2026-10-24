package grants

import (
	"path/filepath"
	"reflect"
	"testing"
)

// 4-3 (Codex P1 1, second pass): the fleet grant names the canonical
// checkout it permits for a repository, and optionally its remote.
func TestFleetRepositoryPaths(t *testing.T) {
	g, err := ParseOwner("fleet:\n  max_squads: 2\n  permitted_repositories: [owner/app]\n  repository_paths:\n    - repository: owner/app\n      path: /Users/owner/code/app\n      remote: github.com/owner/app\n    - repository: owner/web\n      path: /Users/owner/code/web\n", "g")
	if err != nil {
		t.Fatal(err)
	}
	want := []RepositoryPath{{Repository: "owner/app", Path: "/Users/owner/code/app", Remote: "github.com/owner/app"}, {Repository: "owner/web", Path: "/Users/owner/code/web"}}
	if !reflect.DeepEqual(g.Fleet.RepositoryPaths, want) || g.Fleet.MaxSquads != 2 {
		t.Fatalf("repository_paths = %+v", g.Fleet)
	}
	for name, text := range map[string]string{
		"relative path":   "fleet:\n  repository_paths:\n    - repository: owner/app\n      path: code/app\n",
		"no path":         "fleet:\n  repository_paths:\n    - repository: owner/app\n",
		"bad repository":  "fleet:\n  repository_paths:\n    - repository: app\n      path: /a\n",
		"remote with url": "fleet:\n  repository_paths:\n    - repository: owner/app\n      path: /a\n      remote: https://github.com/owner/app\n",
		"unknown key":     "fleet:\n  repository_paths:\n    - repository: owner/app\n      path: /a\n      branch: main\n",
	} {
		if _, err := ParseOwner(text, "g"); err == nil {
			t.Errorf("%s parsed", name)
		}
	}
}

// 4-3: a repository's grant is a squad grant. The owner's fleet grant does
// not reach it, and a squad.yaml that names fleet keys is ignored by name.
func TestRepositoryGrantCarriesNoFleetGrant(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "home", ".amq-squad", "grants.yaml")
	repo := filepath.Join(dir, "repo")
	writeFile(t, owner, "fleet:\n  max_squads: 3\n  permitted_repositories:\n    - owner/app\n", 0o600)
	writeFile(t, filepath.Join(repo, "squad.yaml"), "fleet:\n  max_squads: 9\nmax_squads: 9\npermitted_repositories: [owner/other]\n", 0o644)
	op, err := LoadFrom(owner, "")
	if err != nil || op.Fleet.MaxSquads != 3 || !op.FleetScope() {
		t.Fatalf("owner grant: %+v %v", op.Fleet, err)
	}
	sq, err := LoadFrom(owner, repo)
	if err != nil {
		t.Fatal(err)
	}
	if sq.FleetScope() || !reflect.DeepEqual(sq.Fleet, Fleet{}) {
		t.Fatalf("a repository grant carries a fleet grant: %+v", sq.Fleet)
	}
	want := []string{"fleet " + ReasonFleetInSquad, "max_squads " + ReasonFleetInSquad, "permitted_repositories " + ReasonFleetInSquad}
	if !reflect.DeepEqual(sq.Ignored, want) {
		t.Fatalf("ignored = %q, want %q", sq.Ignored, want)
	}
	if !(Fleet{PermittedRepositories: []string{"Owner/App"}}).RepositoryPermitted("owner/app") || (Fleet{}).RepositoryPermitted("owner/app") {
		t.Fatal("RepositoryPermitted: case-insensitive match; an empty list permits none")
	}
}
