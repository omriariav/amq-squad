package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/attention"
	"github.com/omriariav/amq-squad/v2/internal/notifier"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	notifyProcessHelperEnv = "AMQ_NOTIFY_PROCESS_HELPER"
	notifyProcessStateEnv  = "AMQ_NOTIFY_PROCESS_STATE"
	notifyProcessCallsEnv  = "AMQ_NOTIFY_PROCESS_CALLS"
	notifyProcessStartEnv  = "AMQ_NOTIFY_PROCESS_START"
)

type processRecordingSink struct{ callsPath string }

func (processRecordingSink) ID() string { return "hook" }

func (s processRecordingSink) Deliver(context.Context, attention.Event) error {
	// Hold the reservation long enough for the competing process to observe it.
	time.Sleep(100 * time.Millisecond)
	f, err := os.OpenFile(s.callsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("delivered\n")
	return err
}

func TestNotificationDeliveryProcessHelper(t *testing.T) {
	if os.Getenv(notifyProcessHelperEnv) != "1" {
		return
	}
	startPath := os.Getenv(notifyProcessStartEnv)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(startPath); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for multiprocess start barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}

	oldFactory := notificationSinkFactory
	defer func() { notificationSinkFactory = oldFactory }()
	notificationSinkFactory = func(team.OperatorNotificationSinkConfig) notifier.Sink {
		return processRecordingSink{callsPath: os.Getenv(notifyProcessCallsEnv)}
	}
	item := operatorAttention{
		EventType: "gate",
		Key:       "default/s\x00gate\x00gate/multiprocess",
		LatestID:  "gate-1",
		Profile:   team.DefaultProfile,
		Session:   "s",
		Thread:    "gate/multiprocess",
	}
	policy := team.OperatorNotificationPolicy{
		Enabled: true,
		Events:  []string{"gate"},
		Sinks:   []team.OperatorNotificationSinkConfig{{ID: "hook", Type: "command", Argv: []string{"unused"}, Timeout: "1s"}},
	}
	if _, _, err := deliverNotificationSinksPersisted(context.Background(), "/project", os.Getenv(notifyProcessStateEnv), []operatorAttention{item}, policy, time.Hour, time.Now(), false); err != nil {
		t.Fatal(err)
	}
}

func TestPersistedReservationDeduplicatesAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "notify-state.json")
	callsPath := filepath.Join(dir, "calls")
	startPath := filepath.Join(dir, "start")
	baseEnv := append(os.Environ(),
		notifyProcessHelperEnv+"=1",
		notifyProcessStateEnv+"="+statePath,
		notifyProcessCallsEnv+"="+callsPath,
		notifyProcessStartEnv+"="+startPath,
	)
	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, len(commands))
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestNotificationDeliveryProcessHelper$")
		commands[i].Env = baseEnv
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		if err := commands[i].Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
	}
	if err := os.WriteFile(startPath, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outputs[i].Bytes())
		}
	}

	body, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "delivered\n"); got != 1 {
		t.Fatalf("external sink deliveries=%d, want exactly 1; body=%q", got, body)
	}
	state, err := readNotifyState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := state.Items["default/s\x00gate\x00gate/multiprocess"]
	if !ok {
		t.Fatal("deduplicated event missing from persisted state")
	}
	delivery, ok := rec.Deliveries["hook"]
	if !ok || delivery.Fingerprint != "gate-1" || delivery.ReservationToken != "" {
		t.Fatalf("unexpected committed delivery: %+v", delivery)
	}
}
