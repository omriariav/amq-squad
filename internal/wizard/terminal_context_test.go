package wizard

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestTerminalContextRecommendsDetachedWithoutOverridingExplicitChoice(t *testing.T) {
	context := TerminalContext{
		SchemaVersion: 1,
		Backend:       "iterm2",
		HostProgram:   "iterm2",
		Tier:          "B",
		Operations: map[string]TerminalOperation{
			"launch_current_window": {State: "unsupported", Reason: "native current window unavailable"},
			"launch_new_window":     {State: "supported"},
			"launch_new_session":    {State: "unsupported"},
		},
	}
	if got := recommendedTopology("sibling-tabs", false, context); got != "detached" {
		t.Fatalf("recommended topology = %q, want detached", got)
	}
	if got := recommendedTopology("current", true, context); got != "current" {
		t.Fatalf("explicit topology = %q, want current", got)
	}
	if got := annotateTopologyChoice(context, "current", "Panes in this window"); !strings.Contains(got, "requires a visible tmux pane") || !strings.Contains(got, "recommends detached") {
		t.Fatalf("current topology warning = %q", got)
	}
	if got := annotateTopologyChoice(context, "detached", "Detached squad"); !strings.Contains(got, "recommended") {
		t.Fatalf("detached topology guidance = %q", got)
	}
	diagnostic := topologyDiagnostic(context, "current")
	for _, want := range []string{"backend=iterm2", "tier=B", "host_operations=launch_current_window=unsupported", "explicit selection retained"} {
		if !strings.Contains(diagnostic, want) {
			t.Fatalf("diagnostic missing %q: %s", want, diagnostic)
		}
	}
}

func TestTerminalContextSurfacesControlModeAtTopologyStep(t *testing.T) {
	context := TerminalContext{SchemaVersion: 1, Backend: "tmux", HostProgram: "iterm2", Tier: "A", InsideTmux: true, ControlMode: true}
	for _, visibility := range []string{"current", "sibling-tabs"} {
		got := annotateTopologyChoice(context, visibility, visibility)
		if !strings.Contains(got, "tmux -CC") || !strings.Contains(got, "stagger/retry") {
			t.Fatalf("%s guidance = %q", visibility, got)
		}
	}
	if got := annotateTopologyChoice(context, "detached", "detached"); strings.Contains(got, "tmux -CC") {
		t.Fatalf("detached guidance inherited visible control-mode caveat: %q", got)
	}
}

func TestTerminalContextDoesNotMislabelNonITermControlMode(t *testing.T) {
	context := TerminalContext{SchemaVersion: 1, Backend: "tmux", HostProgram: "vscode", Tier: "A", InsideTmux: true, ControlMode: true}
	choice := annotateTopologyChoice(context, "current", "current")
	if !strings.Contains(choice, "tmux control-mode client detected") || !strings.Contains(choice, "stagger/retry") {
		t.Fatalf("generic control-mode guidance = %q", choice)
	}
	if strings.Contains(choice, "iTerm2") || strings.Contains(choice, "-CC") {
		t.Fatalf("generic control-mode guidance mislabels host: %q", choice)
	}
	diagnostic := topologyDiagnostic(context, "current")
	if !strings.Contains(diagnostic, "tmux control-mode client detected") || strings.Contains(diagnostic, "iTerm2") {
		t.Fatalf("generic control-mode diagnostic = %q", diagnostic)
	}
}

func TestTerminalContextRenderingIsBoundedAndDoesNotRenderReasons(t *testing.T) {
	secret := "ssh://operator@example.internal/private/socket"
	context := TerminalContext{
		SchemaVersion: 1,
		Backend:       secret,
		HostProgram:   "future terminal with spaces",
		Tier:          "unsupported",
		Operations: map[string]TerminalOperation{
			"launch_new_window": {State: "unknown", ReasonCode: "host_terminal_unknown", Reason: secret},
		},
	}
	rendered := topologyDiagnostic(context, "detached")
	if strings.Contains(rendered, secret) || strings.Contains(rendered, "example.internal") {
		t.Fatalf("diagnostic leaked unbounded evidence: %s", rendered)
	}
	if !strings.Contains(rendered, "backend=other") || !strings.Contains(rendered, "host=other") {
		t.Fatalf("diagnostic did not bound unknown values: %s", rendered)
	}
}

func TestTerminalContextIsRenderedByBothWizardAdapters(t *testing.T) {
	context := TerminalContext{SchemaVersion: 1, Backend: "tmux", HostProgram: "iterm2", Tier: "A", InsideTmux: true, ControlMode: true}
	opts := NumberedOptions{Defaults: Spec{Project: "/repo"}, TerminalContext: context}

	var numbered bytes.Buffer
	_, err := RunNumbered(strings.NewReader("q\n"), &numbered, opts)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("numbered cancellation = %v", err)
	}
	if !strings.Contains(numbered.String(), "Terminal context: backend=tmux host=iterm2 tier=A") {
		t.Fatalf("numbered header omitted terminal context:\n%s", numbered.String())
	}

	bubble, err := NewBubbleModel(opts)
	if err != nil {
		t.Fatal(err)
	}
	bubbleScope := bubble.View()
	for _, want := range []string{"Terminal context:", "backend=tmux", "host=iterm2", "tier=A"} {
		if !strings.Contains(bubbleScope, want) {
			t.Fatalf("bubble scope omitted %q:\n%s", want, bubbleScope)
		}
	}
	bubble.stage = stageTopology
	bubble.configureStage()
	view := bubble.View()
	if !strings.Contains(view, "tmux -CC detected") || !strings.Contains(view, "stagger/retry") {
		t.Fatalf("bubble topology omitted control-mode warning:\n%s", view)
	}
}

func TestBubbleTopologyDefaultsDetachedOutsideTmux(t *testing.T) {
	context := TerminalContext{SchemaVersion: 1, Backend: "terminal_app", HostProgram: "terminal_app", Tier: "C"}
	bubble, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"}, TerminalContext: context})
	if err != nil {
		t.Fatal(err)
	}
	bubble.stage = stageTopology
	bubble.configureStage()
	choices := bubble.choices()
	if bubble.cursor >= len(choices) || choices[bubble.cursor].value != "detached" {
		t.Fatalf("topology default cursor=%d choices=%+v", bubble.cursor, choices)
	}

	bubble.spec.VisibilityExplicit = true
	bubble.configureStage()
	choices = bubble.choices()
	if bubble.cursor >= len(choices) || choices[bubble.cursor].value != "sibling-tabs" {
		t.Fatalf("explicit topology cursor=%d choices=%+v", bubble.cursor, choices)
	}
}
