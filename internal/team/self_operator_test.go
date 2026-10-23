package team

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadSerializedSelfOperatorConfigIsRejectedAcrossSchemas: 3.0 (2a-2)
// removes the self_operator mode; a persisted v2 policy fails validation.
func TestReadSerializedSelfOperatorConfigIsRejectedAcrossSchemas(t *testing.T) {
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
  "members": [{"role":"cto","binary":"codex","handle":"cto","session":"s","actor_mode":"review"}]
}`, tc.schema)
			if err := os.WriteFile(Path(dir), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Read(dir)
			if err == nil || !strings.Contains(err.Error(), "removed in amq-squad 3.0") {
				t.Fatalf("serialized schema %d self_operator error = %v", tc.schema, err)
			}
			if strings.Contains(err.Error(), "parse ") || !strings.Contains(err.Error(), "validate ") {
				t.Fatalf("schema %d did not remain backward-readable through validation: %v", tc.schema, err)
			}
		})
	}
}
