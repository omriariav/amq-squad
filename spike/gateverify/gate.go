package main

// Gate rendering: the Go core, not a model, writes the text the owner reads.
// It carries the gate id, the action class, the target digest and the
// ordered PR numbers, and nothing else: no code, paths, branch names, repo
// names, commit messages or secrets. Everything the digest covers that the
// text does not show (repository, base SHA, head SHAs) is bound by the
// digest alone.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	gateRecordSchema   = "amq-squad-gate-spike/v1"
	targetDigestDomain = "amq-squad-gate-target-v1"
	// minDigestPrefix is the shortest digest prefix an answer may type: 64
	// bits, as the gate-transport report recommends (>=16 hex).
	minDigestPrefix = 16
	// answerSkew admits an owner clock up to 30 s behind the core, the same
	// slack upstream gives a Buzz approval (README "dated from 30 seconds
	// before the request").
	answerSkew = 30 * time.Second
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
// digest come from here, never from the answer.
type GateRecord struct {
	Schema          string `json:"schema"`
	GateID          string `json:"gate_id"`
	Class           string `json:"class"`
	Digest          string `json:"digest"`
	PRs             []int  `json:"prs"`
	Text            string `json:"text"`
	TextSHA256      string `json:"text_sha256"`
	IssuedAt        string `json:"issued_at"`
	NotBefore       string `json:"not_before"`
	NotAfter        string `json:"not_after"`
	QuestionEventID string `json:"question_event_id,omitempty"`
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
		if !repoPattern.MatchString(s.Repo) || strings.Trim(owner, ".") == "" || strings.Trim(name, ".") == "" || !gitSHAPattern.MatchString(s.BaseSHA) || len(s.PRs) == 0 || len(s.PRs) > 50 || s.SubjectSHA256 != "" {
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

// Render builds the gate record and its text at now.
func Render(s GateSpec, now time.Time) (GateRecord, error) {
	if err := s.validate(); err != nil {
		return GateRecord{}, err
	}
	digest := s.TargetDigest()
	prefix := strings.TrimPrefix(digest, "sha256:")[:minDigestPrefix]
	var prs []int
	var prText []string
	for _, pr := range s.PRs {
		prs = append(prs, pr.Number)
		prText = append(prText, "#"+strconv.Itoa(pr.Number))
	}
	if prs == nil {
		prs = []int{}
	}
	lines := []string{
		"amq-squad gate " + s.GateID,
		"class: " + s.Class,
		"digest: " + digest,
	}
	if len(prText) > 0 {
		lines = append(lines, "PRs in merge order: "+strings.Join(prText, " "))
	}
	lines = append(lines,
		"Answer with one line:",
		fmt.Sprintf("approve %s %s %s", s.GateID, s.Class, prefix),
		fmt.Sprintf("deny %s %s %s", s.GateID, s.Class, prefix),
	)
	text := strings.Join(lines, "\n")
	sum := sha256.Sum256([]byte(text))
	now = now.UTC().Truncate(time.Second)
	return GateRecord{
		Schema: gateRecordSchema, GateID: s.GateID, Class: s.Class, Digest: digest, PRs: prs,
		Text: text, TextSHA256: "sha256:" + hex.EncodeToString(sum[:]),
		IssuedAt:  now.Format(time.RFC3339),
		NotBefore: now.Add(-answerSkew).Format(time.RFC3339),
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
// Case is folded (a phone keyboard capitalizes the first word) and runs of
// whitespace are one separator; any other token, punctuation or a short
// prefix refuses. The tuple is the whole content: nothing else may ride
// along that a later reader could take for a second instruction.
func ParseAnswer(content string) (Answer, error) {
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

func (g GateRecord) window() (time.Time, time.Time, error) {
	nb, err := time.Parse(time.RFC3339, g.NotBefore)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("gate record not_before is invalid")
	}
	na, err := time.Parse(time.RFC3339, g.NotAfter)
	if err != nil || !na.After(nb) {
		return time.Time{}, time.Time{}, errors.New("gate record not_after is invalid")
	}
	return nb, na, nil
}

func (g GateRecord) validate() error {
	if g.Schema != gateRecordSchema || !gateIDPattern.MatchString(g.GateID) || !gateClasses[g.Class] ||
		!strings.HasPrefix(g.Digest, "sha256:") || !hex64.MatchString(strings.TrimPrefix(g.Digest, "sha256:")) {
		return errors.New("gate record is malformed")
	}
	sum := sha256.Sum256([]byte(g.Text))
	if g.TextSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		return errors.New("gate record text digest does not match its text")
	}
	if g.QuestionEventID != "" && !hex64.MatchString(g.QuestionEventID) {
		return errors.New("gate record question event id is malformed")
	}
	_, _, err := g.window()
	return err
}
