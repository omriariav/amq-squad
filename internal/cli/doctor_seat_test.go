package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// Brief 3.0.1-t15: doctor --probe and --write-registry write the owner's
// registry, so inside a seat (the hooks' marker) they refuse before any read
// of the registry or any provider call. Every row runs runDoctor (the real
// flag path) on a temp HOME, and doctorExecute is swapped for a fake host, so
// a reverted guard writes only the temp registry and never probes the machine.

// seatDoctorEnv isolates the environment: a temp HOME, no seat marker and no
// session root inherited from a seat running these tests, the embedded
// runtime stubbed out. It returns the temp HOME.
func seatDoctorEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range append(append([]string{}, seatEnvMarkers...), "AMQ_SQUAD_LEAD", "AM_ROOT") {
		t.Setenv(name, "")
	}
	oldLoad, oldSHA, oldDir := piLoad, registry.EmbeddedRuntimeManifestSHA256, registry.EmbeddedRuntimeDirName
	piLoad = func() (piruntime.Embedded, error) { return piruntime.Embedded{}, piruntime.ErrNotEmbedded }
	t.Cleanup(func() {
		piLoad, registry.EmbeddedRuntimeManifestSHA256, registry.EmbeddedRuntimeDirName = oldLoad, oldSHA, oldDir
	})
	return home
}

// doctorRunObs is what the fake execute saw: whether doctor got past its
// guard, and the rows the fake run produced, including those of the
// release-drift hook runDoctor built (the clear checks always wrap it, so
// the rows, not the hook's presence, tell a skip from a run).
type doctorRunObs struct {
	reached bool
	checks  []doctorCheck
}

// fakeDoctorExecute replaces runDoctor's last step: it keeps the flags and
// the release-drift hook runDoctor set and runs executeDoctor against a fake
// host on home, whose provider seams fail the test if called. The drift
// hook runs as runDoctor built it; its gh is replaced by a counting fake
// (doctorReleaseLister), so a reverted seat skip never reaches GitHub.
func fakeDoctorExecute(t *testing.T, home string) *doctorRunObs {
	t.Helper()
	obs := &doctorRunObs{}
	old := doctorExecute
	t.Cleanup(func() { doctorExecute = old })
	doctorExecute = func(d doctorExecution) error {
		obs.reached = true
		fake := newDoctorExec(t, t.TempDir())
		var out bytes.Buffer
		fake.JSON, fake.Out, fake.Fix, fake.ReleaseDrift = true, &out, d.Fix, d.ReleaseDrift
		h := hostWith(t, home, "amq", "claude")
		h.WriteRegistry, h.Probe, h.ProbeRuns = d.Host.WriteRegistry, d.Host.Probe, d.Host.ProbeRuns
		h.ProbeCodex = func(context.Context, registry.Env, string, string, string) registry.CodexVerdict {
			t.Error("a provider probe ran")
			return registry.CodexVerdict{}
		}
		h.ProbeLocal = func(context.Context, *doctorHost, registry.Detection, registry.RehashResult, string) (registry.ProbeRecord, error) {
			t.Error("a local probe ran")
			return registry.ProbeRecord{}, nil
		}
		h.TTFT = func(context.Context, registry.Env, string) (registry.Latency, error) {
			t.Error("a TTFT probe ran")
			return registry.Latency{}, nil
		}
		fake.Host = h
		err := executeDoctor(fake)
		obs.checks = decodeDoctorJSON(t, &out).Checks
		return err
	}
	return obs
}

// countingReleaseLister is the drift check's gh in these rows: it counts
// calls and returns one tag, so a run that reaches it writes a baseline.
type countingReleaseLister struct{ calls int }

func (c *countingReleaseLister) Tags(context.Context, string) ([]mergetrain.Tag, error) {
	c.calls++
	return []mergetrain.Tag{{Name: "v1", SHA: "a1"}}, nil
}
func (c *countingReleaseLister) Releases(context.Context, string) ([]mergetrain.Release, error) {
	c.calls++
	return nil, nil
}

// registrySnapshot reads the registry file's bytes; present=false when absent.
func registrySnapshot(t *testing.T, home string) (body []byte, present bool) {
	t.Helper()
	b, err := os.ReadFile(registry.Path(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return b, true
}

func TestDoctorSeatRefusesProbeAndWriteRegistry(t *testing.T) {
	const canary = "# t15 canary registry, not a real one\nentries: []\n"
	for _, args := range [][]string{{"--probe"}, {"--write-registry"}, {"--probe", "--write-registry"}, {"--probe", "--runs", "2", "--json"}} {
		for _, marker := range seatEnvMarkers {
			for _, seeded := range []bool{false, true} {
				name := strings.Join(args, " ") + "/" + marker
				if seeded {
					name += "/registry-present"
				}
				t.Run(name, func(t *testing.T) {
					home := seatDoctorEnv(t)
					if seeded {
						hostWrite(t, registry.Path(home), canary)
					}
					before, had := registrySnapshot(t, home)
					t.Setenv(marker, "t15-seat")
					obs := fakeDoctorExecute(t, home)

					err := runDoctor(args, "test")
					if err == nil || !errors.Is(err, errSeatOwnerState) {
						t.Fatalf("doctor %v inside a seat: err %v, want %v", args, err, errSeatOwnerState)
					}
					flag := "--write-registry"
					if args[0] == "--probe" {
						flag = "--probe"
					}
					for _, want := range []string{"seat_owner_state_refused", marker + " is set", "amq-squad doctor " + flag, "owner"} {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("refusal %q does not name %q", err, want)
						}
					}
					if obs.reached {
						t.Fatal("doctor ran past the seat guard")
					}
					after, has := registrySnapshot(t, home)
					if had != has || !bytes.Equal(before, after) {
						t.Fatalf("registry changed inside a seat: present %v -> %v\nbefore %q\nafter  %q", had, has, before, after)
					}
				})
			}
		}
	}
}

// doctor --fix removes pi runtimes under ~/.amq-squad/runtime, so inside a
// seat it refuses with the same reason and the runtime dir is unchanged: an
// unpacked current runtime plus two older ones, the oldest of which --fix
// would prune.
func TestDoctorSeatRefusesFix(t *testing.T) {
	for _, marker := range seatEnvMarkers {
		t.Run(marker, func(t *testing.T) {
			seatDoctorEnv(t)
			_, home := piFixture(t)
			chdir(t, t.TempDir())
			if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(home, ".amq-squad", "runtime")
			addOldRuntime(t, base, "pi-old-1", 3*time.Hour)
			addOldRuntime(t, base, "pi-old-2", 2*time.Hour)
			before := runtimeTree(t, base)
			t.Setenv(marker, "t15-seat")
			obs := fakeDoctorExecute(t, home)

			err := runDoctor([]string{"--fix"}, "test")
			if err == nil || !errors.Is(err, errSeatOwnerState) {
				t.Fatalf("doctor --fix inside a seat: err %v, want %v", err, errSeatOwnerState)
			}
			for _, want := range []string{"seat_owner_state_refused", marker + " is set", "amq-squad doctor --fix", "~/.amq-squad/runtime"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
			if obs.reached {
				t.Fatal("doctor --fix ran past the seat guard")
			}
			if after := runtimeTree(t, base); after != before {
				t.Fatalf("runtime dir changed inside a seat:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

// runtimeTree lists every path under base with its mode, size and mtime.
func runtimeTree(t *testing.T, base string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		fmt.Fprintf(&b, "%s %s %d %d;", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// Plain doctor writes nothing and still runs inside a seat.
func TestDoctorSeatPlainDoctorStillRuns(t *testing.T) {
	home := seatDoctorEnv(t)
	t.Setenv("AM_ME", "t15-seat")
	obs := fakeDoctorExecute(t, home)
	if err := runDoctor(nil, "test"); err != nil {
		t.Fatalf("plain doctor inside a seat: %v", err)
	}
	if !obs.reached {
		t.Fatal("plain doctor was refused inside a seat")
	}
	if _, present := registrySnapshot(t, home); present {
		t.Fatal("plain doctor wrote the registry")
	}
}

// Outside a seat (the lead marker alone is not a seat) --write-registry runs
// as before and writes the (temp) registry.
func TestDoctorWithoutSeatMarkerWritesRegistry(t *testing.T) {
	for _, lead := range []string{"", "1"} {
		home := seatDoctorEnv(t)
		t.Setenv("AMQ_SQUAD_LEAD", lead)
		t.Setenv("AM_ME", "   ")
		obs := fakeDoctorExecute(t, home)
		if err := runDoctor([]string{"--write-registry"}, "test"); err != nil {
			t.Fatalf("doctor --write-registry outside a seat (AMQ_SQUAD_LEAD=%q): %v", lead, err)
		}
		if !obs.reached {
			t.Fatal("doctor was refused outside a seat")
		}
		if _, present := registrySnapshot(t, home); !present {
			t.Fatal("doctor --write-registry outside a seat wrote no registry")
		}
	}
}

// driftFixture is a temp HOME whose owner grants carry a merge entry and an
// absolute AM_ROOT: outside a seat plain doctor runs the drift check there,
// calls gh (the counting fake) and writes a baseline under the root's
// extensions dir. It returns the root and the fake.
func driftFixture(t *testing.T, home string) (string, *countingReleaseLister) {
	t.Helper()
	hostWrite(t, filepath.Join(home, ".amq-squad", "grants.yaml"), "schema: 1\nmerge:\n  - repository: o/r\n    base_branch: main\n    size_cap_lines: 300\n    push_url: https://github.com/o/r.git\n")
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	lister := &countingReleaseLister{}
	old := doctorReleaseLister
	t.Cleanup(func() { doctorReleaseLister = old })
	doctorReleaseLister = func() mergetrain.ReleaseLister { return lister }
	return root, lister
}

// Plain doctor inside a seat installs no release-drift hook: the one drift
// row says the check was skipped in a seat and the owner runs it; gh is
// never called and nothing is written under <AM_ROOT>/extensions.
func TestDoctorSeatSkipsReleaseDrift(t *testing.T) {
	for _, marker := range seatEnvMarkers {
		t.Run(marker, func(t *testing.T) {
			home := seatDoctorEnv(t)
			root, lister := driftFixture(t, home)
			t.Setenv(marker, "t15-seat")
			obs := fakeDoctorExecute(t, home)
			_ = runDoctor(nil, "test")
			if !obs.reached {
				t.Fatal("plain doctor was refused inside a seat")
			}
			var drift []doctorCheck
			for _, c := range obs.checks {
				if strings.HasPrefix(c.Name, "release drift") {
					drift = append(drift, c)
				}
			}
			if len(drift) != 1 || drift[0].Name != "release drift" || !strings.Contains(drift[0].Detail, "skipped: "+marker+" is set") || !strings.Contains(drift[0].Detail, "the owner runs it") {
				t.Fatalf("drift rows inside a seat: %+v", drift)
			}
			if lister.calls != 0 {
				t.Fatalf("the drift check called gh %d times inside a seat", lister.calls)
			}
			if _, err := os.Lstat(filepath.Join(root, "extensions")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("doctor wrote under %s/extensions inside a seat (lstat err %v)", root, err)
			}
		})
	}
}

// Outside a seat the same fixture runs the drift check as before: gh is
// called, the repository's row is present and the baseline is written. This
// is what makes the seat row above non-vacuous.
func TestDoctorWithoutSeatRunsReleaseDrift(t *testing.T) {
	home := seatDoctorEnv(t)
	root, lister := driftFixture(t, home)
	obs := fakeDoctorExecute(t, home)
	_ = runDoctor(nil, "test")
	if lister.calls == 0 || findCheck(obs.checks, "release drift o/r") == nil {
		t.Fatalf("drift outside a seat: gh calls %d, rows %+v", lister.calls, obs.checks)
	}
	if _, err := os.Stat(filepath.Join(mergetrain.AlertsDir(root), "baseline-o--r.json")); err != nil {
		t.Fatalf("no baseline outside a seat: %v", err)
	}
}

func TestSeatEnvMarker(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{nil, ""},
		{map[string]string{"AM_ME": " \t"}, ""},
		{map[string]string{"AMQ_SQUAD_LEAD": "1"}, ""},
		{map[string]string{"AM_ME": "cto"}, "AM_ME"},
		{map[string]string{"AMQ_SQUAD_SEAT": "cto"}, "AMQ_SQUAD_SEAT"},
		{map[string]string{"AMQ_SQUAD_ROLE_PATH": "/r/role.md"}, "AMQ_SQUAD_ROLE_PATH"},
		{map[string]string{"AM_ME": "cto", "AMQ_SQUAD_SEAT": "cto"}, "AM_ME"},
	} {
		if got := seatEnvMarker(func(n string) string { return tc.env[n] }); got != tc.want {
			t.Fatalf("seatEnvMarker(%v) = %q, want %q", tc.env, got, tc.want)
		}
	}
}
