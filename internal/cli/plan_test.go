package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// planTestSetup seeds launchapiTestTeam's roster and stubs AMQ env
// resolution the same way TestLaunchapiBackendProbePrepareIsSideEffectFree
// does, so planPrepare can run end to end (real launchapi.Prepare, no real
// amq binary or model provider) with an isolated trust store.
func planTestSetup(t *testing.T) team.Team {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tm := launchapiTestTeam(t)
	launchapiTestStubAMQEnv(t, tm.Project)
	return tm
}

// TestPlanIsZeroWrite proves gh#756's core guarantee: the seeded project
// tree is byte-identical before and after planPrepare, reusing the same
// snapshotTestTree proof TestLaunchapiBackendProbePrepareIsSideEffectFree
// already established for the two-phase prepare call plan itself calls
// into -- plan adds no write of its own on top.
func TestPlanIsZeroWrite(t *testing.T) {
	tm := planTestSetup(t)

	before := snapshotTestTree(t, tm.Project)
	prepared, err := planPrepare(tm.Project, team.DefaultProfile, "s")
	if err != nil {
		t.Fatalf("planPrepare: %v", err)
	}
	after := snapshotTestTree(t, tm.Project)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("project tree changed across planPrepare:\n before: %v\n after:  %v", before, after)
	}
	if prepared.Result.SubjectDigest == "" {
		t.Fatal("SubjectDigest is empty")
	}
}

// TestPlanJSONRequestRoundTripsThroughAMQLaunchPrepare proves gh#756's
// --json reproducibility claim in its honest, in-process form (cto's
// decision on task/t6): replaying planPrepare's exact PrepareRequestV1
// through launchapi.Prepare again yields the identical subject_digest and
// plan_digest, and the --json envelope carries that same request verbatim
// (not re-derived), so a caller who piped the emitted JSON to
// 'amq launch --plan - --prepare' would be replaying this exact request.
func TestPlanJSONRequestRoundTripsThroughAMQLaunchPrepare(t *testing.T) {
	tm := planTestSetup(t)

	prepared, err := planPrepare(tm.Project, team.DefaultProfile, "s")
	if err != nil {
		t.Fatalf("planPrepare: %v", err)
	}

	var buf bytes.Buffer
	if err := writeJSONEnvelope(&buf, "plan", planEnvelopeData{Request: prepared.Request, Result: prepared.Result}); err != nil {
		t.Fatalf("writeJSONEnvelope: %v", err)
	}
	var envelope struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Data          struct {
			Request launchapi.PrepareRequestV1 `json:"request"`
			Result  launchapi.PrepareResultV1  `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal plan envelope: %v", err)
	}
	if envelope.Kind != "plan" {
		t.Fatalf("kind = %q, want %q", envelope.Kind, "plan")
	}
	if !reflect.DeepEqual(envelope.Data.Request, prepared.Request) {
		t.Fatalf("emitted request does not round-trip through the JSON envelope:\n got:  %+v\n want: %+v", envelope.Data.Request, prepared.Request)
	}

	replayed, err := launchapi.Prepare(context.Background(), envelope.Data.Request)
	if err != nil {
		t.Fatalf("replay launchapi.Prepare on the emitted request: %v", err)
	}
	if replayed.SubjectDigest != prepared.Result.SubjectDigest {
		t.Fatalf("replayed subject_digest = %q, want %q", replayed.SubjectDigest, prepared.Result.SubjectDigest)
	}
	if replayed.PlanDigest != prepared.Result.PlanDigest {
		t.Fatalf("replayed plan_digest = %q, want %q", replayed.PlanDigest, prepared.Result.PlanDigest)
	}
}

// TestPlanPrintsRequiredActionsVerbatim proves plan's human output and
// --json envelope both carry launchapi's own RequiredActionV1 fields
// (action_id, kind, reason_code, allowed_decisions) byte-for-byte, with no
// amq-squad paraphrase of what an action means or which choices it allows.
func TestPlanPrintsRequiredActionsVerbatim(t *testing.T) {
	actions := launchapiTestRequiredActions()
	result := launchapi.PrepareResultV1{
		Outcome:         "action_required",
		SubjectDigest:   "sha256:deadbeef",
		PlanDigest:      "sha256:cafef00d",
		RequiredActions: actions,
	}

	var human bytes.Buffer
	printPlanResult(&human, result)
	out := human.String()
	for _, action := range actions {
		if !bytes.Contains(human.Bytes(), []byte(action.ActionID)) {
			t.Fatalf("human output missing action_id %q verbatim:\n%s", action.ActionID, out)
		}
		if !bytes.Contains(human.Bytes(), []byte(action.Kind)) {
			t.Fatalf("human output missing kind %q verbatim:\n%s", action.Kind, out)
		}
		if !bytes.Contains(human.Bytes(), []byte(action.ReasonCode)) {
			t.Fatalf("human output missing reason_code %q verbatim:\n%s", action.ReasonCode, out)
		}
		for _, choice := range action.AllowedDecisions {
			if !bytes.Contains(human.Bytes(), []byte(choice)) {
				t.Fatalf("human output missing allowed_decision %q verbatim for action %q:\n%s", choice, action.ActionID, out)
			}
		}
	}

	var jsonBuf bytes.Buffer
	if err := writeJSONEnvelope(&jsonBuf, "plan", planEnvelopeData{Result: result}); err != nil {
		t.Fatalf("writeJSONEnvelope: %v", err)
	}
	var envelope struct {
		Data struct {
			Result launchapi.PrepareResultV1 `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal plan envelope: %v", err)
	}
	if !reflect.DeepEqual(envelope.Data.Result.RequiredActions, actions) {
		t.Fatalf("--json required_actions diverged from launchapi's own values:\n got:  %+v\n want: %+v", envelope.Data.Result.RequiredActions, actions)
	}
}
