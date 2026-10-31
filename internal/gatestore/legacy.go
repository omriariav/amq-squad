package gatestore

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

// Legacy gate records (brief 31-1): a record of a kind the typed-gate
// catalog no longer carries, such as the quarantine_clear gate of the
// deleted seat clear (the owner's signed scribe clear of 2026-10-31 is one),
// no longer validates. Readers list it as legacy and skip it with one line
// naming the kind and the gate id; it is never a refusal.

// LegacyKind reports whether a record's action (its class field) is one the
// typed-gate catalog no longer carries.
func LegacyKind(class string) bool { return class != "" && gatetarget.Class(class) == "" }

// Legacy is the kind of gateID's record and true when that kind is legacy;
// "" and false for a known kind or a file that is no gate record (the
// validator owns that error).
func (s Store) Legacy(gateID string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(s.Dir("gates"), gateID+".json"))
	if err != nil {
		return "", false
	}
	var r struct {
		GateID string `json:"gate_id"`
		Class  string `json:"class"`
	}
	if json.Unmarshal(raw, &r) != nil || r.GateID != gateID || !LegacyKind(r.Class) {
		return "", false
	}
	return r.Class, true
}

// LegacyLine is the one line a reader prints for a skipped legacy record.
func LegacyLine(gateID, kind string) string {
	return "gate " + gateID + " is a legacy " + kind + " gate (a kind this build no longer carries); skipped"
}
