package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func seedTaskScopedGate(t *testing.T, taskID string, terminal string) (string, string) {
	t.Helper()
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	request := operatorauth.GateRequestContext{
		SchemaVersion: operatorauth.GateRequestSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Gate: "gate/release", Thread: "gate/release",
		Namespace: operatorauth.NamespaceBinding{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", NamespaceID: "default/s", Generation: "none"},
		GateKind:  "tag", Action: "tag", Target: "tag v2.22.0", TaskID: taskID,
	}
	context, err := json.Marshal(map[string]any{"authorization_request": request})
	if err != nil {
		t.Fatal(err)
	}
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
		ID: "request-1", From: "cto", To: "user", Thread: "gate/release",
		Subject: "APPROVAL: release", Kind: "question", Created: notifyNow, Context: string(context),
	})
	if terminal != "" {
		terminalContext, err := json.Marshal(map[string]any{"gate": map[string]any{
			"state": terminal, "request_message_id": "request-1", "requester": "cto", "thread": "gate/release", "actor": "cto",
		}})
		if err != nil {
			t.Fatal(err)
		}
		seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
			ID: "terminal-1", From: "cto", To: "user", Thread: "gate/release", ReplyTo: "request-1",
			Subject: strings.ToUpper(terminal) + ": release", Kind: "status", Created: notifyNow.Add(time.Minute), Context: string(terminalContext),
		})
	}
	return project, base
}

func TestTaskCompletionGateCorrelationPreservesUnresolvedDecision(t *testing.T) {
	project, _ := seedTaskScopedGate(t, "t1", "")
	correlation, err := assessTaskCompletionGateCorrelation(project, team.DefaultProfile, "s", "t1", "release", "request-1", notifyNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if correlation.State != "open_preserved" || correlation.Suppressed || correlation.RequestMessageID != "request-1" || correlation.TaskID != "t1" || correlation.RequestSHA256 == "" {
		t.Fatalf("unresolved gate correlation = %+v", correlation)
	}
}

func TestTaskCompletionGateCorrelationSuppressesOnlyDurablyTerminalRequest(t *testing.T) {
	for _, terminal := range []string{"closed", "withdrawn"} {
		t.Run(terminal, func(t *testing.T) {
			project, _ := seedTaskScopedGate(t, "t1", terminal)
			correlation, err := assessTaskCompletionGateCorrelation(project, team.DefaultProfile, "s", "t1", "gate/release", "request-1", notifyNow.Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if correlation.State != terminal || !correlation.Suppressed {
				t.Fatalf("terminal gate correlation = %+v", correlation)
			}
		})
	}
}

func TestTaskCompletionGateCorrelationRejectsTaskAndNamespaceMismatch(t *testing.T) {
	project, _ := seedTaskScopedGate(t, "t2", "")
	if _, err := assessTaskCompletionGateCorrelation(project, team.DefaultProfile, "s", "t1", "release", "request-1", notifyNow.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "exact task namespace") {
		t.Fatalf("task mismatch should fail closed, got %v", err)
	}
	if _, err := assessTaskCompletionGateCorrelation(project, "other", "s", "t2", "release", "request-1", notifyNow.Add(time.Minute)); err == nil {
		t.Fatal("profile mismatch should fail closed")
	}
}

func TestTaskCompletionGateCorrelationRejectsInvalidNewerSupersessionEvidence(t *testing.T) {
	tests := []struct {
		name          string
		messageThread string
		context       func(project string) string
		want          string
	}{
		{
			name: "malformed", messageThread: "gate/release",
			context: func(string) string { return `{"authorization_request":` }, want: "message scan degraded",
		},
		{
			name: "repaired raw thread", messageThread: "gate//release",
			context: func(project string) string {
				return taskGateRequestContextJSON(t, project, "s", "default/s", "t1", operatorauth.GateRequestSchemaVersion)
			},
			want: "newer non-authoritative or mismatched",
		},
		{
			name: "invalid typed", messageThread: "gate/release",
			context: func(project string) string {
				return taskGateRequestContextJSON(t, project, "s", "default/s", "t1", 999)
			},
			want: "newer non-authoritative or mismatched",
		},
		{
			name: "wrong namespace", messageThread: "gate/release",
			context: func(project string) string {
				return taskGateRequestContextJSON(t, project, "other", "default/other", "t1", operatorauth.GateRequestSchemaVersion)
			},
			want: "newer non-authoritative or mismatched",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project, base := seedTaskScopedGate(t, "t1", "")
			seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
				ID: "request-2", From: "cto", To: "user", Thread: tc.messageThread,
				Subject: "APPROVAL: newer", Kind: "question", Created: notifyNow.Add(time.Minute), Context: tc.context(project),
			})
			correlation, err := assessTaskCompletionGateCorrelation(project, team.DefaultProfile, "s", "t1", "release", "request-1", notifyNow.Add(2*time.Minute))
			if err == nil || !strings.Contains(err.Error(), tc.want) || correlation != nil {
				t.Fatalf("invalid newer request suppressed attention: correlation=%+v err=%v", correlation, err)
			}
		})
	}
}

func TestTaskCompletionGateCorrelationAcceptsExactNewerSupersession(t *testing.T) {
	project, base := seedTaskScopedGate(t, "t1", "")
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
		ID: "request-2", From: "cto", To: "user", Thread: "gate/release",
		Subject: "APPROVAL: newer", Kind: "question", Created: notifyNow.Add(time.Minute),
		Context: taskGateRequestContextJSON(t, project, "s", "default/s", "t1", operatorauth.GateRequestSchemaVersion),
	})
	correlation, err := assessTaskCompletionGateCorrelation(project, team.DefaultProfile, "s", "t1", "release", "request-1", notifyNow.Add(2*time.Minute))
	if err != nil || correlation == nil || correlation.State != "superseded" || !correlation.Suppressed {
		t.Fatalf("exact supersession = %+v, err=%v", correlation, err)
	}
}

func taskGateRequestContextJSON(t *testing.T, project, session, namespaceID, taskID string, schema int) string {
	t.Helper()
	request := operatorauth.GateRequestContext{
		SchemaVersion: schema, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Gate: "gate/release", Thread: "gate/release",
		Namespace: operatorauth.NamespaceBinding{
			ProjectDir: project, Profile: team.DefaultProfile, Session: session, NamespaceID: namespaceID, Generation: "none",
		},
		GateKind: "tag", Action: "tag", Target: "tag v2.22.0", TaskID: taskID,
	}
	context, err := json.Marshal(map[string]any{"authorization_request": request})
	if err != nil {
		t.Fatal(err)
	}
	return string(context)
}
