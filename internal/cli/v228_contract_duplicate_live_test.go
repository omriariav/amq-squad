package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// AC4: two genuinely live matching launch records must produce an explicit
// duplicate_live outcome. Neither is silently elected the winner — the whole
// point of the v2.28 conflict semantics is that ambiguity is reported, not
// resolved by scan order.
//
// v228StateDuplicateLive is a string literal on purpose: the state does not
// exist on main, so this file must not depend on a constant that P2 adds.
const v228StateDuplicateLive = "duplicate_live"

func TestV228ContractTwoLiveMatchingRecordsReportDuplicateLive(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac4"
		handle  = "dev"
		pidA    = 4401
		pidB    = 4402
	)
	v228SeedProfile(t, project, profile, session, []team.Member{
		{Role: handle, Binary: "codex", Handle: handle, Session: session},
	})

	canonicalRoot := v228CanonicalRoot(project, profile, session)
	// A second root inside the same project base root, from the legacy
	// session-only layout that ScanEntries still reads. Both records are live and
	// match the same member identity.
	legacyRoot := filepath.Join(project, ".agent-mail", session)
	record := func(root string, pid int) launch.Record {
		return launch.Record{
			Binary: "codex", Role: handle, Handle: handle, Session: session,
			TeamProfile: profile, TeamHome: project, CWD: project,
			Root: root, BaseRoot: filepath.Dir(root),
			AgentPID: pid, StartedAt: v228Now.Add(-time.Hour),
		}
	}
	dirA := v228SeedLiveRecord(t, canonicalRoot, record(canonicalRoot, pidA))
	dirB := v228SeedLiveRecord(t, legacyRoot, record(legacyRoot, pidB))

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       project,
		Profile:          profile,
		RequestedSession: session,
		ExplicitSession:  true,
		JSON:             true,
		Probe:            v228Probe(pidA, pidB),
	})
	if err != nil {
		t.Fatalf("executeStatus: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if len(env.Data.Records) != 1 {
		t.Fatalf("status records = %+v, want one row for the one configured member", env.Data.Records)
	}
	row := env.Data.Records[0]
	if string(row.Status) != v228StateDuplicateLive && row.RecordState != v228StateDuplicateLive {
		t.Fatalf("two live matching records reported status=%q record_state=%q, want %s in one of them (detail: %s)",
			row.Status, row.RecordState, v228StateDuplicateLive, row.Detail)
	}
	if row.Status == statusStateLive {
		t.Errorf("one of two live records was silently elected: status=%q root=%q pid=%d", row.Status, row.Root, row.Signals.AgentPID)
	}
	// The operator must be able to act, so the conflict names both records.
	for _, want := range []string{launch.ExistingPath(dirA), launch.ExistingPath(dirB)} {
		if !strings.Contains(row.Detail, want) {
			t.Errorf("duplicate_live detail %q does not name %q", row.Detail, want)
		}
	}
}

func TestV228ContractOneLiveOneDeadRecordSelectsTheLiveOne(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac4"
		handle  = "dev"
		livePID = 4403
		deadPID = 4404
	)
	v228SeedProfile(t, project, profile, session, []team.Member{
		{Role: handle, Binary: "codex", Handle: handle, Session: session},
	})
	canonicalRoot := v228CanonicalRoot(project, profile, session)
	legacyRoot := filepath.Join(project, ".agent-mail", session)
	record := func(root string, pid int) launch.Record {
		return launch.Record{
			Binary: "codex", Role: handle, Handle: handle, Session: session,
			TeamProfile: profile, TeamHome: project, CWD: project,
			Root: root, BaseRoot: filepath.Dir(root),
			AgentPID: pid, StartedAt: v228Now.Add(-time.Hour),
		}
	}
	v228SeedLiveRecord(t, canonicalRoot, record(canonicalRoot, livePID))
	v228SeedLiveRecord(t, legacyRoot, record(legacyRoot, deadPID))

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       project,
		Profile:          profile,
		RequestedSession: session,
		ExplicitSession:  true,
		JSON:             true,
		Probe:            v228Probe(livePID),
	})
	if err != nil {
		t.Fatalf("executeStatus: %v\n%s", err, out)
	}
	row := decodeJSONEnvelope[statusEnvelopeData](t, out).Data.Records[0]
	// Exactly one live record is not a conflict: select it, at its canonical root.
	if row.Status != statusStateLive {
		t.Errorf("single live record status = %q (%s), want live", row.Status, row.Detail)
	}
	if row.Root != canonicalRoot || row.Signals.AgentPID != livePID {
		t.Errorf("selected record = root %q pid %d, want %q / %d", row.Root, row.Signals.AgentPID, canonicalRoot, livePID)
	}
}
