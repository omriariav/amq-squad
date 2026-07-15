package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func selfOperatorTeam() Team {
	op := DefaultOperator()
	op.InteractionMode = OperatorInteractionSelfOperator
	op.SelfOperator = &SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 1, Sessions: map[string]SelfOperatorSessionPolicy{
		"s": {Enabled: true, AllowedGateKinds: []string{"merge"}},
	}}
	return Team{Operator: &op, Orchestrated: true, Lead: "cto", Members: []Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}, {Role: "qa", Handle: "qa", Binary: "codex", Session: "s"}}}
}

func TestSelfOperatorValidationAndExactSession(t *testing.T) {
	team := selfOperatorTeam()
	if err := Validate(team); err != nil {
		t.Fatal(err)
	}
	if !EffectiveSelfOperator(team, "s").Enabled || EffectiveSelfOperator(team, "other").Enabled {
		t.Fatalf("exact session scope failed: s=%+v other=%+v", EffectiveSelfOperator(team, "s"), EffectiveSelfOperator(team, "other"))
	}
	team.Operator.SelfOperator.Sessions["s"] = SelfOperatorSessionPolicy{Enabled: true, Paused: true, AllowedGateKinds: []string{"merge"}}
	if EffectiveSelfOperator(team, "s").Enabled || !EffectiveSelfOperator(team, "s").Paused {
		t.Fatalf("pause ineffective: %+v", EffectiveSelfOperator(team, "s"))
	}
}

func TestSelfOperatorValidationRejectsLeadAllowlistAndUnknownFields(t *testing.T) {
	for _, mutate := range []func(*Team){
		func(team *Team) { team.Lead = "qa" },
		func(team *Team) { team.Operator.SelfOperator.Sessions["s"] = SelfOperatorSessionPolicy{Enabled: true} },
		func(team *Team) {
			team.Operator.SelfOperator.Sessions["s"] = SelfOperatorSessionPolicy{Enabled: true, AllowedGateKinds: []string{"release"}}
		},
	} {
		team := selfOperatorTeam()
		mutate(&team)
		if err := Validate(team); err == nil {
			t.Fatalf("invalid self operator accepted: %+v", team.Operator.SelfOperator)
		}
	}
	var policy SelfOperatorPolicy
	err := json.Unmarshal([]byte(`{"lead_role":"cto","policy_revision":1,"sessions":{},"allow_self_merge":true}`), &policy)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestSelfOperatorValidationRejectsImmutableHumanOnlySpawn(t *testing.T) {
	team := selfOperatorTeam()
	team.Operator.SelfOperator.Sessions["s"] = SelfOperatorSessionPolicy{Enabled: true, AllowedGateKinds: []string{"spawn"}}
	err := Validate(team)
	if err == nil || !strings.Contains(err.Error(), `gate kind "spawn" is immutable human-only`) {
		t.Fatalf("spawn self-operator allowlist error = %v", err)
	}
}

func TestReadSerializedSelfOperatorConfigRejectsSpawnAcrossSchemas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema int
	}{
		{name: "legacy-backward-readable-schema-1", schema: 1},
		{name: "current-schema", schema: SchemaVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
				t.Fatal(err)
			}
			raw := fmt.Sprintf(`{
  "schema": %d,
  "orchestrated": true,
  "lead": "cto",
  "operator": {
    "enabled": true,
    "interaction_mode": "self_operator",
    "self_operator": {
      "lead_role": "cto",
      "policy_revision": 1,
      "sessions": {"s": {"enabled": true, "allowed_gate_kinds": ["spawn"]}}
    }
  },
  "members": [{"role":"cto","binary":"codex","handle":"cto","session":"s"}]
}`, tc.schema)
			if err := os.WriteFile(Path(dir), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Read(dir)
			if err == nil || !strings.Contains(err.Error(), `gate kind "spawn" is immutable human-only`) {
				t.Fatalf("serialized schema %d spawn policy error = %v", tc.schema, err)
			}
			if strings.Contains(err.Error(), "parse ") || !strings.Contains(err.Error(), "validate ") {
				t.Fatalf("schema %d did not remain backward-readable through validation: %v", tc.schema, err)
			}
		})
	}
}

func TestSelfOperatorPolicyHashSecurityMatrix(t *testing.T) {
	base := selfOperatorTeam()
	want := SelfOperatorPolicyHash(base, "s")
	notificationOnly := base
	op := *base.Operator
	notificationOnly.Operator = &op
	notificationOnly.Operator.Notifications = &OperatorNotificationPolicy{Enabled: true, DeliverySemantics: "attention_only", Sinks: []OperatorNotificationSinkConfig{{ID: "desktop", Type: "desktop"}}}
	if got := SelfOperatorPolicyHash(notificationOnly, "s"); got != want {
		t.Fatalf("notification-only mutation changed hash: %s != %s", got, want)
	}
	mutations := map[string]func(*Team){
		"mode":        func(v *Team) { v.Operator.InteractionMode = OperatorInteractionSeparateTerminal },
		"operator":    func(v *Team) { v.Operator.Handle = "human" },
		"lead role":   func(v *Team) { v.Operator.SelfOperator.LeadRole = "qa" },
		"lead handle": func(v *Team) { v.Members[0].Handle = "chief" },
		"revision":    func(v *Team) { v.Operator.SelfOperator.PolicyRevision++ },
		"enabled": func(v *Team) {
			e := v.Operator.SelfOperator.Sessions["s"]
			e.Enabled = false
			v.Operator.SelfOperator.Sessions["s"] = e
		},
		"paused": func(v *Team) {
			e := v.Operator.SelfOperator.Sessions["s"]
			e.Paused = true
			v.Operator.SelfOperator.Sessions["s"] = e
		},
		"allowlist": func(v *Team) {
			e := v.Operator.SelfOperator.Sessions["s"]
			e.AllowedGateKinds = []string{"feature_branch_push"}
			v.Operator.SelfOperator.Sessions["s"] = e
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			v := selfOperatorTeam()
			mutate(&v)
			if got := SelfOperatorPolicyHash(v, "s"); got == want {
				t.Fatalf("security mutation did not change hash")
			}
		})
	}
}
