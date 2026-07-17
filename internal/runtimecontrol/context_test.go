package runtimecontrol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetectHostContextTmuxUnderITermControlMode(t *testing.T) {
	ctx := DetectHostContext([]string{
		"TERM_PROGRAM=iTerm.app",
		"TMUX=/private/tmp/tmux-501/default,123,0",
	}, true)
	if ctx.SchemaVersion != HostContextSchemaVersion || ctx.Backend != BackendTmux || ctx.HostProgram != "iterm2" || !ctx.InsideTmux || !ctx.ControlMode || ctx.Remote || ctx.Tier != TierA {
		t.Fatalf("context = %+v", ctx)
	}
	for _, name := range []string{string(CapabilitySendPrompt), string(CapabilityCapture), string(CapabilityBusyDetect), string(CapabilityLocalInput)} {
		if state := ctx.Capabilities[name]; state.State != SupportSupported || !state.Available {
			t.Fatalf("tmux raw capability %s = %+v", name, state)
		}
	}
	if ctx.LegacyTmux.State != "compatibility_alias" || ctx.LegacyTmux.Authoritative != "records[].terminal" || ctx.LegacyTmux.Removal != "not_before_v3" {
		t.Fatalf("legacy policy = %+v", ctx.LegacyTmux)
	}
}

func TestDetectHostContextNativeITermKeepsDurableOperationsSeparate(t *testing.T) {
	ctx := DetectHostContext([]string{"TERM_PROGRAM=iTerm.app"}, false)
	if ctx.Backend != BackendITerm2 || ctx.HostProgram != "iterm2" || ctx.InsideTmux || ctx.ControlMode || ctx.Tier != TierB {
		t.Fatalf("context = %+v", ctx)
	}
	if state := ctx.Capabilities[string(CapabilitySendPrompt)]; state.State != SupportUnsupported || state.ReasonCode == "" || state.Reason != ITerm2InjectionDisabledReason {
		t.Fatalf("raw send capability = %+v", state)
	}
	if state := ctx.Capabilities[string(CapabilityLocalInput)]; state.State != SupportUnsupported || state.ReasonCode == "" || state.Reason == "" {
		t.Fatalf("local-input capability must not look like no blocker: %+v", state)
	}
	for _, name := range []string{string(CapabilityGoalDeliver), string(CapabilityDispatch)} {
		if _, ok := ctx.Operations[name]; ok {
			t.Fatalf("effective member action %s leaked into session topology operations", name)
		}
	}
	if state := ctx.Operations[OperationLaunchNewWindow]; state.State != SupportSupported {
		t.Fatalf("native new-window operation = %+v", state)
	}
}

func TestDetectHostContextRemoteNativeGUIFailsClosedWithoutLeakingEndpoint(t *testing.T) {
	const secretEndpoint = "user@secret.example 10.0.0.1 22"
	ctx := DetectHostContext([]string{"TERM_PROGRAM=Apple_Terminal", "SSH_CONNECTION=" + secretEndpoint}, false)
	if ctx.Backend != BackendTerminalApp || !ctx.Remote || ctx.Tier != TierC {
		t.Fatalf("context = %+v", ctx)
	}
	if state := ctx.Operations[OperationLaunchNewWindow]; state.State != SupportUnsupported || state.ReasonCode == "" {
		t.Fatalf("remote GUI launch = %+v", state)
	}
	for _, evidence := range ctx.Evidence {
		if evidence.Value == secretEndpoint {
			t.Fatalf("bounded evidence leaked SSH endpoint: %+v", evidence)
		}
	}
	if _, ok := ctx.Operations[string(CapabilityDispatch)]; ok {
		t.Fatalf("effective dispatch leaked into remote session topology operations")
	}
}

func TestDetectHostContextUnknownIsExplicit(t *testing.T) {
	ctx := DetectHostContext(nil, false)
	if ctx.Backend != "unknown" || ctx.HostProgram != "unknown" || ctx.Tier != TierUnsupported {
		t.Fatalf("context = %+v", ctx)
	}
	if state := ctx.Capabilities[string(CapabilityLocalInput)]; state.State != SupportUnknown || state.ReasonCode != "host_terminal_unknown" || state.Reason == "" {
		t.Fatalf("unknown local-input state = %+v", state)
	}
	if _, ok := ctx.Operations[string(CapabilityGoalDeliver)]; ok {
		t.Fatalf("effective goal delivery leaked into unknown session topology operations")
	}
}

func TestDetectHostContextBoundsUnknownTermProgramEvidence(t *testing.T) {
	secret := "SecretTerminal\n" + strings.Repeat("sensitive-value-", 1024)
	ctx := DetectHostContext([]string{"TERM_PROGRAM=" + secret}, false)
	if ctx.HostProgram != "other" || ctx.Backend != "unknown" || ctx.Tier != TierUnsupported {
		t.Fatalf("bounded unknown context = %+v", ctx)
	}
	b, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SecretTerminal") || strings.Contains(string(b), "sensitive-value") {
		t.Fatalf("serialized context leaked TERM_PROGRAM value: %s", b)
	}
	if !strings.Contains(string(b), `"host_program":"other"`) || !strings.Contains(string(b), `"value":"other"`) {
		t.Fatalf("serialized context missing bounded sentinel: %s", b)
	}
}
