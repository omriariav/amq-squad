package mergetrain

// Brief 320-train-escape-legacy: the reviewed diff read with escape bytes
// (rows E1 to E4), legacy gate records skipped by the drift reader (L1 to
// L3) and the store alerts already on disk (L4). The revert of each row is
// in docs/v3-spike/320-train-escape-legacy/note.md.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

const (
	escapeFlag = "--allow-escape-sequences"
	// gh 2.100.0's words for a response it will not print, and an older
	// gh's for a flag it does not know
	ghEscapeRefusal = "the response contains terminal escape sequences; pass --allow-escape-sequences to output it anyway"
	ghUnknownFlag   = "unknown flag: --allow-escape-sequences"
	diffAccept      = "Accept: application/vnd.github.diff"
	// a captured run log as a terminal wrote it: colour codes around words
	capturedLog = "\x1b[32mok\x1b[0m  \tpkg/a\t0.2s\n\x1b[31mFAIL\x1b[0m\tpkg/b\t0.1s\n"
)

// diffReads are the fake's logged compare diff reads, in order.
func diffReads(f *fixture) []string {
	var out []string
	for _, line := range strings.Split(f.calls(), "\n") {
		if strings.Contains(line, diffAccept) {
			out = append(out, line)
		}
	}
	return out
}

// E1, the fixture's own refusal: the fake answers a compare diff holding
// escape bytes as gh 2.100.0 does (its words, exit 1, no bytes) unless the
// flag is passed, and as an older gh does under OldGH (the flag unknown, the
// bytes printed without it).
func TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed(t *testing.T) {
	f := newFixture(t)
	f.branch("esc", "main", map[string]string{"evidence/run.log": capturedLog})
	read := func(head string, more ...string) (string, string, error) {
		args := append([]string{"api", "repos/" + fixtureRepo + "/compare/" + f.head("main") + "..." + f.head(head), "-H", diffAccept}, more...)
		out, stderr, err := f.gh.runner(context.Background(), args)
		return string(out), stderr, err
	}
	if out, stderr, err := read("esc"); err == nil || err.Error() != "exit status 1" || out != "" || stderr != ghEscapeRefusal+"\n" {
		t.Fatalf("escape bytes without the flag were not refused in gh's words: out=%q stderr=%q err=%v", out, stderr, err)
	}
	if out, stderr, err := read("esc", escapeFlag); err != nil || stderr != "" || !strings.Contains(out, "+\x1b[31mFAIL\x1b[0m\tpkg/b") {
		t.Fatalf("escape bytes with the flag were not printed: out=%q stderr=%q err=%v", out, stderr, err)
	}
	if out, _, err := read("pr1"); err != nil || !strings.Contains(out, "+func A() {}") {
		t.Fatalf("a diff without escape bytes needs no flag: out=%q err=%v", out, err)
	}
	f.edit(func(s *fakeState) { s.OldGH = true })
	if out, stderr, err := read("esc", escapeFlag); err == nil || err.Error() != "exit status 1" || out != "" || stderr != ghUnknownFlag+"\n" {
		t.Fatalf("an older gh knows no such flag: out=%q stderr=%q err=%v", out, stderr, err)
	}
	if out, stderr, err := read("esc"); err != nil || stderr != "" || !strings.Contains(out, "+\x1b[31mFAIL\x1b[0m\tpkg/b") {
		t.Fatalf("an older gh prints escape bytes unrefused: out=%q stderr=%q err=%v", out, stderr, err)
	}
}

// escapeBehind is a strict fixture whose PR 6 adds a captured log holding
// escape bytes, bound to its task, with the base moved on (BEHIND).
func escapeBehind(t *testing.T) (f *fixture, task, old string, req Request) {
	t.Helper()
	f = newFixture(t)
	setStrict(f)
	f.branch("pr6", "main", map[string]string{"evidence/run.log": capturedLog})
	f.edit(func(s *fakeState) { s.PRs["6"] = &fakePR{Head: "pr6", Base: "main", Draft: true, State: "OPEN"} })
	old = f.head("pr6")
	task = f.bindTask(6, old, []string{"evidence/run.log"})
	req = Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 6}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	return f, task, old, req
}

// rebasedAndMerged runs the train over escapeBehind's request and asserts
// the PR was rebased, proved and merged: equal, non-empty patch ids, the
// rebind entry, the task bound to the new head. A run that stopped at the
// proof says so with the PR as it was left.
func rebasedAndMerged(t *testing.T, f *fixture, task, old string, req Request) PlanPR {
	t.Helper()
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	p := res.PRs[0]
	entries, _ := f.train.Audit.Read()
	bound, _ := f.tasks().Get(task)
	branch, _ := gitOut(f.bare, "rev-parse", "refs/heads/pr6")
	if p.Status != "merged" {
		var read []string
		for _, e := range entries {
			if e.Kind == "reconcile_read_failed" || e.Kind == "refusal" || e.Kind == "stop" {
				read = append(read, e.Kind+": "+e.Detail)
			}
		}
		t.Fatalf("#6 was not rebased and merged: status %s; force pushes recorded %d; head unchanged %v; task bound to its old head %v; audit: %s",
			p.Status, len(loadFake(f.state).PRs["6"].ForcePushes), branch == old, bound.PRHead == old, strings.Join(read, " | "))
	}
	if p.Head == old || len(loadFake(f.state).PRs["6"].ForcePushes) != 1 {
		t.Fatalf("#6 was merged without the one update: head %s, was %s, force pushes %+v", p.Head, old, loadFake(f.state).PRs["6"].ForcePushes)
	}
	if p.PatchProof == nil || !p.PatchProof.Match || p.PatchProof.OldID == "" || p.PatchProof.OldID != p.PatchProof.NewID {
		t.Fatalf("the patch proof: %+v", p.PatchProof)
	}
	rebinds := 0
	for _, e := range entries {
		if e.Kind == "rebind" && e.PR == 6 && e.Task == task && e.Head == old && e.NewHead == p.Head && strings.HasPrefix(e.Detail, "patch-id: "+p.PatchProof.OldID+"\n") {
			rebinds++
		}
	}
	if rebinds != 1 || bound.PRHead != p.Head {
		t.Fatalf("the rebind: %d entries, task bound to %s, merged head %s", rebinds, bound.PRHead, p.Head)
	}
	return p
}

// E2 (the defect, measured by the lead on gh 2.100.0 at #948): a BEHIND PR
// whose reviewed diff adds a file holding terminal escape bytes stopped at
// "prove the reviewed patch before updating" on gh's refusal to print the
// diff. The diff read passes --allow-escape-sequences; the bytes go only to
// git patch-id. Reverted (the flag dropped), the run stops at the proof.
func TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged(t *testing.T) {
	f, task, old, req := escapeBehind(t)
	rebasedAndMerged(t, f, task, old, req)
	reads := diffReads(f)
	proofs := strings.Count(f.calls(), "missing_patch")
	if proofs < 2 || len(reads) != proofs {
		t.Fatalf("one diff read per proof: %d proofs, reads %q", proofs, reads)
	}
	for _, r := range reads {
		if !strings.HasSuffix(r, " "+escapeFlag) {
			t.Fatalf("a diff read without the flag: %q", r)
		}
	}
	// that one call only: no other gh call line carries the flag
	for _, line := range strings.Split(f.calls(), "\n") {
		if strings.Contains(line, escapeFlag) && !strings.Contains(line, diffAccept) {
			t.Fatalf("another gh call carries the flag: %q", line)
		}
	}
}

// E3, the older gh: it answers the flag with "unknown flag" and prints
// escape bytes unrefused without it, so each proof reads the diff twice
// (with the flag, then once more without) and the PR is rebased and merged
// as on a current gh. Reverted (the retry removed), the run stops at the
// proof on the unknown flag.
func TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt(t *testing.T) {
	f, task, old, req := escapeBehind(t)
	f.edit(func(s *fakeState) { s.OldGH = true })
	rebasedAndMerged(t, f, task, old, req)
	reads := diffReads(f)
	proofs := strings.Count(f.calls(), "missing_patch")
	if proofs < 2 || len(reads) != 2*proofs {
		t.Fatalf("two diff reads per proof: %d proofs, reads %q", proofs, reads)
	}
	for i, r := range reads {
		if with := strings.HasSuffix(r, " "+escapeFlag); with != (i%2 == 0) {
			t.Fatalf("read %d of %q: the flag first, then once without it", i+1, reads)
		}
	}
}

// E4, the retry's bound: only the unknown flag is answered by a second
// read. A diff read that fails otherwise (the fixture's HTTP 502 form, a
// diff too large) is read once and handled as before the flag; a second
// read that fails returns its own error, never the unknown flag's.
func TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead(t *testing.T) {
	exit1 := errors.New("exit status 1")
	for _, tc := range []struct {
		name          string
		first, second string // gh's stderr for the read with the flag, and without it
		reads         int
		stderr        string // the returned GHError's
		incomplete    bool
	}{
		{name: "HTTP 502", first: "gh: Bad Gateway (HTTP 502)", reads: 1, stderr: "gh: Bad Gateway (HTTP 502)"},
		{name: "a diff too large", first: "gh: Sorry, the diff exceeded the maximum number of lines (HTTP 406)", reads: 1, incomplete: true},
		{name: "gh's own refusal of the bytes", first: ghEscapeRefusal, reads: 1, stderr: ghEscapeRefusal},
		{name: "an unknown flag, then HTTP 502", first: ghUnknownFlag, second: "gh: Bad Gateway (HTTP 502)", reads: 2, stderr: "gh: Bad Gateway (HTTP 502)"},
		{name: "an unknown flag, then a diff too large", first: ghUnknownFlag, second: "gh: Sorry, the diff exceeded the maximum number of lines (HTTP 406)", reads: 2, incomplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			fake := f.gh.runner
			var reads [][]string
			f.gh.runner = func(ctx context.Context, args []string) ([]byte, string, error) {
				if !slices.Contains(args, diffAccept) {
					return fake(ctx, args)
				}
				reads = append(reads, args)
				if slices.Contains(args, escapeFlag) {
					return nil, tc.first + "\n", exit1
				}
				return nil, tc.second + "\n", exit1
			}
			_, err := f.gh.PatchID(context.Background(), fixtureRepo, f.head("main"), f.head("pr1"))
			if len(reads) != tc.reads || !slices.Contains(reads[0], escapeFlag) || (tc.reads == 2 && slices.Contains(reads[1], escapeFlag)) {
				t.Fatalf("diff reads: want %d (the first with the flag, a second without), got %d: %q (error %v)", tc.reads, len(reads), reads, err)
			}
			var ge *GHError
			switch {
			case tc.incomplete:
				if !errors.Is(err, ErrIncompleteDiff) {
					t.Fatalf("a diff too large is an incomplete diff: %v", err)
				}
			case !errors.As(err, &ge) || strings.TrimSpace(ge.Stderr) != tc.stderr:
				t.Fatalf("the error returned: want gh's %q, got %v", tc.stderr, err)
			}
		})
	}
}

// legacyClearIn writes a 3.0.x seat clear's owner gate into a session's
// store: the v31 session's three records' shape with fixture values (schema
// amq-squad-gate/1, kind quarantine_clear, raised by the owner, the clear's
// target). The kind was added by a724b3db and left the catalog in fdfa28ff,
// so the record no longer validates.
func legacyClearIn(t *testing.T, sessionRoot, nonce string) (id, path string) {
	t.Helper()
	if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	id = "clear-" + nonce
	rec, err := gatestore.Render(gatestore.Spec{GateID: id, Action: "tag", Target: gatetarget.Command("git tag", "v0"), RaisedBy: "owner", Squad: gatestore.SquadID(sessionRoot)}, time.Now(), gateIdentity())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rec)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["schema"] != "amq-squad-gate/1" || m["raised_by"] != "owner" {
		t.Fatalf("the fixture record's shape: %s", raw)
	}
	m["class"], m["gate_kind"], m["typed_class"] = "quarantine_clear", "quarantine_clear", "clear"
	m["target"] = "clear scribe nonce=" + nonce + " incarnation=t2@1 config=sha256:" + strings.Repeat("0", 64)
	raw, _ = json.MarshalIndent(m, "", "  ")
	path = filepath.Join(sessionRoot, "extensions", "amq-squad", "gates", id+".json")
	writeFile(t, path, string(raw))
	return id, path
}

// gateRecordIn writes a gate record's fields, altered by edit, into a
// session's store under file name id.
func gateRecordIn(t *testing.T, sessionRoot, id string, edit func(map[string]any)) string {
	t.Helper()
	if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	rec, err := gatestore.Render(gatestore.Spec{GateID: id, Action: "tag", Target: gatetarget.Command("git tag", "v0"), RaisedBy: "lead", Squad: gatestore.SquadID(sessionRoot)}, time.Now(), gateIdentity())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rec)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	edit(m)
	raw, _ = json.MarshalIndent(m, "", "  ")
	path := filepath.Join(sessionRoot, "extensions", "amq-squad", "gates", id+".json")
	writeFile(t, path, string(raw))
	return path
}

const (
	legacyNonce = "0a227ca8d88dc97c65c540a4d0fa18a7"
	legacyWords = "gate clear-" + legacyNonce + " is a legacy quarantine_clear gate (a kind this build no longer carries); skipped"
)

// driftSquad is a squad session and a release session under one AMQ root,
// the squad's drift baseline written.
func driftSquad(t *testing.T, repo string, l ReleaseLister, now time.Time) (base, squad, release string) {
	t.Helper()
	base = t.TempDir()
	squad, release = filepath.Join(base, "v31"), filepath.Join(base, "v320")
	for _, d := range []string{squad, release} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(a) != 0 {
		t.Fatalf("baseline: %v %+v", err, a)
	}
	return base, squad, release
}

// L1 (the defect, measured by the lead in the v31 session): a gate record
// of a kind the catalog no longer carries fails validation, and the drift
// reader raised a gate_store_unreadable alert for it on every run. It is
// skipped with the one line the other readers print (brief 31-1's rule), and
// the release gate beside it still clears its tag. Reverted (the skip
// removed), the alert is raised.
func TestDriftSkipsALegacyGateRecordWithOneLine(t *testing.T) {
	const repo = "owner/app"
	l := &listed{}
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	_, squad, release := driftSquad(t, repo, l, now)
	sha := strings.Repeat("4", 40)
	l.tags = []Tag{{Name: "v3.2.0", SHA: sha}}
	gateIn(t, release, "rel-tag", "tag", gatetarget.ReleaseTag(repo, "v3.2.0", sha), true)
	id, _ := legacyClearIn(t, release, legacyNonce)
	for i := 1; i <= 2; i++ { // every run, not only the first
		run, err := DriftNoted(context.Background(), l, decisionAccepted, squad, repo, now)
		if err != nil || len(run.Alerts) != 0 || len(ReadAlerts(squad)) != 0 {
			t.Fatalf("run %d: a legacy record beside an accepted release gate raised an alert: %v %+v, on disk %+v", i, err, run.Alerts, ReadAlerts(squad))
		}
		if !slices.Equal(run.Notes, []string{legacyWords}) || legacyWords != gatestore.LegacyLine(id, "quarantine_clear") {
			t.Fatalf("run %d: the legacy line, once, naming the kind and the gate id: %q", i, run.Notes)
		}
	}
	// the old signatures raise nothing for it either (the doctor's check
	// reads through Drift)
	if a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(a) != 0 {
		t.Fatalf("Drift: %v %+v", err, a)
	}
	if a, resolved, err := DriftResolved(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(a) != 0 || len(resolved) != 0 {
		t.Fatalf("DriftResolved: %v %+v %+v", err, a, resolved)
	}
}

// L2: a record that is truly malformed still alerts. Torn JSON is
// TestDriftNamesAGateRecordThatDoesNotLoad; here a known kind with a bad
// target, and a legacy kind whose gate_id is not its file's name (the store
// cannot say whose record it is). Reverted (every load error skipped), no
// alert is raised.
func TestDriftStillNamesAMalformedGateRecord(t *testing.T) {
	const repo = "owner/app"
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	for name, write := range map[string]func(t *testing.T, release string) string{
		"a known kind with a bad target": func(t *testing.T, release string) string {
			return gateRecordIn(t, release, "rel-bad-target", func(m map[string]any) { m["target"] = "git tag v0\nand a second line" })
		},
		"a legacy kind under another gate id": func(t *testing.T, release string) string {
			return gateRecordIn(t, release, "clear-"+legacyNonce, func(m map[string]any) {
				m["class"], m["gate_kind"], m["typed_class"] = "quarantine_clear", "quarantine_clear", "clear"
				m["gate_id"] = "clear-" + strings.Repeat("f", 32)
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := &listed{}
			_, squad, release := driftSquad(t, repo, l, now)
			bad := write(t, release)
			run, err := DriftNoted(context.Background(), l, decisionAccepted, squad, repo, now)
			if err != nil || len(run.Alerts) != 1 || run.Alerts[0].Kind != "gate_store_unreadable" || run.Alerts[0].Name != bad || len(ReadAlerts(squad)) != 1 {
				t.Fatalf("a malformed record raised no alert naming it: %v %+v, on disk %+v", err, run.Alerts, ReadAlerts(squad))
			}
			if len(run.Notes) != 0 {
				t.Fatalf("a malformed record was noted as legacy: %q", run.Notes)
			}
		})
	}
}

// legacyTrain is a fixture wired for a dry run that reads drift, its drift
// baseline written by a first run, whose squad session's own gate store then
// gets the legacy record (the v31 session's case). before is a run's result
// while the store holds no such record: no note, and no notes field in its
// JSON.
func legacyTrain(t *testing.T) (f *fixture, root, record string, before Result) {
	t.Helper()
	f = newFixture(t)
	root = filepath.Join(f.dir, "root")
	f.train.ReleaseGates, f.train.Root, f.train.DryRun, f.train.VerifyGate = &f.store, root, true, decisionAccepted
	f.req.PRs = f.req.PRs[:1]
	for run := 1; run <= 2; run++ { // the first writes the baseline, the second reads the stores
		res, err := f.train.Run(context.Background(), f.req)
		if err != nil || len(res.Alerts) != 0 || res.Notes != nil {
			t.Fatalf("run %d with no legacy record: %v %+v %q", run, err, res.Alerts, res.Notes)
		}
		if raw, _ := json.Marshal(res); strings.Contains(string(raw), `"notes"`) {
			t.Fatalf("a run with no legacy record encodes a notes field: %s", raw)
		}
		before = res
	}
	_, record = legacyClearIn(t, root, legacyNonce)
	return f, root, record, before
}

// LegacyDryRuns is for this package's external tests, which print a result
// through internal/cli (this package cannot import it): a dry run's result
// while the root's gate store holds no legacy record, the next run's over
// the same root holding one, and that record's line.
func LegacyDryRuns(t *testing.T) (before, after Result, line string) {
	t.Helper()
	f, _, _, before := legacyTrain(t)
	after, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	return before, after, legacyWords
}

// entriesOf are the audit entries of one kind.
func entriesOf(t *testing.T, f *fixture, kind string) []Entry {
	t.Helper()
	all, err := f.train.Audit.Read()
	if err != nil {
		t.Fatal(err)
	}
	var out []Entry
	for _, e := range all {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// L3: the line reaches the train's result and its audit log (its printed
// form is TestTrainDryRunPrintsASkippedLegacyGateRecordOnce, through
// internal/cli). A dry run over a root holding the legacy record raises no
// alert, carries the one line in Notes and journals one "note" entry; the
// next run says it again and writes no alert file: it is a note of the run,
// never a stored alert. Reverted (the line dropped), Notes is empty.
func TestTrainNotesASkippedLegacyGateRecord(t *testing.T) {
	f, root, _, _ := legacyTrain(t)
	for run := 1; run <= 2; run++ {
		res, err := f.train.Run(context.Background(), f.req)
		if err != nil || len(res.Alerts) != 0 || len(ReadAlerts(root)) != 0 {
			t.Fatalf("run %d: %v, alerts %+v, on disk %+v", run, err, res.Alerts, ReadAlerts(root))
		}
		if !slices.Equal(res.Notes, []string{legacyWords}) {
			t.Fatalf("run %d: the result's notes: %q", run, res.Notes)
		}
		if raw, _ := json.Marshal(res); strings.Count(string(raw), `"notes":["`+legacyWords+`"]`) != 1 {
			t.Fatalf("run %d: the result's JSON: %s", run, raw)
		}
		notes := entriesOf(t, f, "note")
		if len(notes) != run || notes[run-1].Detail != legacyWords || notes[run-1].Run != res.Run {
			t.Fatalf("run %d: one note entry a run: %+v", run, notes)
		}
		if n := len(entriesOf(t, f, "alert")); n != 0 {
			t.Fatalf("run %d: %d alert entries", run, n)
		}
	}
}

// plantStoreAlert writes the gate_store_unreadable alert an earlier build
// left for path: the id and the words drift.go gives it.
func plantStoreAlert(t *testing.T, root, repo, path string) Alert {
	t.Helper()
	sum := sha256.Sum256([]byte(path))
	a := Alert{ID: "gate-store-unreadable-" + hex.EncodeToString(sum[:6]), Repository: repo, Kind: "gate_store_unreadable", Name: path, At: "2026-11-05T17:20:00Z",
		Detail: "a gate store of this squad could not be read, so no gate in it clears an alert; fix or remove the record named below the line\n\n" + gatestore.Rule + "\nthe gate store " + path + " could not be read (gate record is malformed)"}
	if err := WriteAlert(root, a); err != nil {
		t.Fatal(err)
	}
	return a
}

// L4 (Q1, measured on the base: nothing ever removed a
// gate-store-unreadable alert, also after its record was fixed or removed).
// The plan reviewer's bound: the removal condition is positive. An on-disk
// store alert goes only when this run loaded the record at its named path,
// skipped it as legacy, or listed the gates directory it names; a path this
// run did not reach leaves the alert where it is, and so does one this run
// raises again. Reverted (no removal), the first three forms keep their
// alert; reverted (removal when the path was not flagged this run), the
// not-reached forms lose theirs.
func TestDriftClearsAStoreAlertOnlyForAPathThisRunRead(t *testing.T) {
	const repo = "owner/app"
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	gates := func(session string) string { return filepath.Join(session, "extensions", "amq-squad", "gates") }
	for _, tc := range []struct {
		name string
		// plant prepares the stores and returns the path the planted alert names
		plant  func(t *testing.T, base, release string) string
		why    string // the words of its clearing; "" when it must stay
		raised int    // alerts this run raises
		again  bool   // the alert it raises is the planted one
	}{
		{name: "a legacy record this run skipped", why: "the record is a legacy kind and is skipped", plant: func(t *testing.T, base, release string) string {
			_, path := legacyClearIn(t, release, legacyNonce)
			return path
		}},
		{name: "a record this run loaded", why: "the record now loads", plant: func(t *testing.T, base, release string) string {
			gateIn(t, release, "rel-fixed", "tag", gatetarget.ReleaseTag(repo, "v1", strings.Repeat("4", 40)), true)
			return filepath.Join(gates(release), "rel-fixed.json")
		}},
		{name: "a gates directory this run listed", why: "the gate store can be listed again", plant: func(t *testing.T, base, release string) string {
			gateIn(t, release, "rel-ok", "tag", gatetarget.ReleaseTag(repo, "v1", strings.Repeat("4", 40)), true)
			return gates(release)
		}},
		{name: "the record absent", plant: func(t *testing.T, base, release string) string {
			gateIn(t, release, "rel-ok", "tag", gatetarget.ReleaseTag(repo, "v1", strings.Repeat("4", 40)), true)
			return filepath.Join(gates(release), "rel-gone.json")
		}},
		{name: "its session directory missing", plant: func(t *testing.T, base, release string) string {
			return filepath.Join(gates(filepath.Join(base, "v300")), "clear-"+legacyNonce+".json")
		}},
		{name: "the parent of the sessions unlistable", raised: 1, plant: func(t *testing.T, base, release string) string {
			if os.Geteuid() == 0 {
				t.Skip("root lists any directory")
			}
			_, path := legacyClearIn(t, release, legacyNonce)
			// searchable, not listable: the squad's own files are still read
			if err := os.Chmod(base, 0o300); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(base, 0o700) })
			return path
		}},
		{name: "a gates directory this run did not list", plant: func(t *testing.T, base, release string) string {
			return gates(release) // the release session holds no gate store
		}},
		{name: "an alert this run raises again", raised: 1, again: true, plant: func(t *testing.T, base, release string) string {
			path := filepath.Join(gates(release), "rel-torn.json")
			writeFile(t, path, `{"gate_id": "rel-torn", "class": `)
			return path
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &listed{}
			base, squad, release := driftSquad(t, repo, l, now)
			planted := plantStoreAlert(t, squad, repo, tc.plant(t, base, release))
			onDisk := func() bool {
				return slices.ContainsFunc(ReadAlerts(squad), func(a Alert) bool { return a.ID == planted.ID && a.Name == planted.Name })
			}
			run, err := DriftNoted(context.Background(), l, decisionAccepted, squad, repo, now)
			if err != nil || len(run.Alerts) != tc.raised {
				t.Fatalf("the run: %v, alerts %+v", err, run.Alerts)
			}
			if tc.why == "" {
				left := 1 + tc.raised
				if tc.again {
					left = 1
					if run.Alerts[0].ID != planted.ID || run.Alerts[0].Name != planted.Name {
						t.Fatalf("the alert raised again is not the planted one: %+v", run.Alerts)
					}
				}
				if len(run.Resolved) != 0 || !onDisk() || len(ReadAlerts(squad)) != left {
					t.Fatalf("an alert this run had no cause to clear (its path not read, or flagged again) was removed: resolved %+v, on disk %+v", run.Resolved, ReadAlerts(squad))
				}
				return
			}
			if len(run.Resolved) != 1 || run.Resolved[0].Alert != planted || run.Resolved[0].Why != tc.why || onDisk() || len(ReadAlerts(squad)) != 0 {
				t.Fatalf("the alert was not cleared in its own words (%q): resolved %+v, on disk %+v", tc.why, run.Resolved, ReadAlerts(squad))
			}
			// the old signature returns it as resolved too, and a second run
			// has nothing left to clear
			if a, resolved, err := DriftResolved(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(a) != 0 || len(resolved) != 0 {
				t.Fatalf("the next run: %v %+v %+v", err, a, resolved)
			}
		})
	}
}

// L4 through the train: the clearing is journaled once, as alert_cleared
// with words of its own for each cause, and an ungated alert's clearing
// keeps the words it had ("a verified owner gate now covers it").
func TestTrainRecordsAStoreAlertsClearingInItsOwnWords(t *testing.T) {
	f, root, record, _ := legacyTrain(t)
	gateIn(t, root, "rel-fixed", "tag", gatetarget.Command("git tag", "v0"), false)
	legacy := plantStoreAlert(t, root, fixtureRepo, record)
	loads := plantStoreAlert(t, root, fixtureRepo, filepath.Join(root, "extensions", "amq-squad", "gates", "rel-fixed.json"))
	mustGit(t, f.bare, "tag", "v1", "main")
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil || len(res.Alerts) != 1 || res.Alerts[0].Kind != "tag" {
		t.Fatalf("the run that reads both records: %v %+v", err, res.Alerts)
	}
	ungated := res.Alerts[0]
	gateIn(t, root, "rel-v1", "tag", gatetarget.ReleaseTag(fixtureRepo, "v1", f.head("main")), true)
	for run := 2; run <= 3; run++ { // the second covers the tag; the third has nothing to clear
		if res, err := f.train.Run(context.Background(), f.req); err != nil || len(res.Alerts) != 0 || len(ReadAlerts(root)) != 0 {
			t.Fatalf("run %d: %v %+v, on disk %+v", run, err, res.Alerts, ReadAlerts(root))
		}
	}
	got := map[string][]string{}
	for _, e := range entriesOf(t, f, "alert_cleared") {
		got[e.GateID] = append(got[e.GateID], e.Detail)
	}
	for _, want := range []struct {
		a   Alert
		why string
	}{
		{legacy, "the record is a legacy kind and is skipped"},
		{loads, "the record now loads"},
		{ungated, "a verified owner gate now covers it"},
	} {
		if !slices.Equal(got[want.a.ID], []string{want.a.Detail + "; " + want.why}) {
			t.Fatalf("the clearing of %s, once, in its own words (%q): %q", want.a.ID, want.why, got[want.a.ID])
		}
	}
	if len(got) != 3 {
		t.Fatalf("clearings: %+v", got)
	}
}
