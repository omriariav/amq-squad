//go:build unix

package seatlaunch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// multilineCanary is AMQ stderr with a credential key on one line and its
// quoted value on the next, built at run time; it matches no real
// credential shape (briefs ruling 18).
func multilineCanary(t *testing.T, tag string) (stderr, value string) {
	t.Helper()
	value = "t17" + tag + "value" + "0093"
	stderr = "api" + "_key:\n  \"" + value + "\""
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(stderr) {
			t.Fatalf("the canary matches a real credential shape: %s", re)
		}
	}
	return stderr, value
}

// invalidJSONAt answers the named amq launch step ("prepare" or "apply")
// with output that is not JSON and the canary on stderr; any other call
// goes to the fake.
func invalidJSONAt(f *fakeAMQ, step, stderr string) func(context.Context, string, []string, []byte, []string, string) ([]byte, []byte, int, error) {
	return func(ctx context.Context, amq string, args []string, stdin []byte, env []string, dir string) ([]byte, []byte, int, error) {
		if slices.Contains(args, "--"+step) {
			f.calls = append(f.calls, args)
			return []byte("amq: not json"), []byte(stderr), 2, nil
		}
		return f.run(ctx, amq, args, stdin, env, dir)
	}
}

// Codex finding 1 on t17, the per-seat paths: AMQ output that is not JSON
// made the launch prefix its error message to AMQ's stderr before the
// redaction, which then missed a key whose quoted value is on the next
// line. Through prepare and through apply, the value is in neither the
// seat_launch_failed decision nor the state file (status reads it there).
// Reverted (the stderr wrapped raw), both carry it.
func TestInvalidJSONStderrIsRedactedBeforeItIsWrapped(t *testing.T) {
	for _, step := range []string{"prepare", "apply"} {
		t.Run(step, func(t *testing.T) {
			stderr, value := multilineCanary(t, step)
			f := newLauncherFixture(t, "applied")
			f.publish = false
			f.l.Options.RunAMQ = invalidJSONAt(f.fake, step, stderr)
			store, err := task.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(t.TempDir(), "scheduler.json")
			s := &scheduler.Scheduler{Store: store, Now: time.Now, Launcher: f.l, Incarnation: "t", Config: scheduler.Config{MaxSeats: 1, StatePath: statePath}}
			f.l.Post = s.Post
			if _, err := store.Add(task.AddInput{Title: "a", Class: "script", Pool: &poolA}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Tick(); err != nil {
				t.Fatal(err)
			}
			f.l.Wait()
			ds, err := s.Tick()
			if err != nil {
				t.Fatal(err)
			}
			got := kinds(ds, scheduler.KindSeatLaunchFailed)
			if len(got) != 1 || !strings.Contains(got[0].Reason, "without a") || !strings.Contains(got[0].Reason, redact.Placeholder) {
				t.Fatalf("decisions %+v", ds)
			}
			if strings.Contains(got[0].Reason, value) {
				t.Fatalf("the decision carries the credential value: %s", got[0].Reason)
			}
			raw, err := os.ReadFile(statePath)
			if err != nil || !strings.Contains(string(raw), "launch_failures") || strings.Contains(string(raw), value) {
				t.Fatalf("the state file (%v):\n%s", err, raw)
			}
		})
	}
}

// Codex finding 1, the roster path: an Apply whose output is not JSON
// ends the roster launch; neither roster_launch_failed, nor each seat's
// seat_launch_failed, nor the state file carries the value. Reverted, all
// three do.
func TestInvalidJSONRosterFailureIsRedacted(t *testing.T) {
	stderr, value := multilineCanary(t, "roster")
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}, false)
	f.l.Options.RunAMQ = invalidJSONAt(f.fake, "apply", stderr)
	statePath := filepath.Join(t.TempDir(), "scheduler.json")
	f.s.Config.StatePath = statePath
	ds, err := f.s.Tick()
	var failed *scheduler.RosterLaunchError
	if !errors.As(err, &failed) || strings.Contains(err.Error(), value) {
		t.Fatalf("tick error %v", err)
	}
	rl := kinds(ds, "roster_launch_failed")
	sl := kinds(ds, scheduler.KindSeatLaunchFailed)
	if len(rl) != 1 || len(sl) != 2 {
		t.Fatalf("decisions %+v", ds)
	}
	for _, d := range append(rl, sl...) {
		if strings.Contains(d.Reason, value) || !strings.Contains(d.Reason, redact.Placeholder) {
			t.Fatalf("the decision %+v", d)
		}
	}
	raw, err := os.ReadFile(statePath)
	if err != nil || strings.Contains(string(raw), value) {
		t.Fatalf("the state file (%v):\n%s", err, raw)
	}
}
