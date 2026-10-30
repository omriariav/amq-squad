package launchintent

import (
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

// 3.0.1 t9: a required wake carries its injector into the participant; an
// injector on a disabled wake is refused here, before AMQ's grammar would
// refuse it at Prepare (execution_options_grammar.go:32-34 at v0.116.2).
func TestCompileWakeInjectorNeedsARequiredWake(t *testing.T) {
	seat := baseSeat("dev", "/w/dev")
	seat.Injector = &launchapi.InjectorOptionsV1{Mode: launchapi.InjectorRaw}
	intent, _, err := Compile(Input{Operator: OperatorFacts{Handle: "user"}, Seats: []SeatFacts{seat}, Target: baseTarget()})
	if err != nil {
		t.Fatal(err)
	}
	e := intent.Participants[1].Execution
	if e == nil || !e.RequireWake || e.Wake.Mode != launchapi.WakeEnabled || e.Wake.AuditReason != "" || e.Wake.Injector == nil || e.Wake.Injector.Mode != launchapi.InjectorRaw {
		t.Fatalf("execution %+v", e)
	}
	seat.RequireWake = false
	if _, _, err := Compile(Input{Operator: OperatorFacts{Handle: "user"}, Seats: []SeatFacts{seat}, Target: baseTarget()}); err == nil || !strings.Contains(err.Error(), `seat "dev"`) || !strings.Contains(err.Error(), "requires RequireWake") {
		t.Fatalf("an injector on a disabled wake: %v", err)
	}
}
