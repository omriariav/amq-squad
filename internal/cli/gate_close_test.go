package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestSendGateCloseUsesDurableReplyCommandAndReceipt(t *testing.T) {
	for _, jsonOut := range []bool{false, true} {
		name := "plain"
		if jsonOut {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			root := filepath.Join(project, ".agent-mail", "s")
			var captured amqCommandRequest
			withGateCloseAMQCommandSeam(t, root, func(req amqCommandRequest) ([]byte, error) {
				captured = req
				return []byte("Replied terminal-message to request-2\n"), nil
			})
			o := gateCloseSendOptions{
				Project: project, Profile: team.DefaultProfile, Session: "s",
				From: "cto", To: "user", Thread: "gate/release", ReplyTo: "request-2",
				Subject: "CLOSED: release", Body: "checks passed", JSON: jsonOut,
				Context: map[string]any{"gate": map[string]any{
					"state": "closed", "request_message_id": "request-2", "requester": "cto", "thread": "gate/release", "actor": "cto",
				}},
			}
			stdout, _, err := captureOutput(t, func() error { return sendGateClose(o) })
			if err != nil {
				t.Fatal(err)
			}
			wantArgs := []string{
				"reply", "--root", root, "--me", "cto", "--id", "request-2",
				"--kind", "status", "--subject", "CLOSED: release", "--body", "checks passed",
				"--context", `{"gate":{"actor":"cto","request_message_id":"request-2","requester":"cto","state":"closed","thread":"gate/release"}}`,
			}
			if !reflect.DeepEqual(captured.Arg, wantArgs) {
				t.Fatalf("gate close argv = %#v\nwant %#v", captured.Arg, wantArgs)
			}
			if captured.Dir != project || !envHas(captured.Env, "AM_ROOT", root) || !envHas(captured.Env, "AM_ME", "cto") {
				t.Fatalf("gate close command context = dir:%q env:%v", captured.Dir, captured.Env)
			}
			if containsString(captured.Arg, "--reply-to") || containsString(captured.Arg, "send") {
				t.Fatalf("gate close used unsupported send/reply-to argv: %v", captured.Arg)
			}

			paths, globErr := filepath.Glob(filepath.Join(deliveryReceiptDir(project, team.DefaultProfile, "s"), "*.json"))
			if globErr != nil || len(paths) != 1 {
				t.Fatalf("receipt paths=%v err=%v", paths, globErr)
			}
			receipt, readErr := readDeliveryReceipt(paths[0])
			if readErr != nil {
				t.Fatal(readErr)
			}
			if receipt.MessageID != "terminal-message" || receipt.Recipient != "user" || !reflect.DeepEqual(receipt.Recipients, []string{"user"}) || receipt.Sender != "cto" || receipt.Thread != "gate/release" || receipt.Root != root || receipt.DeliveryState != deliveryStateDeliveredNotDrained {
				t.Fatalf("gate close durable receipt = %+v", receipt)
			}
			if jsonOut {
				for _, want := range []string{`"kind": "gate_close"`, `"message_id": "terminal-message"`, `"recipient": "user"`, `"thread": "gate/release"`} {
					if !strings.Contains(stdout, want) {
						t.Fatalf("JSON output missing %s:\n%s", want, stdout)
					}
				}
			} else {
				for _, want := range []string{"Sent gate close on gate/release: terminal-message", "attempt ", "state delivered_not_drained", "receipt "} {
					if !strings.Contains(stdout, want) {
						t.Fatalf("plain output missing %q:\n%s", want, stdout)
					}
				}
			}
		})
	}
}

func TestSendGateCloseReplyErrorIncludesDurableReceipt(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "s")
	withGateCloseAMQCommandSeam(t, root, func(amqCommandRequest) ([]byte, error) {
		return []byte("reply failed\n"), errors.New("exit status 2")
	})
	err := sendGateClose(gateCloseSendOptions{
		Project: project, Profile: team.DefaultProfile, Session: "s",
		From: "cto", To: "user", Thread: "gate/release", ReplyTo: "request-2",
		Subject: "CLOSED: release", Body: "checks failed",
		Context: map[string]any{"gate": map[string]any{
			"state": "closed", "request_message_id": "request-2", "requester": "cto", "thread": "gate/release", "actor": "cto",
		}},
	})
	if err == nil {
		t.Fatal("sendGateClose should surface reply failure")
	}
	for _, want := range []string{"gate close send to user", "exit status 2", "attempt_id=", "state=ambiguous_unknown", "receipt="} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("reply error missing %q: %v", want, err)
		}
	}
	paths, globErr := filepath.Glob(filepath.Join(deliveryReceiptDir(project, team.DefaultProfile, "s"), "*.json"))
	if globErr != nil || len(paths) != 1 {
		t.Fatalf("receipt paths=%v err=%v", paths, globErr)
	}
	receipt, readErr := readDeliveryReceipt(paths[0])
	if readErr != nil || receipt.Recipient != "user" || receipt.Thread != "gate/release" || !receipt.AMQInvoked || receipt.DeliveryState != deliveryStateAmbiguousUnknown {
		t.Fatalf("failed reply receipt=%+v readErr=%v", receipt, readErr)
	}
}

func TestRunGateCloseBindsCurrentRequestGeneration(t *testing.T) {
	for _, withdrawn := range []bool{false, true} {
		name := "closed"
		wantState := state.OperatorGateStateClosed
		if withdrawn {
			name = "withdrawn"
			wantState = state.OperatorGateStateWithdrawn
		}
		t.Run(name, func(t *testing.T) {
			project, _, _ := seedNotifyProject(t, team.DefaultOperator())
			question := state.Message{
				ID: "request-2", From: "cto", To: []string{"user"}, Thread: "gate/release",
				RawThread: "gate/release", SchemaOK: true,
				Kind: state.KindQuestion, Subject: "APPROVAL: release?", Created: notifyNow.Add(-time.Minute),
			}
			question = exactGateRequestRecipient(question, "user")
			var sent gateCloseSendOptions
			withGateCloseFakes(t, []state.Message{question}, nil, func(o gateCloseSendOptions) error {
				sent = o
				return nil
			})

			args := []string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "no longer needed", "--json"}
			if withdrawn {
				args = append(args, "--withdrawn")
			}
			if err := runGateClose(args); err != nil {
				t.Fatalf("runGateClose: %v", err)
			}
			if sent.From != "cto" || sent.To != "user" || sent.Thread != "gate/release" || sent.ReplyTo != "request-2" || sent.Body != "no longer needed" || !sent.JSON {
				t.Fatalf("send options = %+v", sent)
			}
			gate, ok := sent.Context["gate"].(map[string]any)
			if !ok || gate["state"] != string(wantState) || gate["request_message_id"] != "request-2" || gate["requester"] != "cto" || gate["thread"] != "gate/release" || gate["actor"] != "cto" {
				t.Fatalf("terminal context = %#v", sent.Context)
			}
		})
	}
}

func TestRunGateCloseRefusesDegradedOrUnauthorizedMutation(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	question := state.Message{
		ID: "request", From: "cto", To: []string{"user"}, Thread: "gate/release",
		RawThread: "gate/release", SchemaOK: true,
		Kind: state.KindQuestion, Subject: "APPROVAL: release?", Created: notifyNow.Add(-time.Minute),
	}
	question = exactGateRequestRecipient(question, "user")

	t.Run("degraded scan", func(t *testing.T) {
		sent := false
		withGateCloseFakes(t, []state.Message{question}, []state.Warning{{Path: "bad", Reason: "torn"}}, func(gateCloseSendOptions) error {
			sent = true
			return nil
		})
		err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
		if err == nil || !strings.Contains(err.Error(), "scan is degraded") || sent {
			t.Fatalf("err=%v sent=%t", err, sent)
		}
	})

	t.Run("different requester", func(t *testing.T) {
		sent := false
		withGateCloseFakes(t, []state.Message{question}, nil, func(gateCloseSendOptions) error {
			sent = true
			return nil
		})
		err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "qa", "--reason", "done"})
		if err == nil || sent {
			t.Fatalf("err=%v sent=%t", err, sent)
		}
	})

	t.Run("identity verification", func(t *testing.T) {
		original := resolveVerifiedOperatorActor
		resolveVerifiedOperatorActor = func(string, string, string, string, string) (verifiedOperatorActor, error) {
			return verifiedOperatorActor{}, errors.New("unverified")
		}
		t.Cleanup(func() { resolveVerifiedOperatorActor = original })
		err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
		if err == nil || !strings.Contains(err.Error(), "not verified") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestRunGateCloseUsesReraisedRequestID(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	old := state.Message{ID: "old", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/release", SchemaOK: true, Kind: state.KindQuestion, Subject: "APPROVAL: old?", Created: notifyNow.Add(-time.Hour)}
	newRequest := state.Message{ID: "new", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/release", SchemaOK: true, Kind: state.KindQuestion, Subject: "APPROVAL: new?", Created: notifyNow.Add(-time.Minute)}
	old = exactGateRequestRecipient(old, "user")
	newRequest = exactGateRequestRecipient(newRequest, "user")
	var sent gateCloseSendOptions
	withGateCloseFakes(t, []state.Message{newRequest, old}, nil, func(o gateCloseSendOptions) error { sent = o; return nil })

	if err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"}); err != nil {
		t.Fatal(err)
	}
	gate := sent.Context["gate"].(map[string]any)
	if gate["request_message_id"] != "new" {
		t.Fatalf("bound request = %#v, want new", gate)
	}
}

func TestRunGateCloseRefusesConflictedCurrentRequest(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	copyA := state.Message{ID: "request", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/release", SchemaOK: true, Kind: state.KindQuestion, Subject: "APPROVAL: A?", Created: notifyNow.Add(-time.Minute)}
	copyA = exactGateRequestRecipient(copyA, "user")
	copyB := copyA
	copyB.Subject = "APPROVAL: B?"
	sent := false
	withGateCloseFakes(t, []state.Message{copyA, copyB}, nil, func(gateCloseSendOptions) error { sent = true; return nil })

	err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
	if err == nil || !strings.Contains(err.Error(), "conflicting mailbox evidence") || sent {
		t.Fatalf("err=%v sent=%t", err, sent)
	}
}

func TestRunGateCloseRefusesRepairedRequestThread(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	request := state.Message{ID: "request", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate//release", SchemaOK: true, Kind: state.KindQuestion, Subject: "APPROVAL: release?", Created: notifyNow.Add(-time.Minute)}
	request = exactGateRequestRecipient(request, "user")
	sent := false
	withGateCloseFakes(t, []state.Message{request}, nil, func(gateCloseSendOptions) error { sent = true; return nil })

	err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
	if err == nil || !strings.Contains(err.Error(), "lacks exact schema/thread identity") || sent {
		t.Fatalf("err=%v sent=%t", err, sent)
	}
}

func TestRunGateCloseRefusesRequestThatAlreadyCarriesRefs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		refs  []string
		valid bool
	}{
		{name: "populated", refs: []string{"parent-request"}, valid: true},
		{name: "empty", refs: []string{}, valid: true},
		{name: "null", valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, _, _ := seedNotifyProject(t, team.DefaultOperator())
			request := state.Message{
				ID: "request", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/release", SchemaOK: true,
				Kind: state.KindQuestion, Subject: "APPROVAL: release?", Refs: tc.refs, RefsPresent: true, RefsValid: tc.valid, Created: notifyNow.Add(-time.Minute),
			}
			request = exactGateRequestRecipient(request, "user")
			sent := false
			withGateCloseFakes(t, []state.Message{request}, nil, func(gateCloseSendOptions) error { sent = true; return nil })

			err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
			if err == nil || !strings.Contains(err.Error(), "already carries refs") || sent {
				t.Fatalf("err=%v sent=%t", err, sent)
			}
		})
	}
}

func TestRunGateCloseRequiresCanonicalSingleOperatorRecipientWithoutSendOrReceipt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rawTo      []string
		present    bool
		arrayValid bool
		raw        string
	}{
		{name: "qa then user", rawTo: []string{"qa", "user"}, present: true, arrayValid: true, raw: `["qa","user"]`},
		{name: "user then qa", rawTo: []string{"user", "qa"}, present: true, arrayValid: true, raw: `["user","qa"]`},
		{name: "duplicate user", rawTo: []string{"user", "user"}, present: true, arrayValid: true, raw: `["user","user"]`},
		{name: "empty array", rawTo: []string{}, present: true, arrayValid: true, raw: `[]`},
		{name: "trim repaired", rawTo: []string{" user "}, present: true, arrayValid: true, raw: `[" user "]`},
		{name: "bare string", present: true, raw: `"user"`},
		{name: "missing"},
		{name: "null", present: true, raw: `null`},
		{name: "malformed array", present: true, raw: `["user",1]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, _, _ := seedNotifyProject(t, team.DefaultOperator())
			request := state.Message{
				ID: "request", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/release", SchemaOK: true,
				Kind: state.KindQuestion, Subject: "APPROVAL: release?", Created: notifyNow.Add(-time.Minute),
				RawTo: tc.rawTo, ToPresent: tc.present, ToArrayValid: tc.arrayValid, ToRaw: tc.raw,
			}
			sent := false
			withGateCloseFakes(t, []state.Message{request}, nil, func(gateCloseSendOptions) error { sent = true; return nil })
			err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
			if err == nil || !strings.Contains(err.Error(), "exactly one canonical on-disk recipient") || sent {
				t.Fatalf("err=%v sent=%t", err, sent)
			}
			if _, statErr := os.Stat(deliveryReceiptDir(project, team.DefaultProfile, "s")); !os.IsNotExist(statErr) {
				t.Fatalf("recipient refusal reserved a receipt: %v", statErr)
			}
		})
	}
}

func TestRunGateCloseRecipientOrderConflictFailsClosedBothInputOrders(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	qaUser := state.Message{
		ID: "request", From: "cto", To: []string{"qa", "user"}, RawTo: []string{"qa", "user"}, ToPresent: true, ToArrayValid: true, ToRaw: `["qa","user"]`,
		Thread: "gate/release", RawThread: "gate/release", SchemaOK: true, Kind: state.KindQuestion, Subject: "APPROVAL: release?", Created: notifyNow.Add(-time.Minute),
	}
	userQA := qaUser
	userQA.To = []string{"user", "qa"}
	userQA.RawTo = []string{"user", "qa"}
	userQA.ToRaw = `["user","qa"]`
	for i, messages := range [][]state.Message{{qaUser, userQA}, {userQA, qaUser}} {
		t.Run(fmt.Sprintf("order-%d", i), func(t *testing.T) {
			sent := false
			withGateCloseFakes(t, messages, nil, func(gateCloseSendOptions) error { sent = true; return nil })
			err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
			if err == nil || !strings.Contains(err.Error(), "conflicting mailbox evidence") || sent {
				t.Fatalf("err=%v sent=%t", err, sent)
			}
			if _, statErr := os.Stat(deliveryReceiptDir(project, team.DefaultProfile, "s")); !os.IsNotExist(statErr) {
				t.Fatalf("recipient conflict reserved a receipt: %v", statErr)
			}
		})
	}
}

func TestRunGateCloseRefusesTerminalOrMissingGateWithoutSend(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DefaultOperator())
	question := state.Message{ID: "request", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/release", SchemaOK: true, Kind: state.KindQuestion, Subject: "APPROVAL: release?", Created: notifyNow.Add(-time.Hour)}
	question = exactGateRequestRecipient(question, "user")
	answer := state.Message{ID: "answer", From: "user", To: []string{"cto"}, Thread: "gate/release", Kind: state.KindAnswer, ReplyTo: "request", Created: notifyNow.Add(-time.Minute)}
	close := state.Message{
		ID: "close", From: "cto", To: []string{"user"}, Thread: "gate/release", RawThread: "gate/release", SchemaOK: true, Kind: state.KindStatus, ReplyTo: "request", Created: notifyNow.Add(-time.Minute),
		Context: map[string]any{"gate": map[string]any{"state": "closed", "request_message_id": "request", "requester": "cto", "thread": "gate/release", "actor": "cto"}},
	}
	for _, tc := range []struct {
		name     string
		messages []state.Message
	}{
		{name: "no open request"},
		{name: "already answered", messages: []state.Message{answer, question}},
		{name: "already closed", messages: []state.Message{close, question}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := false
			withGateCloseFakes(t, tc.messages, nil, func(gateCloseSendOptions) error { sent = true; return nil })
			err := runGateClose([]string{"--project", project, "--session", "s", "--gate", "release", "--me", "cto", "--reason", "done"})
			if err == nil || sent {
				t.Fatalf("err=%v sent=%t", err, sent)
			}
		})
	}
}

func TestRunGateCloseRejectsNonCanonicalTopic(t *testing.T) {
	for _, topic := range []string{"gate//release", "gate/release/", "/release", " release", "release ", "gate/../release", `gate/release\nested`, "gate/release\u00a0candidate", "gate/release\u2028candidate"} {
		t.Run(strings.ReplaceAll(topic, "/", "_"), func(t *testing.T) {
			if _, err := strictGateCloseTopic(topic); err == nil {
				t.Fatalf("strictGateCloseTopic(%q) should fail", topic)
			}
		})
	}
}

func TestStrictGateCloseTopicAcceptsCanonicalNestedTopic(t *testing.T) {
	for _, input := range []string{"decision/v2-21-0-414-464-interface", "gate/decision/v2-21-0-414-464-interface"} {
		got, err := strictGateCloseTopic(input)
		if err != nil || got != "gate/decision/v2-21-0-414-464-interface" {
			t.Fatalf("strictGateCloseTopic(%q) = %q, %v", input, got, err)
		}
	}
}

func TestTerminalGateSuppressedFromOperatorStatusAndAgedWarnings(t *testing.T) {
	for _, terminalState := range []state.OperatorGateState{state.OperatorGateStateClosed, state.OperatorGateStateWithdrawn} {
		t.Run(string(terminalState), func(t *testing.T) {
			project, base, _ := seedNotifyProject(t, team.DefaultOperator())
			seedNotifyLaunch(t, project, base, "s", "cto")
			seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
				ID: "request", From: "cto", To: "user", Thread: "gate/release",
				Subject: "APPROVAL: release", Kind: "question", Created: notifyNow.Add(-3 * time.Hour),
			})
			seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
				ID: "terminal", From: "cto", To: "user", Thread: "gate/release",
				Subject: strings.ToUpper(string(terminalState)) + ": release", Kind: "status", ReplyTo: "request", Created: notifyNow.Add(-time.Hour),
				Context: `{"gate":{"state":"` + string(terminalState) + `","request_message_id":"request","requester":"cto","thread":"gate/release","actor":"cto"}}`,
			})

			data, err := buildOperatorStatusData(operatorExecution{
				ProjectDir: project, Profile: team.DefaultProfile, Session: "s", BaseRoot: base,
				Probe: state.Probe{Now: func() time.Time { return notifyNow }}, Now: func() time.Time { return notifyNow },
			})
			if err != nil {
				t.Fatal(err)
			}
			if data.OperatorLoop.GatesOpen != 0 || data.OperatorLoop.Backlog != 0 || len(data.Attention) != 0 {
				t.Fatalf("terminal operator status = gates:%d backlog:%d attention:%+v", data.OperatorLoop.GatesOpen, data.OperatorLoop.Backlog, data.Attention)
			}
			if warnings := statusWarningsForAgedOperatorGates(data); len(warnings) != 0 {
				t.Fatalf("terminal aged gate emitted warnings: %+v", warnings)
			}
		})
	}
}

func withGateCloseFakes(t *testing.T, messages []state.Message, warnings []state.Warning, send func(gateCloseSendOptions) error) {
	t.Helper()
	originalScan := gateCloseScanMessages
	originalSend := gateCloseSend
	originalVerify := resolveVerifiedOperatorActor
	gateCloseScanMessages = func(string, func() time.Time) ([]state.Message, []state.Warning) { return messages, warnings }
	gateCloseSend = send
	resolveVerifiedOperatorActor = func(_, profile, session, role, handle string) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: role, Handle: handle, Profile: profile, Session: session}, nil
	}
	t.Cleanup(func() {
		gateCloseScanMessages = originalScan
		gateCloseSend = originalSend
		resolveVerifiedOperatorActor = originalVerify
	})
}

func withGateCloseAMQCommandSeam(t *testing.T, root string, run amqCommandRunner) {
	t.Helper()
	originalEnv := resolveAMQEnvForAMQCommand
	originalRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(_, _, session, handle string) (amqEnv, error) {
		return amqEnv{Root: root, BaseRoot: filepath.Dir(root), SessionName: session, Me: handle}, nil
	}
	runAMQCommand = run
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = originalEnv
		runAMQCommand = originalRun
	})
}

func exactGateRequestRecipient(message state.Message, operator string) state.Message {
	message.RawTo = []string{operator}
	message.ToPresent = true
	message.ToArrayValid = true
	message.ToRaw = `["` + operator + `"]`
	return message
}
