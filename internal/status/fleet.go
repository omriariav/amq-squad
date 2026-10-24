package status

import (
	"encoding/json"
	"time"
)

// The fleet form of the contract (plan 16.3, briefs ruling 16):
// `amq-squad status --fleet --json` is one document that wraps one schema 1
// status document per squad of ~/.amq-squad/fleet.yaml, unchanged. It is the
// only fleet input for amq-noc (R44). The single-squad document carries no
// kind; a reader treats a document with squads as a fleet and anything else
// as one squad (IsFleet). testdata/status-fleet-v1.golden.json is the
// reference; its first squad is the single-squad golden with lead_handle
// null.

// KindFleet is the kind of the fleet document.
const KindFleet = "fleet"

// Fleet is the whole `status --fleet --json` document.
type Fleet struct {
	SchemaVersion   int       `json:"schema_version"`
	Kind            string    `json:"kind"`
	Producer        string    `json:"producer"`
	ProducerVersion string    `json:"producer_version"`
	GeneratedAt     time.Time `json:"generated_at"`
	Squads          []Status  `json:"squads"`
	// Waiting are squads the mayor declined or deferred for the fleet cap;
	// they have no squad document yet (additive to the ruling 16 shape).
	Waiting []FleetWaiting `json:"waiting"`
}

// WaitReasonFleetCap is the wait reason of a squad held back by the fleet
// grant's max_squads or cost_per_day_usd.
const WaitReasonFleetCap = "fleet_cap"

// FleetWaiting is one squad that is not up because of the fleet cap.
type FleetWaiting struct {
	Squad       string    `json:"squad"`
	Repository  string    `json:"repository"`
	ProjectPath string    `json:"project_path"`
	WaitReason  string    `json:"wait_reason"` // fleet_cap
	Cap         string    `json:"cap"`         // max_squads | cost_per_day
	Since       time.Time `json:"since"`
	Detail      string    `json:"detail"`
}

// BuildFleet wraps squad documents in the fleet header. squads and waiting
// are never nil in the output.
func BuildFleet(version string, now time.Time, squads []Status, waiting []FleetWaiting) Fleet {
	return Fleet{SchemaVersion: SchemaVersion, Kind: KindFleet, Producer: "amq-squad", ProducerVersion: version,
		GeneratedAt: now.UTC(), Squads: append([]Status{}, squads...), Waiting: append([]FleetWaiting{}, waiting...)}
}

// IsFleet reports whether raw is a fleet document: an object with a squads
// key. Anything else is one squad.
func IsFleet(raw []byte) bool {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	_, ok := probe["squads"]
	return ok
}
