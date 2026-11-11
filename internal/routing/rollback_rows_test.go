package routing

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

func routingRollbackKeys(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" {
			key = field.Name
		}
		if key != "-" {
			out[key] = true
		}
	}
	return out
}

func TestRollbackOutcomeCrossVersionProbe(t *testing.T) {
	root := os.Getenv("AMQSQ_P13_ROOT")
	mode := os.Getenv("AMQSQ_P13_MODE")
	if root == "" || mode == "" {
		t.Skip("set AMQSQ_P13_ROOT and AMQSQ_P13_MODE")
	}
	key, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "write" {
		row := Row{Schema: RowSchema, At: time.Unix(30, 0).UTC(), Task: "t1", Attempt: 1,
			Class: "plan", Policy: PolicyManual, Runtime: "codex", Outcome: "completed",
			VerifierResult: VerifierNoCheck, Seq: 1}
		if err := AppendRow(root, row.Sign(key.Key)); err != nil {
			t.Fatal(err)
		}
	}
	rows, skipped, err := ReadRows(OutcomePath(root))
	if err != nil || skipped != 0 || len(rows) != 1 {
		t.Fatalf("P13 outcome read: rows=%d skipped=%d err=%v", len(rows), skipped, err)
	}
	good, tampered := VerifyRows(rows, key.Key)
	if len(good) != 1 || len(tampered) != 0 {
		t.Fatalf("P13 outcome refused or raised a tamper alarm: good=%d tampered=%d", len(good), len(tampered))
	}
	t.Logf("P13 outcome accepted: task=%s policy=%s", good[0].Task, good[0].Policy)
}

func TestRC1RoutingOutcomeRowKeyGuard(t *testing.T) {
	// These are the authenticated JSON keys at both v3.1.2 and
	// v3.2.0-rc.1 (3966afb7), internal/routing/outcome.go:53-114.
	want := map[string]map[string]bool{
		"Skip": {"entry": true, "reason": true},
		"Features": {
			"declared_files": true, "size_band": true, "input_tokens": true,
			"sensitive": true, "no_tests": true, "privacy": true,
			"privacy_glob": true, "implementer_family": true, "unchecked": true,
		},
		"Row": {
			"schema": true, "at": true, "task": true, "attempt": true, "class": true,
			"features": true, "table_tier": true, "used_tier": true, "entry": true,
			"runtime": true, "skipped": true, "policy": true, "propensity": true,
			"exploration_share": true, "tokens_in": true, "tokens_out": true,
			"cost_usd": true, "usage_invalid": true, "cost_index": true, "wall_ms": true,
			"wait_ms": true, "wait_reasons": true, "outcome": true, "verifier_result": true,
			"review_findings": true, "consumed_smol_outputs": true, "holder": true,
			"granted_at": true, "ended_at": true, "seq": true, "prev": true, "tag": true,
		},
	}
	types := map[string]reflect.Type{"Skip": reflect.TypeOf(Skip{}), "Features": reflect.TypeOf(Features{}), "Row": reflect.TypeOf(Row{})}
	for typeName, typ := range types {
		for key := range routingRollbackKeys(typ) {
			if !want[typeName][key] {
				t.Errorf("add a frozen rc.1 row for %s.%s and list it here", typeName, key)
			}
		}
	}
}
