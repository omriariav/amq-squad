package gatestore

import (
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

// Sender posts a gate question and returns what it posted.
type Sender interface {
	PostQuestion(Record) (Question, error)
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

// PostQuestion sends the question of a stored gate once and records it. It
// refuses a gate whose question is already recorded.
func (s Store) PostQuestion(gateID string, send Sender) (Question, error) {
	rec, err := s.Load(gateID)
	if err != nil {
		return Question{}, err
	}
	if q, err := s.Question(gateID); err != nil || q != nil {
		return Question{}, errors.Join(err, errOnlyIf(q != nil, "gate %s already has a question", gateID))
	}
	if send == nil {
		return Question{}, errors.New("no transport to send the gate question")
	}
	q, err := send.PostQuestion(rec)
	if err != nil {
		return Question{}, fmt.Errorf("send gate question (the record stays; retry the send): %w", err)
	}
	if q.GateID != rec.GateID || !hex64.MatchString(q.EventID) {
		return Question{}, errors.New("the transport returned a malformed question")
	}
	dir, _ := s.path(gateID, "gates")
	return q, writeOnce(filepath.Join(dir, gateID+".question.json"), mustJSON(q))
}

func errOnlyIf(cond bool, format string, args ...any) error {
	if !cond {
		return nil
	}
	return fmt.Errorf(format, args...)
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
	if err := readStrict(filepath.Join(dir, gateID+".question.json"), &q); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if q.GateID != gateID || !hex64.MatchString(q.EventID) {
		return nil, errors.New("gate question record is malformed")
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

// State reports a gate's state from the files alone: accepted or denied once
// consumed (acceptance is terminal: not_after governs only an unanswered
// gate), else expired past not_after, else open. It does not re-verify the
// answer file; guard does that against the trust store.
func (s Store) State(rec Record, now time.Time) string {
	if consumed, decision := s.consumedDecision(rec.GateID); consumed {
		return decision
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
	if err := os.MkdirAll(s.Dir("consumed", "locks"), 0o700); err != nil {
		return err
	}
	lock, err := flock.AcquireExclusive(s.Dir("consumed", "locks", id+".lock"))
	if err != nil {
		return err // no lock, no consume (non-unix fails closed)
	}
	defer lock.Close()
	if err := s.replayed(id, f.Evidence.EventID); err != nil {
		return err
	}
	commitHook()
	if err := writeAtomic(s.Dir(sub, id+".json"), mustJSON(f)); err != nil {
		return fmt.Errorf("write %s (nothing consumed): %w", sub, err)
	}
	line := []byte(fmt.Sprintf("%s %s %s\n", f.Decision, f.Evidence.EventID, time.Now().UTC().Format(time.RFC3339)))
	for _, m := range []string{s.Dir("consumed", "gates", id), s.Dir("consumed", "events", f.Evidence.EventID)} {
		if err := writeOnce(m, line); errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", ErrConsumed, filepath.Base(m))
		} else if err != nil {
			return err
		}
	}
	return nil
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
	if err := readStrict(filepath.Join(dir, gateID+".json"), &r); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
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
func writeOnce(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(path)
		return err
	}
	return syncDir(filepath.Dir(path))
}

// writeAtomic replaces path durably: temp file, fsync, rename, directory fsync.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
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
