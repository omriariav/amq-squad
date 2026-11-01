package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
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
// digest. Thread text or an unverified answer file never counts. The core
// writes the store (internal/gatestore, brief 3-1); this reader is unchanged
// in contract and adds one state: a gate with a deny record
// (denials/<id>.json) is denied, never open or accepted.
type hookGate struct {
	GateID   string `json:"gate_id"`
	Class    string `json:"class"` // the operatorauth action, e.g. tag, external_send, spawn
	Digest   string `json:"digest"`
	NotAfter string `json:"not_after,omitempty"`
	RaisedBy string `json:"raised_by,omitempty"`
	State    string `json:"state"` // open | accepted | denied | expired
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
	store := gatestore.Store{Root: root}
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
		if gatestore.LegacyKind(g.Class) {
			continue // a kind the catalog no longer carries (brief 31-1); status names it
		}
		g.State = "open"
		loadTrust := func() operatorauth.AuthorizationTrustStore {
			if trust == nil {
				trust = &operatorauth.AuthorizationTrustStore{}
				if p := hookOwnerPath("authorization-trust.json"); p != "" {
					*trust, _ = operatorauth.LoadAuthorizationTrustStore(p)
				}
			}
			return *trust
		}
		// The recovered decision, the one reader rule the core and the
		// verifier use (gatestore.Store.Decision: a deny record or denied mark
		// wins), then the answer's envelope against the owner's trust store.
		switch store.Decision(g.GateID) {
		case gatestore.StateDenied:
			g.State = "denied"
		case gatestore.StateAccepted:
			if ans, err := os.ReadFile(hookExtPath(root, "answers", name)); err == nil && hookAnswerVerified(ans, g, loadTrust()) {
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
	if p.Gate != "gate/"+g.GateID || p.Action != g.Class || p.Target != g.Digest {
		return false
	}
	return operatorauth.ValidateAuthorizationPayload(p) == nil && operatorauth.VerifyAuthorizationEnvelope(f.Envelope, trust) == nil
}
