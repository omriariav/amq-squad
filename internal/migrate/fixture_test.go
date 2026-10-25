//go:build unix

package migrate

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// fixture is a v2 checkout built in a temp dir from testdata/v2, plus a
// HOME with the owner's grants.yaml and registry.yaml.
type fixture struct {
	t       *testing.T
	project string
	home    string
	profile string
	owner   string // ~/.amq-squad/grants.yaml
}

func newFixture(t *testing.T) *fixture { return newFixtureNamed(t, "project") }

// newFixtureNamed is newFixture with the project directory called name.
func newFixtureNamed(t *testing.T, name string) *fixture {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	f := &fixture{t: t, project: filepath.Join(root, name), home: filepath.Join(root, "home")}
	t.Setenv("HOME", f.home)
	v2 := filepath.Join(f.project, ".amq-squad")
	f.profile = filepath.Join(v2, "team.json")
	f.put(f.profile, f.testdata("team.json"), 0o644)
	f.put(filepath.Join(v2, "launch.local.json"), f.testdata("launch.local.json"), 0o600)
	f.put(filepath.Join(v2, "team-rules.md"), f.testdata("team-rules.md"), 0o644)
	f.put(filepath.Join(v2, "briefs", "feature-x.md"), f.testdata("briefs/feature-x.md"), 0o644)
	f.put(filepath.Join(v2, "roles", "contracts.md"), f.testdata("roles/contracts.md"), 0o644)
	agents := filepath.Join(f.project, ".agent-mail", "feature-x", "agents")
	f.put(filepath.Join(agents, "counsel", "extensions", roleLayer, "role.md"), f.testdata("role-legal.md"), 0o644)
	f.put(filepath.Join(agents, "lead", "extensions", roleLayer, "role.md"), f.testdata("role-cto.md"), 0o644)
	f.owner = filepath.Join(f.home, ".amq-squad", "grants.yaml")
	f.put(f.owner, "schema: 1\nmode: auto\nmax_seats: 8\n", 0o600)
	f.put(filepath.Join(f.home, ".amq-squad", "registry.yaml"), "schema: 1\nhost:\n  ram_gb: 16\ntools: []\nentries: []\n", 0o600)
	if err := os.Chmod(filepath.Join(f.home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) testdata(name string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "v2", name))
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "{{PROJECT}}", f.project)
}

func (f *fixture) put(path, body string, mode os.FileMode) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) opts() Options { return Options{Profile: f.profile, OwnerPath: f.owner} }

// profileWith replaces the profile with its testdata text edited by edit.
func (f *fixture) profileWith(edit func(string) string) {
	f.t.Helper()
	f.put(f.profile, edit(f.testdata("team.json")), 0o644)
}

// entry is what a tree walk records of one path: identity (device and
// inode), mtime, mode and a content digest (B1, B7: dev+inode+mtime).
type entry struct {
	Dev, Ino uint64
	Mtime    int64
	Mode     fs.FileMode
	Sum      string
}

// snapshot maps every path under each root to its entry.
func snapshot(t *testing.T, roots ...string) map[string]entry {
	t.Helper()
	out := map[string]entry{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			st := info.Sys().(*syscall.Stat_t)
			e := entry{Dev: uint64(st.Dev), Ino: st.Ino, Mtime: info.ModTime().UnixNano(), Mode: info.Mode()}
			if info.Mode().IsRegular() {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				e.Sum = fmt.Sprintf("%x", sha256.Sum256(b))
			}
			out[path] = e
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// changed lists the paths whose entry differs between two snapshots, added
// or removed paths included.
func changed(before, after map[string]entry) []string {
	var out []string
	for p, e := range before {
		if a, ok := after[p]; !ok || a != e {
			out = append(out, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			out = append(out, p)
		}
	}
	return out
}
