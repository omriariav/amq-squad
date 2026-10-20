package main

// Gate rendering and the gate record.
//
// The Go core, not a model, writes the text the owner reads. That text
// carries the gate id, the action class, the target digest and the ordered
// PR numbers, and nothing else: no code, paths, branch names, repo names,
// commit messages or secrets. Everything the digest covers that the text
// does not show (repository, base SHA, head SHAs) is bound by the digest
// alone.
//
// Binding rule: the core writes the gate record, write-once, into the gate
// store under the AMQ root before the question is sent. The record holds the
// full target digest. The verifier loads the record by gate id from that
// store and compares the owner's typed prefix against that record only. The
// action after an approval executes against the record's full digest, never
// against anything a seat presents later. One gate id names one record and
// so one digest. An approval of gate G therefore authorizes G's digest and
// nothing else, so choosing two targets with a shared 16-hex prefix gains an
// attacker nothing. The prefix is the owner's visual check, not the binding.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	gateRecordSchema   = "amq-squad-gate-spike/v2"
	targetDigestDomain = "amq-squad-gate-target-v1"
	// minDigestPrefix is the shortest digest prefix an answer may type: 16
	// hex characters, 64 bits. It is safe because of the binding rule above,
	// not because of its length.
	minDigestPrefix = 16
	// clockSkew is the one clock allowance, 60 s. An answer's created_at may
	// sit up to 60 s before the gate was issued (owner phone clock behind),
	// and up to 60 s after the verifier's clock (owner clock ahead). The
	// presentation time must satisfy created_at - 60 s <= now <= not_after.
	// There is no grace after not_after.
	clockSkew = 60 * time.Second
)

// Classes are the five typed gates of plan 7.1 / R30.
var gateClasses = map[string]bool{"merge": true, "release": true, "send": true, "credential": true, "grow": true}

var (
	gateIDPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,46}[a-z0-9])$`)
	repoPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
	gitSHAPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	digestPrefixRe = regexp.MustCompile(`^[0-9a-f]{16,64}$`)
)

// PRRef is one PR of a merge batch.
type PRRef struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// GateSpec is the core's input to render. For merge it is the R34 batch:
// ordered (PR, head SHA) pairs plus the base SHA. Other classes carry an
// opaque subject digest the core computed elsewhere.
type GateSpec struct {
	GateID        string  `json:"gate_id"`
	Class         string  `json:"class"`
	Repo          string  `json:"repo,omitempty"`
	BaseSHA       string  `json:"base_sha,omitempty"`
	PRs           []PRRef `json:"prs,omitempty"`
	SubjectSHA256 string  `json:"subject_sha256,omitempty"`
	WindowMinutes int     `json:"window_minutes"`
}

// GateRecord is what the core keeps for verification. The window and the
// digest come from here, never from the answer or from a caller.
type GateRecord struct {
	Schema     string `json:"schema"`
	GateID     string `json:"gate_id"`
	Class      string `json:"class"`
	Digest     string `json:"digest"`
	PRs        []int  `json:"prs"`
	Text       string `json:"text"`
	TextSHA256 string `json:"text_sha256"`
	IssuedAt   string `json:"issued_at"`
	NotBefore  string `json:"not_before"`
	NotAfter   string `json:"not_after"`
}

// GateQuestion is the body-signed question event as posted, kept write-once
// beside its record.
type GateQuestion struct {
	GateID   string `json:"gate_id"`
	EventID  string `json:"event_id"`
	RawEvent string `json:"raw_event"`
}

func (s GateSpec) validate() error {
	if !gateIDPattern.MatchString(s.GateID) {
		return errors.New("gate id must be 3..48 of [a-z0-9-], starting and ending alphanumeric")
	}
	if !gateClasses[s.Class] {
		return fmt.Errorf("class %q is not one of merge, release, send, credential, grow", s.Class)
	}
	if s.WindowMinutes < 1 || s.WindowMinutes > 24*60 {
		return errors.New("window_minutes must be 1..1440")
	}
	if s.Class == "merge" {
		owner, name, _ := strings.Cut(s.Repo, "/")
		if !repoPattern.MatchString(s.Repo) || strings.Trim(owner, ".") == "" || strings.Trim(name, ".") == "" ||
			!gitSHAPattern.MatchString(s.BaseSHA) || len(s.PRs) == 0 || len(s.PRs) > 50 || s.SubjectSHA256 != "" {
			return errors.New("merge needs repo owner/name, a lowercase base SHA, 1..50 PRs, and no subject digest")
		}
		seen := map[int]bool{}
		for _, pr := range s.PRs {
			if pr.Number < 1 || pr.Number > 1_000_000_000 || seen[pr.Number] || !gitSHAPattern.MatchString(pr.HeadSHA) {
				return errors.New("each PR needs a unique number and a lowercase head SHA")
			}
			seen[pr.Number] = true
		}
		return nil
	}
	if !hex64.MatchString(s.SubjectSHA256) || s.Repo != "" || s.BaseSHA != "" || len(s.PRs) != 0 {
		return errors.New("non-merge classes carry only subject_sha256 (64 lowercase hex)")
	}
	return nil
}

// TargetDigest is sha256 over a length-prefixed encoding of the class and
// target. For merge it covers repo, base SHA and the ordered (PR, head)
// pairs, so a reorder, a new head or a new base changes it (R34).
func (s GateSpec) TargetDigest() string {
	var b []byte
	put := func(v string) {
		b = binary.BigEndian.AppendUint32(b, uint32(len(v)))
		b = append(b, v...)
	}
	put(targetDigestDomain)
	put(s.Class)
	if s.Class == "merge" {
		put(s.Repo)
		put(s.BaseSHA)
		put(strconv.Itoa(len(s.PRs)))
		for _, pr := range s.PRs {
			put(strconv.Itoa(pr.Number))
			put(pr.HeadSHA)
		}
	} else {
		put(s.SubjectSHA256)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// gateText is the only renderer of the owner-facing text. validate rebuilds
// the text from the record's own fields with it, so a stored text cannot
// drift from the stored digest.
func gateText(id, class, digest string, prs []int) string {
	prefix := strings.TrimPrefix(digest, "sha256:")[:minDigestPrefix]
	lines := []string{"amq-squad gate " + id, "class: " + class, "digest: " + digest}
	if len(prs) > 0 {
		var t []string
		for _, n := range prs {
			t = append(t, "#"+strconv.Itoa(n))
		}
		lines = append(lines, "PRs in merge order: "+strings.Join(t, " "))
	}
	lines = append(lines, "Answer with one line:",
		fmt.Sprintf("approve %s %s %s", id, class, prefix),
		fmt.Sprintf("deny %s %s %s", id, class, prefix))
	return strings.Join(lines, "\n")
}

// Render builds the gate record at now. It does not store it: see
// GateStore.Create.
func Render(s GateSpec, now time.Time) (GateRecord, error) {
	if err := s.validate(); err != nil {
		return GateRecord{}, err
	}
	digest := s.TargetDigest()
	prs := []int{}
	for _, pr := range s.PRs {
		prs = append(prs, pr.Number)
	}
	text := gateText(s.GateID, s.Class, digest, prs)
	sum := sha256.Sum256([]byte(text))
	now = now.UTC().Truncate(time.Second)
	return GateRecord{
		Schema: gateRecordSchema, GateID: s.GateID, Class: s.Class, Digest: digest, PRs: prs,
		Text: text, TextSHA256: "sha256:" + hex.EncodeToString(sum[:]),
		IssuedAt:  now.Format(time.RFC3339),
		NotBefore: now.Add(-clockSkew).Format(time.RFC3339),
		NotAfter:  now.Add(time.Duration(s.WindowMinutes) * time.Minute).Format(time.RFC3339),
	}, nil
}

// Answer is a parsed owner reply.
type Answer struct {
	Verb         string `json:"verb"`
	GateID       string `json:"gate_id"`
	Class        string `json:"class"`
	DigestPrefix string `json:"digest_prefix"`
}

// ParseAnswer reads exactly "approve|deny <gate-id> <class> <hex prefix>".
// The content must be one line: any CR or LF anywhere, even trailing, is
// refused. Case is folded, because a phone keyboard capitalizes the first
// word, and runs of spaces or tabs are one separator. Any other token,
// punctuation or a short prefix refuses. The tuple is the whole content,
// so nothing can ride along that a later reader might take for a second
// instruction.
func ParseAnswer(content string) (Answer, error) {
	if strings.ContainsAny(content, "\r\n") {
		return Answer{}, errors.New("answer must be one line (no CR or LF)")
	}
	fields := strings.Fields(strings.ToLower(content))
	if len(fields) != 4 {
		return Answer{}, fmt.Errorf("answer must be exactly 4 tokens, got %d", len(fields))
	}
	a := Answer{Verb: fields[0], GateID: fields[1], Class: fields[2], DigestPrefix: fields[3]}
	switch {
	case a.Verb != "approve" && a.Verb != "deny":
		return Answer{}, fmt.Errorf("answer verb %q is not approve or deny", fields[0])
	case !gateIDPattern.MatchString(a.GateID):
		return Answer{}, errors.New("answer gate id is malformed")
	case !gateClasses[a.Class]:
		return Answer{}, errors.New("answer class is not a typed gate class")
	case !digestPrefixRe.MatchString(a.DigestPrefix):
		return Answer{}, fmt.Errorf("answer digest prefix must be %d..64 hex", minDigestPrefix)
	}
	return a, nil
}

func (g GateRecord) window() (issued, nb, na time.Time, err error) {
	if issued, err = time.Parse(time.RFC3339, g.IssuedAt); err != nil {
		return issued, nb, na, errors.New("gate record issued_at is invalid")
	}
	if nb, err = time.Parse(time.RFC3339, g.NotBefore); err != nil || !nb.Equal(issued.Add(-clockSkew)) {
		return issued, nb, na, errors.New("gate record not_before is not issued_at - 60 s")
	}
	if na, err = time.Parse(time.RFC3339, g.NotAfter); err != nil || !na.After(issued) || na.Sub(issued) > 24*time.Hour {
		return issued, nb, na, errors.New("gate record not_after is invalid")
	}
	return issued, nb, na, nil
}

// validate checks the record is self-consistent: the text is exactly what
// gateText renders from the record's id, class, digest and PRs, and the
// text digest matches it.
func (g GateRecord) validate() error {
	if g.Schema != gateRecordSchema || !gateIDPattern.MatchString(g.GateID) || !gateClasses[g.Class] ||
		!strings.HasPrefix(g.Digest, "sha256:") || !hex64.MatchString(strings.TrimPrefix(g.Digest, "sha256:")) || g.PRs == nil {
		return errors.New("gate record is malformed")
	}
	if g.Text != gateText(g.GateID, g.Class, g.Digest, g.PRs) {
		return errors.New("gate record text is not the rendering of its own fields")
	}
	sum := sha256.Sum256([]byte(g.Text))
	if g.TextSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		return errors.New("gate record text digest does not match its text")
	}
	_, _, _, err := g.window()
	return err
}

// GateStore is the core's gate directory under the AMQ root:
// <root>/extensions/amq-squad/gates/<gate-id>.json (record) and
// <gate-id>.question.json (the posted question). Both are created once
// (O_EXCL) and never rewritten. The host threat model is the same as the
// ledger's (verify.go): same-UID processes can write here. That is accepted
// for phase 1 on a single-user Mac, and the core is the only intended writer.
type GateStore struct{ Dir string }

// GateStoreFor returns the store under an AMQ root.
func GateStoreFor(root string) GateStore {
	return GateStore{Dir: filepath.Join(root, "extensions", "amq-squad", "gates")}
}

func (s GateStore) path(id, suffix string) (string, error) {
	if !gateIDPattern.MatchString(id) {
		return "", errors.New("gate id is malformed")
	}
	if !filepath.IsAbs(s.Dir) {
		return "", errors.New("gate store must be an absolute path")
	}
	return filepath.Join(s.Dir, id+suffix), nil
}

// Create writes the record once. A second record for the same gate id is
// refused, which is what makes "one gate id, one digest" hold.
func (s GateStore) Create(g GateRecord) error {
	if err := g.validate(); err != nil {
		return err
	}
	p, err := s.path(g.GateID, ".json")
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return writeOnce(p, append(raw, '\n'))
}

// Load reads and validates the record for id.
func (s GateStore) Load(id string) (GateRecord, error) {
	p, err := s.path(id, ".json")
	if err != nil {
		return GateRecord{}, err
	}
	var g GateRecord
	if err := readJSON(p, &g); err != nil {
		return GateRecord{}, fmt.Errorf("gate %s: %w", id, err)
	}
	if g.GateID != id {
		return GateRecord{}, errors.New("gate record names another gate id")
	}
	return g, g.validate()
}

// PutQuestion records the posted question once.
func (s GateStore) PutQuestion(q GateQuestion) error {
	p, err := s.path(q.GateID, ".question.json")
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	return writeOnce(p, append(raw, '\n'))
}

// Question reads the posted question, if there is one.
func (s GateStore) Question(id string) (*GateQuestion, error) {
	p, err := s.path(id, ".question.json")
	if err != nil {
		return nil, err
	}
	var q GateQuestion
	if err := readJSON(p, &q); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if q.GateID != id || !hex64.MatchString(q.EventID) {
		return nil, errors.New("gate question record is malformed")
	}
	return &q, nil
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

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	serr := d.Sync()
	return errors.Join(serr, d.Close())
}

// writeAtomic replaces path with data durably: a temp file in the same
// directory, fsync, rename, fsync of the directory.
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
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(dir)
}
