package gatestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

var (
	testNow   = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	testIdent = Identity{OwnerPubKey: strings.Repeat("a1", 32), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b2", 32)}
)

func testStore(t *testing.T) Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fileSender "publishes" a question by writing a marker file.
type fileSender struct{ dir string }

func (f fileSender) SignQuestion(r Record) (Question, error) {
	return Question{GateID: r.GateID, EventID: strings.Repeat("c3", 32), RawEvent: r.GateID, PostedAt: testNow.Format(time.RFC3339)}, nil
}

func (f fileSender) PublishQuestion(q Question) error {
	return os.WriteFile(filepath.Join(f.dir, "sent-"+q.GateID), []byte(q.EventID), 0o600)
}

func spec(id string) Spec {
	return Spec{GateID: id, Action: "tag", Target: gatetarget.Command("git tag", "v1"), RaisedBy: "lead"}
}

func TestRenderAndValidate(t *testing.T) {
	r, err := Render(spec("g1"), testNow, testIdent)
	if err != nil {
		t.Fatal(err)
	}
	if r.Class != "tag" || r.TypedClass != "release" || r.GateKind != "tag" || r.RaisedBy != "lead" ||
		r.Digest != gatetarget.Digest("tag", "git tag v1") || r.NotAfter != testNow.Add(30*time.Minute).Format(time.RFC3339) {
		t.Fatalf("record = %+v", r)
	}
	if !strings.Contains(r.Text, "Reply in this thread") || !strings.Contains(r.Text, "approve g1 release "+strings.TrimPrefix(r.Digest, "sha256:")[:16]) {
		t.Fatalf("text = %q", r.Text)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Record){
		"digest":   func(r *Record) { r.Digest = gatetarget.Digest("tag", "git tag v2") },
		"target":   func(r *Record) { r.Target = "git tag v2" },
		"text":     func(r *Record) { r.Text += " " },
		"class":    func(r *Record) { r.TypedClass = "send" },
		"window":   func(r *Record) { r.NotAfter = testNow.Add(48 * time.Hour).Format(time.RFC3339) },
		"owner":    func(r *Record) { r.OwnerPubKey = "x" },
		"schema":   func(r *Record) { r.Schema = "s1" },
		"raisedby": func(r *Record) { r.RaisedBy = "" },
	} {
		bad := r
		mutate(&bad)
		if bad.Validate() == nil {
			t.Errorf("%s tamper validates", name)
		}
	}
	for _, s := range []Spec{
		{GateID: "G1", Action: "tag", Target: "git tag v1", RaisedBy: "lead"},
		{GateID: "x.question", Action: "tag", Target: "git tag v1", RaisedBy: "lead"},
		{GateID: "..", Action: "tag", Target: "git tag v1", RaisedBy: "lead"},
		{GateID: "a/b", Action: "tag", Target: "git tag v1", RaisedBy: "lead"},
		{GateID: "g", Action: "default_branch_push", Target: "x", RaisedBy: "lead"},
		{GateID: "g", Action: "tag", Target: "a\nb", RaisedBy: "lead"},
		{GateID: "g", Action: "tag", Target: "git tag v1", RaisedBy: ""},
		{GateID: "g", Action: "tag", Target: "git tag v1", RaisedBy: "lead", Window: time.Second},
	} {
		if _, err := Render(s, testNow, testIdent); err == nil {
			t.Errorf("Render(%+v) accepted", s)
		}
	}
	if _, err := Render(spec("g"), testNow, Identity{}); err == nil {
		t.Error("Render without identities accepted")
	}
	if _, err := Render(spec(".g-dot"), testNow, testIdent); err != nil {
		t.Errorf("dot-led gate id refused: %v", err)
	}
}

func TestParseAnswer(t *testing.T) {
	ok := "Approve  g1 release 0123456789abcdef"
	a, err := ParseAnswer(ok)
	if err != nil || a.Verb != "approve" || a.GateID != "g1" || a.Class != "release" {
		t.Fatalf("ParseAnswer(%q) = %+v, %v", ok, a, err)
	}
	for _, bad := range []string{"approve g1 release 0123456789abcdef\n", "\rapprove g1 release 0123456789abcdef", "approve g1\nrelease 0123456789abcdef",
		"yes", "approve g1 release 0123456789abcde", "approve g1 release 0123456789abcdef now", "approve g1 merge 0123456789abcdef", "maybe g1 release 0123456789abcdef"} {
		if _, err := ParseAnswer(bad); err == nil {
			t.Errorf("ParseAnswer(%q) accepted", bad)
		}
	}
}

// Acceptance 1, the child half: raise a gate and die between the record write
// and the send.
func TestHelperRaiseCrash(t *testing.T) {
	root := os.Getenv("GATESTORE_CRASH_ROOT")
	if root == "" {
		t.Skip("helper process only")
	}
	raiseHook = func(Record) { os.Exit(3) }
	s, _ := Open(root)
	_, _, err := s.Raise(spec("g-crash"), testIdent, testNow, fileSender{root})
	t.Fatalf("raise returned instead of dying: %v", err)
}

// Acceptance 1: a process killed between record write and send leaves the
// record, sent no question, and a second raise with the same id fails O_EXCL.
func TestRaiseWritesBeforeSend(t *testing.T) {
	s := testStore(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRaiseCrash$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GATESTORE_CRASH_ROOT="+s.Root)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("child = %v\n%s", err, out)
	}
	rec, err := s.Load("g-crash")
	if err != nil || rec.Digest != gatetarget.Digest("tag", "git tag v1") {
		t.Fatalf("record after the crash = %+v, %v", rec, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "sent-g-crash")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a question was sent before the crash: %v", err)
	}
	if q, err := s.Question("g-crash"); q != nil || err != nil {
		t.Fatalf("question recorded: %+v %v", q, err)
	}
	if _, _, err := s.Raise(spec("g-crash"), testIdent, testNow, fileSender{s.Root}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second raise = %v, want O_EXCL failure", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "sent-g-crash")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the failed second raise sent a question")
	}
	// the stored gate's question can still be sent, once
	if _, err := s.PostQuestion("g-crash", fileSender{s.Root}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostQuestion("g-crash", fileSender{s.Root}); err == nil {
		t.Fatal("second question accepted")
	}
}

func answerFile(rec Record, decision, eventID string) AnswerFile {
	return AnswerFile{Schema: AnswerSchema, Scheme: "bip340", Decision: decision,
		Envelope: operatorauth.AuthorizationEnvelope{Payload: operatorauth.AuthorizationPayload{Decision: decision}},
		Evidence: Evidence{Scheme: "bip340", EventID: eventID}, Gate: rec}
}

func raised(t *testing.T, s Store, id string) Record {
	t.Helper()
	rec, _, err := s.Raise(spec(id), testIdent, testNow, fileSender{s.Root})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestCommitSingleUseAndStates(t *testing.T) {
	s := testStore(t)
	rec := raised(t, s, "g1")
	ev1, ev2 := strings.Repeat("01", 32), strings.Repeat("02", 32)
	if st := s.State(rec, testNow); st != StateOpen {
		t.Fatalf("state = %s", st)
	}
	if st := s.State(rec, testNow.Add(31*time.Minute)); st != StateExpired {
		t.Fatalf("state past not_after = %s", st)
	}
	if err := s.Commit(answerFile(rec, operatorauth.DecisionApproved, ev1)); err != nil {
		t.Fatal(err)
	}
	// acceptance is terminal: past not_after the gate stays accepted
	if st := s.State(rec, testNow.Add(48*time.Hour)); st != StateAccepted {
		t.Fatalf("state = %s", st)
	}
	if f, err := s.ReadAnswer("g1"); err != nil || f == nil || f.Evidence.EventID != ev1 {
		t.Fatalf("answer = %+v %v", f, err)
	}
	for _, f := range []AnswerFile{answerFile(rec, operatorauth.DecisionApproved, ev1), answerFile(rec, operatorauth.DecisionDenied, ev2)} {
		if err := s.Commit(f); !errors.Is(err, ErrConsumed) {
			t.Fatalf("replay = %v", err)
		}
	}
	// the event is single-use across gates too
	rec2 := raised(t, s, "g2")
	if err := s.Commit(answerFile(rec2, operatorauth.DecisionApproved, ev1)); !errors.Is(err, ErrConsumed) {
		t.Fatalf("event reuse = %v", err)
	}
	if err := s.Commit(answerFile(rec2, operatorauth.DecisionDenied, ev2)); err != nil {
		t.Fatal(err)
	}
	if st := s.State(rec2, testNow); st != StateDenied {
		t.Fatalf("denied state = %s", st)
	}
	if f, _ := s.ReadAnswer("g2"); f != nil {
		t.Fatal("a deny wrote an approval file")
	}
	if f, _ := s.ReadDenial("g2"); f == nil || f.Decision != operatorauth.DecisionDenied {
		t.Fatalf("deny record = %+v", f)
	}
	bad := answerFile(rec2, operatorauth.DecisionApproved, ev2)
	bad.Envelope.Payload.Decision = operatorauth.DecisionDenied
	if err := s.Commit(bad); err == nil || errors.Is(err, ErrConsumed) {
		t.Fatalf("mismatched decision = %v", err)
	}
}

// Review finding 4: a crash between the approval file and its marks must not
// let a later deny win. The approval file is the gate's durable decision:
// every reader reports accepted, the retry of the same answer completes the
// marks, and any other answer (a deny, another approval) is already_consumed.
// A conflict left by older code (both files present) reads as denied.
func TestReview4CrashBeforeMarksRecoversTheDecision(t *testing.T) {
	s := testStore(t)
	rec := raised(t, s, ".g-dot")
	f := answerFile(rec, operatorauth.DecisionApproved, strings.Repeat("0a", 32))
	if err := writeAtomic(s.Dir("answers", rec.GateID+".json"), mustJSON(f)); err != nil {
		t.Fatal(err)
	}
	if st := s.State(rec, testNow); st != StateAccepted {
		t.Fatalf("state after the crash = %s", st)
	}
	deny := answerFile(rec, operatorauth.DecisionDenied, strings.Repeat("0b", 32))
	if err := s.Commit(deny); !errors.Is(err, ErrConsumed) {
		t.Fatalf("deny after the crash = %v", err)
	}
	if d, _ := s.ReadDenial(rec.GateID); d != nil {
		t.Fatal("a deny record was written beside the recovered approval")
	}
	if _, err := os.Stat(s.Dir("consumed", "gates", rec.GateID)); err != nil {
		t.Fatalf("recovery did not complete the gate mark: %v", err)
	}
	if err := s.Commit(f); !errors.Is(err, ErrConsumed) {
		t.Fatalf("a replay after recovery = %v", err)
	}
	if st := s.State(rec, testNow); st != StateAccepted {
		t.Fatalf("state = %s", st)
	}
	// an interrupted commit retried by the same answer succeeds once
	rec2 := raised(t, s, "g-retry")
	f2 := answerFile(rec2, operatorauth.DecisionApproved, strings.Repeat("0c", 32))
	if err := writeAtomic(s.Dir("answers", rec2.GateID+".json"), mustJSON(f2)); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(f2); err != nil {
		t.Fatalf("retry of the interrupted commit = %v", err)
	}
	// both files present (older code): the deny wins, fail closed
	rec3 := raised(t, s, "g-both")
	_ = writeAtomic(s.Dir("answers", rec3.GateID+".json"), mustJSON(answerFile(rec3, operatorauth.DecisionApproved, strings.Repeat("0d", 32))))
	_ = writeAtomic(s.Dir("denials", rec3.GateID+".json"), mustJSON(answerFile(rec3, operatorauth.DecisionDenied, strings.Repeat("0e", 32))))
	if st := s.State(rec3, testNow); st != StateDenied {
		t.Fatalf("both files = %s", st)
	}
	ids, err := s.IDs()
	if err != nil || len(ids) != 3 || ids[0] != ".g-dot" {
		t.Fatalf("IDs = %v %v", ids, err)
	}
}

// Review finding 7: two handlers reserve the re-ask at once; exactly one
// record is created, both get the same event, and a later call returns it for
// a retry of the publication.
func TestReview7ReaskReservedOnceBeforePublication(t *testing.T) {
	s := testStore(t)
	raised(t, s, "g1")
	var built int32
	var mu sync.Mutex
	build := func(n int) func() (Reask, error) {
		return func() (Reask, error) {
			mu.Lock()
			built++
			mu.Unlock()
			return Reask{GateID: "g1", TriggerEventID: strings.Repeat("11", 32), EventID: fmt.Sprintf("%02x%s", n, strings.Repeat("22", 31)), RawEvent: "{}", At: testNow.Format(time.RFC3339)}, nil
		}
	}
	var wg sync.WaitGroup
	got := make([]Reask, 8)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, _, err := s.ReserveReask("g1", build(i))
			if err != nil {
				t.Error(err)
			}
			got[i] = r
		}(i)
	}
	wg.Wait()
	if built != 1 {
		t.Fatalf("built %d re-asks", built)
	}
	for _, r := range got {
		if r.EventID != got[0].EventID {
			t.Fatalf("handlers got different re-asks: %+v", got)
		}
	}
	if s.ReaskPublished("g1") {
		t.Fatal("published before the caller published")
	}
	if err := s.MarkReaskPublished("g1"); err != nil || !s.ReaskPublished("g1") {
		t.Fatalf("mark published: %v", err)
	}
	if r, created, _ := s.ReserveReask("g1", build(99)); created || r.EventID != got[0].EventID {
		t.Fatalf("later reserve = %+v %v", r, created)
	}
}

// Concurrent approve and deny: exactly one commit wins per round, and a losing
// approve never leaves an approval file after a deny won.
func TestCommitConcurrentApproveDeny(t *testing.T) {
	s := testStore(t)
	prev := commitHook
	commitHook = func() { time.Sleep(time.Millisecond) }
	t.Cleanup(func() { commitHook = prev })
	wins := map[string]int{}
	for round := 0; round < 20; round++ {
		rec := raised(t, s, fmt.Sprintf("r%02d", round))
		var wg sync.WaitGroup
		results := make([]error, 3)
		decisions := []string{operatorauth.DecisionApproved, operatorauth.DecisionApproved, operatorauth.DecisionDenied}
		for i, d := range decisions {
			wg.Add(1)
			go func(i int, d string) {
				defer wg.Done()
				results[i] = s.Commit(answerFile(rec, d, fmt.Sprintf("%02x%s", round, strings.Repeat(fmt.Sprintf("%02x", i), 31))))
			}(i, d)
		}
		wg.Wait()
		won := 0
		for i, err := range results {
			if err == nil {
				won++
				wins[decisions[i]]++
			} else if !errors.Is(err, ErrConsumed) {
				t.Fatal(err)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d winners", round, won)
		}
		if st := s.State(rec, testNow); st == StateDenied {
			if f, _ := s.ReadAnswer(rec.GateID); f != nil {
				t.Fatalf("round %d: approval file after a deny won", round)
			}
		}
	}
	t.Logf("winners over 20 rounds: %v", wins)
}

func TestReaskOnce(t *testing.T) {
	s := testStore(t)
	raised(t, s, "g1")
	r := Reask{GateID: "g1", TriggerEventID: strings.Repeat("11", 32), EventID: strings.Repeat("22", 32), RawEvent: "{}", At: testNow.Format(time.RFC3339)}
	if err := s.PutReask(r); err != nil {
		t.Fatal(err)
	}
	if err := s.PutReask(r); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second re-ask = %v", err)
	}
	if got, err := s.ReadReask("g1"); err != nil || got == nil || got.EventID != r.EventID {
		t.Fatalf("re-ask = %+v %v", got, err)
	}
}

// The 2a-3 reader reads the S1 fields of the record unchanged.
func TestRecordKeepsTheReaderFields(t *testing.T) {
	s := testStore(t)
	raised(t, s, "g1")
	raw, err := os.ReadFile(s.Dir("gates", "g1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"gate_id", "class", "digest", "not_after", "raised_by"} {
		if m[k] == nil || m[k] == "" {
			t.Errorf("record lacks %s", k)
		}
	}
	if st, _ := os.Stat(s.Dir("gates", "g1.json")); st.Mode().Perm() != 0o600 {
		t.Errorf("record mode %v", st.Mode())
	}
}

// countingSender signs a distinct event per call and fails the first n
// publishes.
type countingSender struct {
	mu        sync.Mutex
	signed    int
	published []string
	fail      int
}

func (c *countingSender) SignQuestion(r Record) (Question, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.signed++
	return Question{GateID: r.GateID, EventID: fmt.Sprintf("%02x%s", c.signed, strings.Repeat("c3", 31)), RawEvent: "{}", PostedAt: testNow.Format(time.RFC3339)}, nil
}

func (c *countingSender) PublishQuestion(q Question) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail > 0 {
		c.fail--
		return errors.New("relay unavailable")
	}
	c.published = append(c.published, q.EventID)
	return nil
}

// Review residual (finding 8): the question is signed and recorded under the
// gate lock before it is published; concurrent posts sign one question, a
// failed publish is retried with the same event, and a published question is
// never posted again.
func TestReview8QuestionRecordedBeforePublication(t *testing.T) {
	s := testStore(t)
	rec, err := Render(spec("g8"), testNow, testIdent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create(rec); err != nil {
		t.Fatal(err)
	}
	send := &countingSender{fail: 1}
	if _, err := s.PostQuestion("g8", send); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("first post with a failing relay = %v", err)
	}
	recorded, err := s.Question("g8")
	if err != nil || recorded == nil {
		t.Fatalf("question not recorded before publication: %+v %v", recorded, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.PostQuestion("g8", send) }()
	}
	wg.Wait()
	if send.signed != 1 {
		t.Fatalf("signed %d questions", send.signed)
	}
	for _, id := range send.published {
		if id != recorded.EventID {
			t.Fatalf("published %v, recorded %s", send.published, recorded.EventID)
		}
	}
	if len(send.published) == 0 {
		t.Fatal("the recorded question was never published on retry")
	}
	n := len(send.published)
	if _, err := s.PostQuestion("g8", send); err == nil || len(send.published) != n {
		t.Fatalf("post after publication = %v (published %d -> %d)", err, n, len(send.published))
	}
}

// Second review, 4a: with both decision files present and no marks, a retry
// of the approval is refused and the marks follow the deny (deny wins).
func TestReview4aCommitConsultsTheDecisionBeforeAnyWrite(t *testing.T) {
	s := testStore(t)
	rec := raised(t, s, "g-both")
	approval := answerFile(rec, operatorauth.DecisionApproved, strings.Repeat("0d", 32))
	_ = writeAtomic(s.Dir("answers", rec.GateID+".json"), mustJSON(approval))
	_ = writeAtomic(s.Dir("denials", rec.GateID+".json"), mustJSON(answerFile(rec, operatorauth.DecisionDenied, strings.Repeat("0e", 32))))
	if err := s.Commit(approval); !errors.Is(err, ErrConsumed) {
		t.Fatalf("approval retry beside a deny record = %v", err)
	}
	mark, err := os.ReadFile(s.Dir("consumed", "gates", rec.GateID))
	if err != nil || !strings.HasPrefix(string(mark), operatorauth.DecisionDenied+" ") {
		t.Fatalf("gate mark = %q %v", mark, err)
	}
	if st := s.State(rec, testNow); st != StateDenied {
		t.Fatalf("state = %s", st)
	}
	// a denied mark with no deny record also refuses an approval
	rec2 := raised(t, s, "g-mark")
	a2 := answerFile(rec2, operatorauth.DecisionApproved, strings.Repeat("0f", 32))
	if err := writeOnce(s.Dir("consumed", "gates", rec2.GateID), []byte("denied "+strings.Repeat("1f", 32)+" t\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(a2); !errors.Is(err, ErrConsumed) {
		t.Fatalf("approval after a denied mark = %v", err)
	}
	if f, _ := s.ReadAnswer(rec2.GateID); f != nil {
		t.Fatal("an approval file was written after a denied mark")
	}
}

// Second review, 7b: a reservation left empty by a kill (an older binary
// created the name before writing) reads as absent and is repaired; new
// writes never expose a partial file because the name appears only by link.
func TestReview7bEmptyReaskReservationIsRepaired(t *testing.T) {
	s := testStore(t)
	raised(t, s, "g1")
	if err := os.MkdirAll(s.Dir("reasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Dir("reasks", "g1.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ReadReask("g1"); r != nil || err != nil {
		t.Fatalf("empty reservation read = %+v %v", r, err)
	}
	r, created, err := s.ReserveReask("g1", func() (Reask, error) {
		return Reask{GateID: "g1", TriggerEventID: strings.Repeat("11", 32), EventID: strings.Repeat("22", 32), RawEvent: "{}", At: testNow.Format(time.RFC3339)}, nil
	})
	if err != nil || !created || r.EventID != strings.Repeat("22", 32) {
		t.Fatalf("reserve over an empty reservation = %+v %v %v", r, created, err)
	}
	if got, err := s.ReadReask("g1"); err != nil || got == nil || got.EventID != r.EventID {
		t.Fatalf("after repair = %+v %v", got, err)
	}
	if err := writeOnce(s.Dir("reasks", "g1.json"), []byte("{}")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("writeOnce over an existing name = %v", err)
	}
	entries, _ := os.ReadDir(s.Dir("reasks"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// Second review, 8b: an empty question reservation does not strand the gate;
// PostQuestion signs, records and publishes a real one.
func TestReview8bEmptyQuestionReservationIsRepaired(t *testing.T) {
	s := testStore(t)
	rec, err := Render(spec("g8b"), testNow, testIdent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create(rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Dir("gates", "g8b.question.json"), []byte(" "), 0o600); err != nil {
		t.Fatal(err)
	}
	if q, err := s.Question("g8b"); q != nil || err != nil {
		t.Fatalf("empty question read = %+v %v", q, err)
	}
	send := &countingSender{}
	q, err := s.PostQuestion("g8b", send)
	if err != nil || send.signed != 1 || len(send.published) != 1 || send.published[0] != q.EventID || !s.QuestionPublished("g8b") {
		t.Fatalf("repair = %+v %v (signed %d published %v)", q, err, send.signed, send.published)
	}
}
