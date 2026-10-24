package gatestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Store is the gate store of one AMQ session root.
type Store struct{ Root string }

// Open returns the store under an AMQ session root (absolute).
func Open(root string) (Store, error) {
	if !filepath.IsAbs(root) {
		return Store{}, fmt.Errorf("gate store root %q must be absolute", root)
	}
	return Store{Root: filepath.Clean(root)}, nil
}

// Dir is <root>/extensions/amq-squad/<parts...>.
func (s Store) Dir(parts ...string) string {
	return filepath.Join(append([]string{s.Root, "extensions", "amq-squad"}, parts...)...)
}

func (s Store) path(id string, parts ...string) (string, error) {
	if err := ValidGateID(id); err != nil {
		return "", err
	}
	return s.Dir(parts...), nil
}

// Question is the gate question as the transport posted it. For the Buzz
// transport it is the body-signed kind 9 event in the DM channel whose
// content is the record's text (S1 1.1); the thread binding checks answers
// against EventID.
type Question struct {
	GateID   string `json:"gate_id"`
	EventID  string `json:"event_id"`
	RawEvent string `json:"raw_event"`
	Relay    string `json:"relay,omitempty"`
	Channel  string `json:"channel,omitempty"`
	PostedAt string `json:"posted_at"`
}

// Sender signs a gate question without publishing it, and publishes a signed
// question. The store records the signed question before it is published.
type Sender interface {
	SignQuestion(Record) (Question, error)
	PublishQuestion(Question) error
}

// raiseHook runs between the record write and the send. It is a variable only
// so the write-before-send test can stop a process there (acceptance 1).
var raiseHook = func(Record) {}

// Raise writes the gate record (O_EXCL, fsynced) and only then sends the
// question (plan 7.3: the record exists before the question does). A second
// raise of the same id fails with os.ErrExist and sends nothing. When the
// send fails the record stays and PostQuestion can retry the send.
func (s Store) Raise(spec Spec, id Identity, now time.Time, send Sender) (Record, Question, error) {
	rec, err := Render(spec, now, id)
	if err != nil {
		return Record{}, Question{}, err
	}
	if err := s.Create(rec); err != nil {
		return Record{}, Question{}, err
	}
	raiseHook(rec)
	q, err := s.PostQuestion(rec.GateID, send)
	return rec, q, err
}

// Create writes a record once.
func (s Store) Create(rec Record) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	dir, err := s.path(rec.GateID, "gates")
	if err != nil {
		return err
	}
	return writeOnce(filepath.Join(dir, rec.GateID+".json"), mustJSON(rec))
}

// PostQuestion sends the one question of a stored gate. Under the gate lock it
// signs the question and records it (O_EXCL, fsynced) before anything is
// published, so concurrent posts, or a crash between publish and persist,
// never produce a second question: a retry publishes the recorded event
// again. A question already published is refused.
func (s Store) PostQuestion(gateID string, send Sender) (Question, error) {
	rec, err := s.Load(gateID)
	if err != nil {
		return Question{}, err
	}
	if send == nil {
		return Question{}, errors.New("no transport to send the gate question")
	}
	q, err := s.reserveQuestion(rec, send)
	if err != nil {
		return Question{}, err
	}
	if s.questionPublished(gateID) {
		return q, fmt.Errorf("gate %s already has a published question", gateID)
	}
	if err := send.PublishQuestion(q); err != nil {
		return q, fmt.Errorf("publish the gate question %s (recorded; a retry publishes the same event): %w", q.EventID, err)
	}
	err = writeOnce(s.Dir("gates", gateID+".question.published"), []byte("published\n"))
	if errors.Is(err, os.ErrExist) {
		err = nil
	}
	return q, err
}

// reserveQuestion returns the gate's recorded question, signing and recording
// it under the gate lock when there is none.
func (s Store) reserveQuestion(rec Record, send Sender) (Question, error) {
	if err := mkdirDurable(s.Dir("consumed", "locks")); err != nil {
		return Question{}, err
	}
	lock, err := flock.AcquireExclusive(s.Dir("consumed", "locks", rec.GateID+".lock"))
	if err != nil {
		return Question{}, err
	}
	defer lock.Close()
	if q, err := s.Question(rec.GateID); err != nil || q != nil {
		if q == nil {
			return Question{}, err
		}
		return *q, nil
	}
	if err := clearUnusable(s.Dir("gates", rec.GateID+".question.json")); err != nil {
		return Question{}, err
	}
	q, err := send.SignQuestion(rec)
	if err != nil {
		return Question{}, fmt.Errorf("sign the gate question (the record stays; retry the send): %w", err)
	}
	if q.GateID != rec.GateID || !hex64.MatchString(q.EventID) {
		return Question{}, errors.New("the transport signed a malformed question")
	}
	return q, writeOnce(s.Dir("gates", rec.GateID+".question.json"), mustJSON(q))
}

// QuestionPublished reports whether the gate's recorded question reached the
// relay.
func (s Store) QuestionPublished(gateID string) bool { return s.questionPublished(gateID) }

func (s Store) questionPublished(gateID string) bool {
	_, err := os.Lstat(s.Dir("gates", gateID+".question.published"))
	return err == nil
}

// Load reads and validates a record.
func (s Store) Load(gateID string) (Record, error) {
	dir, err := s.path(gateID, "gates")
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := readStrict(filepath.Join(dir, gateID+".json"), &r); err != nil {
		return Record{}, fmt.Errorf("gate %s: %w", gateID, err)
	}
	if r.GateID != gateID {
		return Record{}, errors.New("gate record names another gate id")
	}
	return r, r.Validate()
}

// Question reads the stored question of a gate, nil when none was posted.
func (s Store) Question(gateID string) (*Question, error) {
	dir, err := s.path(gateID, "gates")
	if err != nil {
		return nil, err
	}
	var q Question
	if ok, err := readReservation(filepath.Join(dir, gateID+".question.json"), &q); !ok || err != nil {
		return nil, err
	}
	if q.GateID != gateID || !hex64.MatchString(q.EventID) {
		return nil, nil // not a question of this gate: absent; PostQuestion replaces it under the lock
	}
	return &q, nil
}

// IDs lists the gate ids that have a record file.
func (s Store) IDs() ([]string, error) {
	entries, err := os.ReadDir(s.Dir("gates"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if id, ok := strings.CutSuffix(name, ".json"); ok && !e.IsDir() && !strings.HasSuffix(name, ".question.json") && ValidGateID(id) == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// State of a gate in the store.
const (
	StateOpen     = "open"
	StateAccepted = "accepted"
	StateDenied   = "denied"
	StateExpired  = "expired"
)

// State reports a gate's durable decision from the files alone: denied when a
// deny record or a denied mark exists (a deny wins any conflict: fail
// closed), else accepted when an approval file or an approved mark exists
// (acceptance is terminal: not_after governs only an unanswered gate), else
// expired past not_after, else open. An answer file without its marks (a
// crash between the two writes) is the decision; Commit completes its marks
// under the lock before it accepts anything else. State does not re-verify
// the answer file; consumers do (gateverify.VerifyAnswerFile).
func (s Store) State(rec Record, now time.Time) string {
	if d := s.Decision(rec.GateID); d != "" {
		return d
	}
	if _, _, na, err := rec.Window(); err == nil && now.After(na) {
		return StateExpired
	}
	return StateOpen
}

// AnswerSchema names the answer file format.
const AnswerSchema = "amq-squad-gate-answer/1"

// Evidence is the owner's signed answer as carried beside the envelope: the
// event bytes exactly as fetched, so a consumer re-verifies the owner's
// signature and never rests on the issuer key alone (S1 section 6).
type Evidence struct {
	Scheme      string `json:"scheme"` // bip340
	EventID     string `json:"event_id"`
	OwnerPubKey string `json:"owner_pubkey"`
	Channel     string `json:"channel"`
	Relay       string `json:"relay,omitempty"`
	RawEvent    string `json:"raw_event"`
}

// AnswerFile is answers/<id>.json (an approval) or denials/<id>.json (a deny
// record). Gate and Question are unsigned copies of the stored records; a
// consumer compares them with the store and never uses them as a source.
type AnswerFile struct {
	Schema   string                             `json:"schema"`
	Scheme   string                             `json:"scheme"`
	Decision string                             `json:"decision"` // approved | denied
	Envelope operatorauth.AuthorizationEnvelope `json:"envelope"`
	Evidence Evidence                           `json:"bip340_evidence"`
	Gate     Record                             `json:"gate"`
	Question *Question                          `json:"question,omitempty"`
}

// Typed rejection codes the core reports.
const (
	RejectReplayed = "already_consumed"
)

// ErrConsumed is a replay: the gate or the event was already used.
var ErrConsumed = errors.New(RejectReplayed)

// commitHook runs inside Commit after the replay check and before any write;
// tests widen the race window with it.
var commitHook = func() {}

// Commit is the only consuming path (S1 1.3). Under the gate's lock it refuses
// a replay (the gate already answered, or this event used), writes the answer
// file (envelope first: temp file, fsync, rename, directory fsync), then the
// consumed marks (gate, then event; O_EXCL, fsynced). The first committed
// answer, approve or deny, closes the gate. A crash between the answer file
// and the marks leaves an unconsumed answer that re-verifies on restart.
func (s Store) Commit(f AnswerFile) error {
	id := f.Gate.GateID
	if err := ValidGateID(id); err != nil {
		return err
	}
	if !hex64.MatchString(f.Evidence.EventID) {
		return errors.New("answer evidence names no event")
	}
	sub := map[string]string{operatorauth.DecisionApproved: "answers", operatorauth.DecisionDenied: "denials"}[f.Decision]
	if sub == "" || f.Envelope.Payload.Decision != f.Decision || f.Schema != AnswerSchema {
		return errors.New("answer file decision or schema is invalid")
	}
	if err := mkdirDurable(s.Dir("consumed", "locks")); err != nil {
		return err
	}
	lock, err := flock.AcquireExclusive(s.Dir("consumed", "locks", id+".lock"))
	if err != nil {
		return err // no lock, no consume (non-unix fails closed)
	}
	defer lock.Close()
	decided, incomplete, err := s.recoverLocked(id)
	if err != nil {
		return err
	}
	if decided != nil {
		if incomplete && decided.Decision == f.Decision && decided.Evidence.EventID == f.Evidence.EventID {
			return nil // the retry of the interrupted commit
		}
		return fmt.Errorf("%w: gate %s already has a %s answer", ErrConsumed, id, decided.Decision)
	}
	if err := s.replayed(id, f.Evidence.EventID); err != nil {
		return err
	}
	commitHook()
	if err := writeAtomic(s.Dir(sub, id+".json"), mustJSON(f)); err != nil {
		return fmt.Errorf("write %s (nothing consumed): %w", sub, err)
	}
	return s.markConsumed(id, f)
}

// markConsumed writes the gate and event marks of a written answer file; a
// mark that already exists for this same answer is kept (recovery).
func (s Store) markConsumed(id string, f AnswerFile) error {
	line := []byte(fmt.Sprintf("%s %s %s\n", f.Decision, f.Evidence.EventID, time.Now().UTC().Format(time.RFC3339)))
	for _, m := range []string{s.Dir("consumed", "gates", id), s.Dir("consumed", "events", f.Evidence.EventID)} {
		err := writeOnce(m, line)
		if errors.Is(err, os.ErrExist) {
			if prior, rerr := os.ReadFile(m); rerr == nil && strings.HasPrefix(string(prior), f.Decision+" "+f.Evidence.EventID+" ") {
				continue
			}
			return fmt.Errorf("%w: %s", ErrConsumed, filepath.Base(m))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Recover repairs the marks of a gate's durable decision under the gate lock:
// a crash between the answer file and its marks, or between the two marks,
// leaves a decision whose missing marks are written here. The core calls it
// before its replay check, so a half-marked decision never strands the gate.
func (s Store) Recover(gateID string) error {
	if err := ValidGateID(gateID); err != nil {
		return err
	}
	if err := mkdirDurable(s.Dir("consumed", "locks")); err != nil {
		return err
	}
	lock, err := flock.AcquireExclusive(s.Dir("consumed", "locks", gateID+".lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	lockedAt := time.Now()
	_, _, err = s.recoverLocked(gateID)
	return errors.Join(err, s.sweepTemps(lockedAt))
}

// recoverLocked finds the gate's durable decision with the lock held. The
// deny wins (the same rule as Decision): a deny record, else a denied mark,
// else an approval file. It writes any missing mark of that decision and
// reports whether marks were missing. A denied mark with no deny record is a
// decision with nothing to repair.
func (s Store) recoverLocked(id string) (decided *AnswerFile, incomplete bool, err error) {
	denial, derr := s.readAnswerFile(id, "denials")
	approval, aerr := s.readAnswerFile(id, "answers")
	if derr != nil || aerr != nil {
		return nil, false, fmt.Errorf("%w: an unreadable decision file closes gate %s: %v", ErrConsumed, id, errors.Join(derr, aerr))
	}
	consumed, mark := s.consumedDecision(id)
	switch {
	case denial != nil:
		decided = denial
	case consumed && mark == StateDenied:
		return nil, false, fmt.Errorf("%w: gate %s is denied", ErrConsumed, id)
	case approval != nil:
		decided = approval
	case consumed:
		return nil, false, fmt.Errorf("%w: gate %s", ErrConsumed, id)
	default:
		return nil, false, nil
	}
	for _, m := range []string{s.Dir("consumed", "gates", id), s.Dir("consumed", "events", decided.Evidence.EventID)} {
		if _, err := os.Lstat(m); err != nil {
			incomplete = true
		}
	}
	if err := s.markConsumed(id, *decided); err != nil {
		return nil, false, err
	}
	return decided, incomplete, nil
}

// Replayed reports whether a gate already has a consumed answer or an event
// was already used. Commit repeats the check under the lock.
func (s Store) Replayed(gateID, eventID string) error {
	if err := ValidGateID(gateID); err != nil {
		return err
	}
	return s.replayed(gateID, eventID)
}

func (s Store) replayed(gateID, eventID string) error {
	for _, m := range []string{s.Dir("consumed", "gates", gateID), s.Dir("consumed", "events", eventID)} {
		if _, err := os.Lstat(m); err == nil {
			return fmt.Errorf("%w: %s", ErrConsumed, filepath.Base(m))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Decision is the gate's recovered durable decision: StateDenied,
// StateAccepted or "" (none yet). Every reader of the store uses it.
func (s Store) Decision(gateID string) string {
	exists := func(parts ...string) bool { _, err := os.Lstat(s.Dir(parts...)); return err == nil }
	consumed, mark := s.consumedDecision(gateID)
	switch {
	case exists("denials", gateID+".json") || (consumed && mark == StateDenied):
		return StateDenied
	case exists("answers", gateID+".json") || consumed:
		return StateAccepted
	}
	return ""
}

// consumedDecision reads the gate's consumed mark: "approved ..." or
// "denied ..." on its first line.
func (s Store) consumedDecision(gateID string) (bool, string) {
	raw, err := os.ReadFile(s.Dir("consumed", "gates", gateID))
	if err != nil {
		return false, ""
	}
	switch strings.SplitN(string(raw), " ", 2)[0] {
	case operatorauth.DecisionApproved:
		return true, StateAccepted
	case operatorauth.DecisionDenied:
		return true, StateDenied
	}
	return true, StateDenied // an unreadable mark still closes the gate
}

// ReadAnswer reads answers/<id>.json, nil when absent.
func (s Store) ReadAnswer(gateID string) (*AnswerFile, error) {
	return s.readAnswerFile(gateID, "answers")
}

// ReadDenial reads denials/<id>.json, nil when absent.
func (s Store) ReadDenial(gateID string) (*AnswerFile, error) {
	return s.readAnswerFile(gateID, "denials")
}

func (s Store) readAnswerFile(gateID, sub string) (*AnswerFile, error) {
	dir, err := s.path(gateID, sub)
	if err != nil {
		return nil, err
	}
	var f AnswerFile
	if err := readStrict(filepath.Join(dir, gateID+".json"), &f); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &f, nil
}

// Reask is the one "reply here to confirm" re-post of a gate (amendment 19).
type Reask struct {
	GateID         string `json:"gate_id"`
	TriggerEventID string `json:"trigger_event_id"` // the non-thread answer that matched the gate
	EventID        string `json:"event_id"`         // the re-post in the gate thread
	RawEvent       string `json:"raw_event"`
	At             string `json:"at"`
}

// ReserveReask returns the gate's one re-ask, creating it under the gate lock
// when none exists: build signs the event (it is not published yet), and the
// record is fsynced before the caller publishes, so concurrent handlers and a
// crash between publish and persist can never produce a second re-ask. The
// caller publishes the reserved bytes and marks them published; a later call
// returns the same event for a retry.
func (s Store) ReserveReask(gateID string, build func() (Reask, error)) (Reask, bool, error) {
	if err := ValidGateID(gateID); err != nil {
		return Reask{}, false, err
	}
	if err := mkdirDurable(s.Dir("consumed", "locks")); err != nil {
		return Reask{}, false, err
	}
	lock, err := flock.AcquireExclusive(s.Dir("consumed", "locks", gateID+".lock"))
	if err != nil {
		return Reask{}, false, err
	}
	defer lock.Close()
	if r, err := s.ReadReask(gateID); err != nil || r != nil {
		if r == nil {
			return Reask{}, false, err
		}
		return *r, false, err
	}
	if err := clearUnusable(s.Dir("reasks", gateID+".json")); err != nil {
		return Reask{}, false, err
	}
	r, err := build()
	if err != nil {
		return Reask{}, false, err
	}
	if r.GateID != gateID {
		return Reask{}, false, errors.New("re-ask names another gate")
	}
	return r, true, s.PutReask(r)
}

// ReaskPublished reports whether the gate's re-ask was published.
func (s Store) ReaskPublished(gateID string) bool {
	_, err := os.Lstat(s.Dir("reasks", gateID+".published"))
	return err == nil
}

// MarkReaskPublished records that the gate's re-ask reached the relay.
func (s Store) MarkReaskPublished(gateID string) error {
	if err := ValidGateID(gateID); err != nil {
		return err
	}
	err := writeOnce(s.Dir("reasks", gateID+".published"), []byte("published\n"))
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}

// PutReask records the gate's re-ask once; a second one fails os.ErrExist.
func (s Store) PutReask(r Reask) error {
	dir, err := s.path(r.GateID, "reasks")
	if err != nil {
		return err
	}
	if !hex64.MatchString(r.TriggerEventID) || !hex64.MatchString(r.EventID) {
		return errors.New("re-ask record is malformed")
	}
	return writeOnce(filepath.Join(dir, r.GateID+".json"), mustJSON(r))
}

// ReadReask reads the gate's re-ask, nil when none was posted.
func (s Store) ReadReask(gateID string) (*Reask, error) {
	dir, err := s.path(gateID, "reasks")
	if err != nil {
		return nil, err
	}
	var r Reask
	if ok, err := readReservation(filepath.Join(dir, gateID+".json"), &r); !ok || err != nil {
		return nil, err
	}
	if r.GateID != gateID || !hex64.MatchString(r.EventID) {
		return nil, nil // not a re-ask of this gate: absent; ReserveReask replaces it under the lock
	}
	return &r, nil
}

func mustJSON(v any) []byte {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err) // only fixed struct types reach here
	}
	return append(raw, '\n')
}

// readStrict decodes one JSON value with unknown fields refused.
func readStrict(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// writeOnce creates path exclusively, writes and fsyncs it, then fsyncs the
// directory, so a created file is durable before anyone acts on it.
// The final name appears only with its full, fsynced content: the bytes go to
// a temp file in the same directory, are fsynced, and are hard-linked into
// place (link fails if the name exists, which keeps O_EXCL's create-once
// rule), so a kill at any point never leaves an empty or partial file there.
func writeOnce(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := mkdirDurable(dir); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return &os.PathError{Op: "create", Path: path, Err: os.ErrExist}
	}
	f, err := os.CreateTemp(dir, ".tmp-once-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(tmp, 0o600)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	lerr := os.Link(tmp, path)
	_ = os.Remove(tmp) // before the fsync, so one fsync covers the link and the unlink
	if lerr != nil {
		return lerr
	}
	return syncDir(dir)
}

// mkdirDurable creates dir and any missing parents, fsyncing each new
// directory's parent, so a directory created for a reservation survives a
// power loss with the reservation in it.
func mkdirDurable(dir string) error {
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		return nil
	}
	parent := filepath.Dir(dir)
	if parent != dir {
		if err := mkdirDurable(parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDir(parent)
}

// tempGrace is how old a write temp must be, before the gate lock was taken,
// to count as orphaned: a live writer holds its temp for milliseconds.
const tempGrace = time.Minute

// sweepTemps removes write temps (.tmp-once-*, .tmp-*) that a killed writer
// left in the store's directories, older than the lock acquisition by
// tempGrace, and fsyncs each directory it changed once.
func (s Store) sweepTemps(lockedAt time.Time) error {
	cutoff := lockedAt.Add(-tempGrace)
	var errs []error
	for _, d := range [][]string{{"gates"}, {"answers"}, {"denials"}, {"reasks"}, {"consumed", "gates"}, {"consumed", "events"}} {
		dir := s.Dir(d...)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		changed := false
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".tmp-") || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) && os.Remove(filepath.Join(dir, e.Name())) == nil {
				changed = true
			}
		}
		if changed {
			errs = append(errs, syncDir(dir))
		}
	}
	return errors.Join(errs...)
}

// readReservation decodes a reservation file into v. It reports absent (false,
// nil) for a missing file and for any file that does not decode strictly to
// the expected struct (empty, `[]`, partial, unknown fields): such a file was
// never a recorded event, and the reserve path replaces it under the gate
// lock. An I/O error other than absence is returned.
func readReservation(path string, v any) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.More() {
		return false, nil
	}
	return true, nil
}

// clearUnusable removes a reservation file that the reader treats as absent.
// It runs only with the gate lock held, just before a real reservation is
// recorded at that name.
func clearUnusable(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// writeAtomic replaces path durably: temp file, fsync, rename, directory fsync.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := mkdirDurable(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(tmp, 0o600)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(dir)
}
