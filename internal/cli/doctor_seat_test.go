package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

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

// fakeDoctorExecute replaces runDoctor's last step: it keeps the flags
// runDoctor set on the host and runs executeDoctor against a fake host on
// home, whose provider seams fail the test if called. *reached records that
// doctor got past its guard.
func fakeDoctorExecute(t *testing.T, home string, reached *bool) {
	t.Helper()
	old := doctorExecute
	t.Cleanup(func() { doctorExecute = old })
	doctorExecute = func(d doctorExecution) error {
		*reached = true
		fake := newDoctorExec(t, t.TempDir())
		fake.JSON, fake.Out = true, &bytes.Buffer{}
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
		return executeDoctor(fake)
	}
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
					reached := false
					fakeDoctorExecute(t, home, &reached)

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
					if reached {
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

// Plain doctor writes nothing and still runs inside a seat.
func TestDoctorSeatPlainDoctorStillRuns(t *testing.T) {
	home := seatDoctorEnv(t)
	t.Setenv("AM_ME", "t15-seat")
	reached := false
	fakeDoctorExecute(t, home, &reached)
	if err := runDoctor(nil, "test"); err != nil {
		t.Fatalf("plain doctor inside a seat: %v", err)
	}
	if !reached {
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
		reached := false
		fakeDoctorExecute(t, home, &reached)
		if err := runDoctor([]string{"--write-registry"}, "test"); err != nil {
			t.Fatalf("doctor --write-registry outside a seat (AMQ_SQUAD_LEAD=%q): %v", lead, err)
		}
		if !reached {
			t.Fatal("doctor was refused outside a seat")
		}
		if _, present := registrySnapshot(t, home); !present {
			t.Fatal("doctor --write-registry outside a seat wrote no registry")
		}
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
