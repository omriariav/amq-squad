package cli

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// hookOwnerPath is a file in the owner's ~/.amq-squad (grants, trust store).
func hookOwnerPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", name)
}

// loadHookGrants is the guard's grant: grants schema 1 (internal/grants), the
// owner file ~/.amq-squad/grants.yaml narrowed by <repo>/squad.yaml, else the
// defaults. An owner file that does not parse is grants_invalid; one that is
// present but untrusted fails closed (owner_grants_insecure).
func loadHookGrants(repo string) (grants.Resolved, error) {
	return grants.LoadFrom(hookOwnerPath("grants.yaml"), repo)
}

// The gate store under the session root, in the layout spike S1 set
// (docs/v3-spike/S1-report.md 1.1): gates/<id>.json is the core's immutable
// gate record and answers/<id>.json the answer file whose "envelope" is an
// operatorauth envelope. A gate counts as accepted only when that envelope
// verifies against the owner's trust store ~/.amq-squad/authorization-trust.json
// (R32) and binds the gate id, the action class and the record's target
// digest. Thread text or an unverified answer file never counts. Read only
// here; nothing in the binary writes the store yet (2a3-notes).
type hookGate struct {
	GateID   string `json:"gate_id"`
	Class    string `json:"class"` // the operatorauth action, e.g. tag, external_send, spawn
	Digest   string `json:"digest"`
	NotAfter string `json:"not_after,omitempty"`
	RaisedBy string `json:"raised_by,omitempty"`
	State    string `json:"state"` // open | accepted | expired
}

func readHookGates(root string, now time.Time) ([]hookGate, error) {
	dir := hookExtPath(root, "gates")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var trust *operatorauth.AuthorizationTrustStore
	var gates []hookGate
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".question.json") {
			continue
		}
		var g hookGate
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || json.Unmarshal(raw, &g) != nil || g.GateID+".json" != name || g.Class == "" || g.Digest == "" {
			continue // not a gate record; the core's validator owns the error
		}
		g.State = "open"
		if ans, err := os.ReadFile(hookExtPath(root, "answers", name)); err == nil {
			if trust == nil {
				trust = &operatorauth.AuthorizationTrustStore{}
				if p := hookOwnerPath("authorization-trust.json"); p != "" {
					*trust, _ = operatorauth.LoadAuthorizationTrustStore(p)
				}
			}
			if hookAnswerVerified(ans, g, *trust) {
				g.State = "accepted"
			}
		}
		// Acceptance is terminal: not_after governs only an unanswered gate; the
		// envelope's own window, checked by VerifyAuthorizationEnvelope, is the
		// only time check on an answer.
		if t, err := time.Parse(time.RFC3339Nano, g.NotAfter); g.State == "open" && err == nil && now.After(t) {
			g.State = "expired"
		}
		gates = append(gates, g)
	}
	sort.Slice(gates, func(i, j int) bool { return gates[i].GateID < gates[j].GateID })
	return gates, nil
}

func hookAnswerVerified(raw []byte, g hookGate, trust operatorauth.AuthorizationTrustStore) bool {
	var f struct {
		Envelope operatorauth.AuthorizationEnvelope `json:"envelope"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return false
	}
	p := f.Envelope.Payload
	return p.Gate == "gate/"+g.GateID && p.Action == g.Class && p.Target == g.Digest &&
		operatorauth.ValidateAuthorizationPayload(p) == nil && operatorauth.VerifyAuthorizationEnvelope(f.Envelope, trust) == nil
}

// hookGateDigest is the S1 target digest for a non-merge class: sha256 over
// the length-prefixed domain, class (here the operatorauth action) and
// subject_sha256, where the subject is the guard's target string.
func hookGateDigest(class, target string) string {
	subject := sha256.Sum256([]byte(target))
	var b []byte
	for _, v := range []string{"amq-squad-gate-target-v1", class, hex.EncodeToString(subject[:])} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(v)))
		b = append(b, v...)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
