package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeWakeLockRejectsIncompleteStateBinding(t *testing.T) {
	_, err := decodeWakeLockFile([]byte(`{"pid":1,"generation":7,"state_generation":7,"wake_mode":"inject-via-v1"}`))
	if err == nil || !strings.Contains(err.Error(), "incomplete state binding") {
		t.Fatalf("decode error = %v, want incomplete binding", err)
	}
}

func TestVerifiedWakeRecordBindingUsesAuthoritativeCheckForStateBoundLock(t *testing.T) {
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writeWakeLock(t, agentDir, wakeLockFile{
		PID: 4242, Root: root, WakeMode: "inject-via-v1",
		TargetDigest: "sha256:target", Generation: json.RawMessage("7"),
		StateGeneration: json.RawMessage("7"), StateDigest: "sha256:target",
	})
	previous := runWakeCheckForBinding
	t.Cleanup(func() { runWakeCheckForBinding = previous })
	var got amqCommandRequest
	runWakeCheckForBinding = func(req amqCommandRequest) ([]byte, error) {
		got = req
		return []byte(fmt.Sprintf(`{"schema":1,"agent":"qa","root":%q,"live_wake":true,"wake_status":"live","wake_pid":4242}`, root)), nil
	}
	binding, err := verifiedWakeRecordBinding(agentDir, root, "qa", downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true}))
	if err != nil || binding.PID != 4242 {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	want := []string{"wake", "check", "--root", root, "--me", "qa", "--json", "--json-schema", "1"}
	if strings.Join(got.Arg, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("wake check args=%q want=%q", got.Arg, want)
	}
}

func TestVerifiedWakeRecordBindingRejectsUnverifiedStateBoundLock(t *testing.T) {
	agentDir := t.TempDir()
	root := filepath.Dir(agentDir)
	writeWakeLock(t, agentDir, wakeLockFile{
		PID: 4242, Root: root, WakeMode: "owner-inject-via-v1",
		TargetDigest: "sha256:target", Generation: json.RawMessage("7"),
		StateGeneration: json.RawMessage("7"), StateDigest: "sha256:target",
	})
	previous := runWakeCheckForBinding
	t.Cleanup(func() { runWakeCheckForBinding = previous })
	runWakeCheckForBinding = func(amqCommandRequest) ([]byte, error) {
		return nil, errors.New("wake state is unverified")
	}
	if _, err := verifiedWakeRecordBinding(agentDir, root, "qa", downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true})); err == nil || !strings.Contains(err.Error(), "authoritative amq wake check failed") {
		t.Fatalf("binding error = %v, want authoritative refusal", err)
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("unverified state-bound lock must be preserved: %v", err)
	}
}
