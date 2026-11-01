package task

import (
	"reflect"
	"testing"
	"time"
)

func TestTypedWidenPathsSurviveEscalationAndApprovedRetry(t *testing.T) {
	s := newStore(t)
	tk := mustAdd(t, s, AddInput{Title: "typed", DeclaredFiles: []string{"a.go"}})
	if _, err := s.Claim(tk.ID, "seat", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.BlockWithPaths(tk.ID, "seat", ReasonWidenRequested, "", RouteLead, []string{"protected/x.go"}, t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	escalated, err := s.Escalate(tk.ID, "growth_gate_required", "owner decision", t0.Add(2*time.Second))
	if err != nil || escalated.BlockRoute != RouteOwner || !reflect.DeepEqual(escalated.RefusedPaths, blocked.RefusedPaths) {
		t.Fatalf("escalated = %+v, %v", escalated, err)
	}
	got, err := s.WidenGated(tk.ID, "lead", "owner approved", []string{"protected/x.go"}, []string{"gate-target"}, time.Minute, t0.Add(3*time.Second))
	if err != nil || got.State != StatePending || len(got.RefusedPaths) != 0 || len(got.ScopeChanges) != 1 || !reflect.DeepEqual(got.ScopeChanges[0].Gates, []string{"gate-target"}) {
		t.Fatalf("approved retry = %+v, %v", got, err)
	}
}

func TestRelabelledReportlessBlockIsNotRearmedAsATypedRequest(t *testing.T) {
	s := newStore(t)
	tk := mustAdd(t, s, AddInput{Title: "typed", DeclaredFiles: []string{"a.go"}})
	if _, err := s.Claim(tk.ID, "seat", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BlockWithPaths(tk.ID, "seat", ReasonWidenRequested, "", RouteLead, []string{"b.go"}, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Escalate(tk.ID, "outside_declared_files", "relabelled", t0.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := s.WidenGated(tk.ID, "lead", "not a typed retry", []string{"b.go"}, nil, time.Minute, t0.Add(3*time.Second))
	if err != nil || got.State != StateBlocked || got.BlockReason != "outside_declared_files" || len(got.RefusedPaths) != 1 {
		t.Fatalf("relabelled widen = %+v, %v", got, err)
	}
}
