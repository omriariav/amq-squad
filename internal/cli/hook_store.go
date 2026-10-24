package cli

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Grants for guard (format provisional until 2b): the owner file
// ~/.amq-squad/grants.yaml (ignored, grants_reason_code owner_grants_insecure,
// when group- or world-writable), narrowed by the repository's squad.yaml,
// else defaults (12 seats, no protected paths). A strict YAML subset:
//
//	schema: 1
//	max_seats: 12          # session seat cap (plan 7.2)
//	default_branch: main   # else origin/HEAD, else main
//	protected_paths:       # ~/, absolute or repo-relative; a trailing / or a
//	  - .amq-squad/team-rules.md   # dir covers the tree; * ? [ are globs
//
// A bad owner file makes guard deny (grants_invalid). squad.yaml is read
// leniently: only top-level max_seats (lowers) and protected_paths (adds).
type hookGrants struct {
	Source         string   `json:"source"`
	ReasonCode     string   `json:"reason_code,omitempty"`
	MaxSeats       int      `json:"max_seats"`
	DefaultBranch  string   `json:"default_branch,omitempty"`
	ProtectedPaths []string `json:"protected_paths,omitempty"`
}

const hookDefaultMaxSeats = 12

// hookOwnerPath is a file in the owner's ~/.amq-squad (grants, trust store).
func hookOwnerPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", name)
}

func loadHookGrants(repo string) (hookGrants, error) {
	g := hookGrants{Source: "default", MaxSeats: hookDefaultMaxSeats}
	if p := hookOwnerPath("grants.yaml"); p != "" {
		if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o022 != 0 {
			g.ReasonCode = "owner_grants_insecure"
		} else if raw, err := os.ReadFile(p); err == nil {
			if g, err = parseHookGrants(string(raw), p, true); err != nil {
				return g, err
			}
		} else if !os.IsNotExist(err) {
			return hookGrants{Source: p}, err
		}
	}
	if repo == "" {
		return g, nil
	}
	p := filepath.Join(repo, "squad.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		return g, nil
	}
	squad, err := parseHookGrants(string(raw), p, false)
	if err != nil {
		return g, err
	}
	g.MaxSeats = min(g.MaxSeats, squad.MaxSeats)
	g.ProtectedPaths = append(g.ProtectedPaths, squad.ProtectedPaths...)
	g.Source += "+" + p
	return g, nil
}

func parseHookGrants(text, source string, strict bool) (hookGrants, error) {
	g := hookGrants{Source: source, MaxSeats: hookDefaultMaxSeats}
	inList := false
	for n, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, "#"); i >= 0 && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			line = line[:i]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		bad := fmt.Errorf("%s:%d: malformed line %q", source, n+1, trimmed)
		indented := line[0] == ' ' || line[0] == '\t'
		if item, ok := strings.CutPrefix(trimmed, "- "); ok && inList && indented {
			g.ProtectedPaths = append(g.ProtectedPaths, strings.Trim(strings.TrimSpace(item), `"'`))
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || indented {
			if !strict {
				continue // nested squad.yaml content
			}
			return g, bad
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		inList = false
		switch key {
		case "schema":
			if strict && value != "1" {
				return g, fmt.Errorf("%s: schema %q is not 1", source, value)
			}
		case "max_seats":
			v, err := strconv.Atoi(value)
			if err != nil || v < 1 {
				return g, bad
			}
			g.MaxSeats = v
		case "default_branch":
			if strict {
				g.DefaultBranch = value
			}
		case "protected_paths":
			if value != "" && value != "[]" {
				return g, bad
			}
			inList = value == ""
		default:
			if strict {
				return g, fmt.Errorf("%s:%d: unknown key %q", source, n+1, key)
			}
		}
	}
	return g, nil
}

// The gate store under the session root, in spike S1's layout (S1-report
// 1.1): gates/<id>.json is the core's gate record, answers/<id>.json holds an
// operatorauth "envelope". A gate is accepted only when that envelope verifies
// against the owner's ~/.amq-squad/authorization-trust.json (R32) and binds
// the gate id, action and the record's digest; thread text or an unverified
// file never counts. Read only; nothing writes the store yet (2a3-notes).
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
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".question.json") {
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
		if t, err := time.Parse(time.RFC3339Nano, g.NotAfter); err == nil && now.After(t) {
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
