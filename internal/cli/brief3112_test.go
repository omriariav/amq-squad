package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 3.1.1-2 (the plan reviewer's addition 2): the typed widen request
// exists (#844), so the refusal of a widen on a task blocked for anything
// else names both widenable blocks, keeps the reset pointer, and no longer
// points at t20.
func TestWidenRefusalNamesTheTypedWidenRequest(t *testing.T) {
	err := validateBlockedWiden(task.Task{ID: "t9", State: task.StateBlocked, BlockReason: "needs_decision"}, []string{"b.go"})
	if err == nil {
		t.Fatal("a widen of a task blocked needs_decision was not refused")
	}
	msg := err.Error()
	for _, want := range []string{"widen_not_requested", "is not blocked for files outside its declared set or by a seat's widen request (state blocked, reason needs_decision); there is nothing to widen", "amq-squad task reset t9 --pool RUNTIME:MODEL:EFFORT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal lacks %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "t20") || strings.Contains(msg, "until the typed block") {
		t.Errorf("the refusal still points at t20: %s", msg)
	}
}

// Brief 3.1.1-2 item 5: the drift check's verifier is the owner check, and
// without the owner's gate binding it verifies nothing (every release since
// the baseline stays an alert), never a file's presence.
func TestReleaseGateVerifierFailsClosedWithoutTheBinding(t *testing.T) {
	saved := gateBindingPath
	gateBindingPath = func() string { return filepath.Join(t.TempDir(), "no-binding.json") }
	defer func() { gateBindingPath = saved }()
	root := filepath.Join(t.TempDir(), "v310")
	store, err := gatestore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	err = releaseGateVerifier(contextResolution{Root: root, ProjectDir: "/p", Profile: "default"})("v310", store, gatestore.Record{GateID: "rel-tag"})
	if err == nil || !strings.Contains(err.Error(), "gate binding") {
		t.Fatalf("without the binding: %v", err)
	}
}
