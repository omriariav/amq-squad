package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestOperatorStatusJSONReportsPollContractAndAttention(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "fyi-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "note/operator",
		Subject: "FYI: note",
		Kind:    string(state.KindStatus),
		Created: notifyNow,
	})
	seedNotifyMessage(t, base, "s", "cto", "new", notifyMsg{
		ID:      "directive-1",
		From:    team.DefaultOperatorHandle,
		To:      "cto",
		Thread:  "p2p/cto__user",
		Subject: "DIRECTIVE: ship it",
		Kind:    string(state.KindTodo),
		Created: notifyNow,
	})

	var out bytes.Buffer
	err := executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		Now: func() time.Time { return notifyNow },
	})
	if err != nil {
		t.Fatalf("operator status: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Kind != "operator_status" {
		t.Fatalf("kind = %q, want operator_status", env.Kind)
	}
	data := env.Data
	if !data.ReadOnly {
		t.Fatalf("readonly = false, want true for operator status")
	}
	if data.Operator.Handle != team.DefaultOperatorHandle || data.Operator.CanonicalInbox == nil {
		t.Fatalf("operator = %+v, want canonical user inbox", data.Operator)
	}
	if data.Operator.CanonicalInbox.Session != "s" || data.Operator.CanonicalInbox.Root == "" {
		t.Fatalf("canonical inbox = %+v, want session s with root", data.Operator.CanonicalInbox)
	}
	if data.OperatorLoop.Mode != "poll" || data.OperatorLoop.State != "poll_required_unowned" || data.OperatorLoop.Owner != "none" {
		t.Fatalf("operator loop = %+v, want unowned poll loop", data.OperatorLoop)
	}
	if data.OperatorLoop.Backlog != 2 || data.OperatorLoop.GatesOpen != 1 || data.OperatorLoop.DirectivesUnacked != 1 {
		t.Fatalf("operator loop counts = %+v, want backlog=2, gates_open=1, directives_unacked=1", data.OperatorLoop)
	}
	if data.Operator.Poll == nil || data.Operator.Poll.Unread != 2 || data.Operator.Poll.OpenGates != 1 || data.Operator.Poll.OpenBlockers != 0 {
		t.Fatalf("operator poll = %+v, want unread/open gate counts without directive duplicate", data.Operator.Poll)
	}
	if len(data.Attention) != 1 || data.Attention[0].Thread != "gate/release" {
		t.Fatalf("attention = %+v, want gate/release", data.Attention)
	}
}

func TestOperatorStatusDisabledProfileIsUnconfigured(t *testing.T) {
	project, _, _ := seedNotifyProject(t, team.DisabledOperator())

	var out bytes.Buffer
	err := executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   "",
		JSON:       true,
		Out:        &out,
	})
	if err != nil {
		t.Fatalf("operator status disabled: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Data.OperatorGates {
		t.Fatalf("operator gates = true, want false")
	}
	if env.Data.OperatorLoop.Mode != "disabled" || env.Data.OperatorLoop.State != "unconfigured" {
		t.Fatalf("operator loop = %+v, want disabled/unconfigured", env.Data.OperatorLoop)
	}
	if !strings.Contains(env.Data.Message, "disabled") {
		t.Fatalf("message = %q, want disabled guidance", env.Data.Message)
	}
}

func TestOperatorPollReadOnlyJSONUsesOperatorLoopContract(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "gate-1",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})

	var out bytes.Buffer
	err := executeOperatorPoll(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		ReadOnly:   true,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		Now: func() time.Time { return notifyNow },
	})
	if err != nil {
		t.Fatalf("operator poll readonly: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Kind != "operator_poll" {
		t.Fatalf("kind = %q, want operator_poll", env.Kind)
	}
	if !env.Data.ReadOnly {
		t.Fatalf("readonly = false, want true")
	}
	if env.Data.OperatorLoop.Backlog != 1 || env.Data.OperatorLoop.GatesOpen != 1 || env.Data.OperatorLoop.Owner != "none" {
		t.Fatalf("operator loop = %+v, want read-only unowned poll counts", env.Data.OperatorLoop)
	}
}

func TestOperatorPollClaimsLeaseAndCursorHighWater(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "cur", notifyMsg{
		ID:      "2026-06-28T22-00-02.000Z_pid1_newer",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "p2p/cto__user",
		Subject: "read high-water",
		Kind:    string(state.KindStatus),
		Created: notifyNow.Add(2 * time.Minute),
	})
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "2026-06-28T22-00-01.000Z_pid1_older",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow.Add(time.Minute),
	})

	var out bytes.Buffer
	err := executeOperatorPoll(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		Owner:      "noc",
		OwnerID:    "noc:host:1",
		LeaseTTL:   5 * time.Minute,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return notifyNow },
		},
		Now: func() time.Time { return notifyNow },
	})
	if err != nil {
		t.Fatalf("operator poll lease claim: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Kind != "operator_poll" || env.Data.ReadOnly {
		t.Fatalf("poll envelope kind/readonly = %q/%v, want operator_poll/false", env.Kind, env.Data.ReadOnly)
	}
	loop := env.Data.OperatorLoop
	if loop.State != "poller_active" || loop.Owner != "noc" || loop.OwnerID != "noc:host:1" || loop.Cursor != "2026-06-28T22-00-02.000Z_pid1_newer" {
		t.Fatalf("operator loop = %+v, want active noc lease with inbox high-water cursor", loop)
	}
	if loop.LeaseExpiresAt != notifyNow.Add(5*time.Minute).UTC().Format(time.RFC3339) || loop.LastPollAt != notifyNow.UTC().Format(time.RFC3339) {
		t.Fatalf("lease timestamps = %+v, want RFC3339 UTC from fixed clock", loop)
	}
	leasePath := operatorLoopLeasePath(project, team.DefaultProfile, "s")
	if leasePath != filepath.Join(project, team.DirName, "operator-loop", "s.json") {
		t.Fatalf("default lease path = %q, want profile omitted", leasePath)
	}
	if _, err := os.Stat(filepath.Join(project, team.DirName, "operator-loop", team.DefaultProfile, "s.json")); !os.IsNotExist(err) {
		t.Fatalf("default-profile lease must not be written under literal default dir")
	}
	lease, err := readOperatorLoopLease(leasePath)
	if err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if lease.OwnerID != "noc:host:1" || lease.Cursor != "2026-06-28T22-00-02.000Z_pid1_newer" {
		t.Fatalf("lease = %+v, want claimed high-water lease", lease)
	}
}

func TestOperatorStatusReportsExistingLeaseReadOnly(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "daemon",
		OwnerID:        "daemon:host:7",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: now.Add(time.Minute).UTC(),
		LastPollAt:     now.Add(-time.Minute).UTC(),
		Cursor:         "m9",
		UpdatedAt:      now.Add(-time.Minute).UTC(),
	})
	if err != nil {
		t.Fatalf("write lease: %v", err)
	}

	var out bytes.Buffer
	err = executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return now },
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("operator status lease read: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	loop := env.Data.OperatorLoop
	if !env.Data.ReadOnly || loop.State != "poller_active" || loop.Owner != "daemon" || loop.OwnerID != "daemon:host:7" || loop.Cursor != "m9" {
		t.Fatalf("status loop = %+v readonly=%v, want read-only active lease", loop, env.Data.ReadOnly)
	}
}

func TestOperatorStatusReportsExpiredLeaseStale(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "daemon",
		OwnerID:        "daemon:host:7",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: now.Add(-time.Minute).UTC(),
		LastPollAt:     now.Add(-3 * time.Minute).UTC(),
		Cursor:         "m9",
		UpdatedAt:      now.Add(-3 * time.Minute).UTC(),
	})
	if err != nil {
		t.Fatalf("write lease: %v", err)
	}

	var out bytes.Buffer
	err = executeOperatorStatus(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return now },
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("operator status expired lease read: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Data.OperatorLoop.State != "poller_stale" || env.Data.OperatorLoop.OwnerID != "daemon:host:7" {
		t.Fatalf("status loop = %+v, want stale daemon lease", env.Data.OperatorLoop)
	}
}

func TestOperatorPollRefusesActiveForeignLease(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	if err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "noc",
		OwnerID:        "noc:host:1",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: now.Add(time.Minute).UTC(),
		LastPollAt:     now.Add(-time.Minute).UTC(),
		UpdatedAt:      now.Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	err := executeOperatorPoll(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		Owner:      "cli",
		OwnerID:    "cli:host:2",
		LeaseTTL:   2 * time.Minute,
		Out:        &bytes.Buffer{},
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return now },
		},
		Now: func() time.Time { return now },
	})
	if err == nil || !strings.Contains(err.Error(), "already held by noc:host:1") {
		t.Fatalf("foreign active lease error = %v, want deterministic conflict", err)
	}
	lease, readErr := readOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"))
	if readErr != nil {
		t.Fatalf("read lease: %v", readErr)
	}
	if lease.OwnerID != "noc:host:1" {
		t.Fatalf("lease owner after conflict = %q, want unchanged noc:host:1", lease.OwnerID)
	}
}

func TestOperatorPollJSONConflictEmitsHolderAndRuntimeError(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	if err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "noc",
		OwnerID:        "noc:host:1",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: now.Add(time.Minute).UTC(),
		LastPollAt:     now.Add(-time.Minute).UTC(),
		Cursor:         "m9",
		UpdatedAt:      now.Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	var out bytes.Buffer
	err := executeOperatorPoll(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		Owner:      "cli",
		OwnerID:    "cli:host:2",
		LeaseTTL:   2 * time.Minute,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return now },
		},
		Now: func() time.Time { return now },
	})
	if err == nil || ExitCode(err) != ExitSystem {
		t.Fatalf("conflict err = %v exit=%d, want runtime/system conflict", err, ExitCode(err))
	}
	if _, ok := err.(UsageError); ok {
		t.Fatalf("conflict err must not be UsageError: %T %v", err, err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Kind != "operator_poll" || env.Data.Claimed == nil || *env.Data.Claimed {
		t.Fatalf("poll conflict kind/claimed = %q/%v, want operator_poll claimed=false", env.Kind, env.Data.Claimed)
	}
	if env.Data.OperatorLoop.State != "poller_active" || env.Data.OperatorLoop.OwnerID != "noc:host:1" {
		t.Fatalf("operator loop = %+v, want current holder", env.Data.OperatorLoop)
	}
	if env.Data.Conflict == nil || env.Data.Conflict.Code != "lease_conflict" || env.Data.Conflict.OwnerID != "noc:host:1" || env.Data.Conflict.LeaseExpiresAt == "" || env.Data.Conflict.Cursor != "m9" {
		t.Fatalf("conflict = %+v, want structured current holder", env.Data.Conflict)
	}
}

func TestOperatorWatchOnceClaimsLeaseCompactJSON(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", team.DefaultOperatorHandle, "new", notifyMsg{
		ID:      "2026-06-28T22-00-01.000Z_pid1_msg",
		From:    "cto",
		To:      team.DefaultOperatorHandle,
		Thread:  "gate/release",
		Subject: "APPROVAL: release",
		Kind:    string(state.KindQuestion),
		Created: notifyNow,
	})

	var out bytes.Buffer
	err := executeOperatorWatch(operatorWatchExecution{
		operatorExecution: operatorExecution{
			ProjectDir: project,
			Profile:    team.DefaultProfile,
			Session:    "s",
			BaseRoot:   base,
			Owner:      "noc",
			OwnerID:    "noc:host:1",
			LeaseTTL:   2 * time.Minute,
			JSON:       true,
			Out:        &out,
			Probe: state.Probe{
				PIDAlive:     func(pid int) bool { return true },
				ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
				Now:          func() time.Time { return notifyNow },
			},
			Now: func() time.Time { return notifyNow },
		},
		Interval: 5 * time.Second,
		Once:     true,
		Sleep: func(time.Duration) bool {
			t.Fatal("watch --once must not sleep")
			return false
		},
	})
	if err != nil {
		t.Fatalf("operator watch once: %v", err)
	}
	raw := out.String()
	if strings.Count(raw, "\n") != 1 || strings.Contains(raw, "\n  ") {
		t.Fatalf("watch JSON must be one compact NDJSON line, got:\n%s", raw)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, raw)
	if env.Kind != "operator_watch" {
		t.Fatalf("kind = %q, want operator_watch", env.Kind)
	}
	if env.Data.Watch == nil || env.Data.Watch.Tick != 1 || env.Data.Watch.Interval != "5s" || !env.Data.Watch.At.Equal(notifyNow.UTC()) {
		t.Fatalf("watch metadata = %+v, want tick=1 interval=5s at fixed clock", env.Data.Watch)
	}
	if env.Data.Claimed == nil || !*env.Data.Claimed || env.Data.OperatorLoop.OwnerID != "noc:host:1" {
		t.Fatalf("watch claimed/loop = %v/%+v, want claimed noc lease", env.Data.Claimed, env.Data.OperatorLoop)
	}
}

func TestOperatorWatchOnceConflictReturnsTypedError(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	if err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "noc",
		OwnerID:        "noc:host:1",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: now.Add(time.Minute).UTC(),
		LastPollAt:     now.Add(-time.Minute).UTC(),
		Cursor:         "m9",
		UpdatedAt:      now.Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	var out bytes.Buffer
	err := executeOperatorWatch(operatorWatchExecution{
		operatorExecution: operatorExecution{
			ProjectDir: project,
			Profile:    team.DefaultProfile,
			Session:    "s",
			BaseRoot:   base,
			Owner:      "cli",
			OwnerID:    "cli:host:2",
			LeaseTTL:   2 * time.Minute,
			JSON:       true,
			Out:        &out,
			Probe: state.Probe{
				PIDAlive:     func(pid int) bool { return true },
				ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
				Now:          func() time.Time { return now },
			},
			Now: func() time.Time { return now },
		},
		Interval: 5 * time.Second,
		Once:     true,
	})
	if err == nil || ExitCode(err) != ExitSystem {
		t.Fatalf("watch once conflict err = %v exit=%d, want runtime conflict", err, ExitCode(err))
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Kind != "operator_watch" || env.Data.Claimed == nil || *env.Data.Claimed || env.Data.Conflict == nil || env.Data.Conflict.Code != "lease_conflict" {
		t.Fatalf("watch conflict envelope = kind %q claimed %v conflict %+v", env.Kind, env.Data.Claimed, env.Data.Conflict)
	}
	if env.Data.Watch == nil || env.Data.Watch.Tick != 1 {
		t.Fatalf("watch metadata = %+v, want tick=1", env.Data.Watch)
	}
}

func TestOperatorWatchContinuesAfterConflictAndReclaimsExpiredLease(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	current := notifyNow
	if err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "noc",
		OwnerID:        "noc:host:1",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: current.Add(time.Second).UTC(),
		LastPollAt:     current.Add(-time.Minute).UTC(),
		UpdatedAt:      current.Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("write lease: %v", err)
	}
	sleepCalls := 0
	var out bytes.Buffer
	err := executeOperatorWatch(operatorWatchExecution{
		operatorExecution: operatorExecution{
			ProjectDir: project,
			Profile:    team.DefaultProfile,
			Session:    "s",
			BaseRoot:   base,
			Owner:      "cli",
			OwnerID:    "cli:host:2",
			LeaseTTL:   4 * time.Second,
			JSON:       true,
			Out:        &out,
			Probe: state.Probe{
				PIDAlive:     func(pid int) bool { return true },
				ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
				Now:          func() time.Time { return current },
			},
			Now: func() time.Time { return current },
		},
		Interval: 2 * time.Second,
		Sleep: func(d time.Duration) bool {
			sleepCalls++
			current = current.Add(d)
			return sleepCalls < 2
		},
	})
	if err != nil {
		t.Fatalf("watch long-running conflict/reclaim: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("watch lines = %d, want 2:\n%s", len(lines), out.String())
	}
	first := decodeJSONEnvelope[operatorStatusEnvelopeData](t, lines[0])
	second := decodeJSONEnvelope[operatorStatusEnvelopeData](t, lines[1])
	if first.Data.Claimed == nil || *first.Data.Claimed || first.Data.Conflict == nil {
		t.Fatalf("first tick = claimed %v conflict %+v, want conflict", first.Data.Claimed, first.Data.Conflict)
	}
	if second.Data.Claimed == nil || !*second.Data.Claimed || second.Data.Conflict != nil || second.Data.OperatorLoop.OwnerID != "cli:host:2" {
		t.Fatalf("second tick = claimed %v conflict %+v loop %+v, want reclaimed lease", second.Data.Claimed, second.Data.Conflict, second.Data.OperatorLoop)
	}
	if first.Data.Watch.Tick != 1 || second.Data.Watch.Tick != 2 {
		t.Fatalf("watch ticks = %d/%d, want 1/2", first.Data.Watch.Tick, second.Data.Watch.Tick)
	}
}

func TestRunOperatorWatchRejectsIntervalTooCloseToTTL(t *testing.T) {
	chdir(t, t.TempDir())
	_, _, err := captureOutput(t, func() error {
		return runOperator([]string{"watch", "--session", "s", "--interval", "2m", "--ttl", "2m"})
	})
	if err == nil || !strings.Contains(err.Error(), "--interval must be <= --ttl/2") {
		t.Fatalf("operator watch interval/ttl error = %v, want guard", err)
	}
}

func TestOperatorPollClaimsExpiredForeignLease(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	if err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "noc",
		OwnerID:        "noc:host:1",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: now.Add(-time.Minute).UTC(),
		LastPollAt:     now.Add(-3 * time.Minute).UTC(),
		UpdatedAt:      now.Add(-3 * time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	var out bytes.Buffer
	err := executeOperatorPoll(operatorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
		Session:    "s",
		BaseRoot:   base,
		Owner:      "cli",
		OwnerID:    "cli:host:2",
		LeaseTTL:   2 * time.Minute,
		JSON:       true,
		Out:        &out,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return now },
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("claim expired lease: %v", err)
	}
	env := decodeJSONEnvelope[operatorStatusEnvelopeData](t, out.String())
	if env.Data.OperatorLoop.State != "poller_active" || env.Data.OperatorLoop.OwnerID != "cli:host:2" {
		t.Fatalf("operator loop = %+v, want expired lease reclaimed by cli:host:2", env.Data.OperatorLoop)
	}
}

func TestOperatorPollForceStealsLiveLeaseWritesAudit(t *testing.T) {
	project, base, _ := seedNotifyProject(t, team.DefaultOperator())
	seedNotifyLaunch(t, project, base, "s", "cto")
	now := notifyNow
	if err := writeOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"), operatorLoopLeaseFile{
		SchemaVersion:  1,
		Profile:        team.DefaultProfile,
		Session:        "s",
		NamespaceID:    "default/s",
		Mode:           "poll",
		Owner:          "noc",
		OwnerID:        "noc:host:1",
		LeaseTTL:       "2m0s",
		LeaseExpiresAt: now.Add(time.Minute).UTC(),
		LastPollAt:     now.Add(-time.Minute).UTC(),
		UpdatedAt:      now.Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	err := executeOperatorPoll(operatorExecution{
		ProjectDir:  project,
		Profile:     team.DefaultProfile,
		Session:     "s",
		BaseRoot:    base,
		Owner:       "cli",
		OwnerID:     "cli:host:2",
		LeaseTTL:    2 * time.Minute,
		Force:       true,
		ForceReason: "recover stuck poller",
		Out:         &bytes.Buffer{},
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return now },
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("force steal lease: %v", err)
	}
	lease, readErr := readOperatorLoopLease(operatorLoopLeasePath(project, team.DefaultProfile, "s"))
	if readErr != nil {
		t.Fatalf("read lease: %v", readErr)
	}
	if lease.OwnerID != "cli:host:2" {
		t.Fatalf("lease owner after force = %q, want cli:host:2", lease.OwnerID)
	}
	auditPath := filepath.Join(project, team.DirName, "operator-loop-audit", "s.jsonl")
	b, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{"recover stuck poller", "noc:host:1", "cli:host:2"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("audit missing %q:\n%s", want, string(b))
		}
	}
}

func TestRunOperatorPollForceRequiresReason(t *testing.T) {
	chdir(t, t.TempDir())
	_, _, err := captureOutput(t, func() error {
		return runOperator([]string{"poll", "--session", "s", "--force", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "--force requires --reason") {
		t.Fatalf("operator poll --force without reason error = %v, want reason usage", err)
	}
}
