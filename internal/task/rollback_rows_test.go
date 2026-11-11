package task

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func taskRollbackKeys(typ reflect.Type) map[string]bool {
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

func TestRollbackVerdictAndSnapshotCrossVersionProbe(t *testing.T) {
	root := os.Getenv("AMQSQ_P13_ROOT")
	mode := os.Getenv("AMQSQ_P13_MODE")
	if root == "" || mode == "" {
		t.Skip("set AMQSQ_P13_ROOT and AMQSQ_P13_MODE")
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "write" {
		now := time.Unix(20, 0).UTC()
		item, err := store.Add(AddInput{Title: "P13 sealed rows", Class: "implement"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Grant(item.ID, "qa", nil, time.Hour, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		digest := CommandDigest("p13 verifier")
		if _, err := store.RecordSnapshot(item.ID, "p13-tree", digest, now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordVerdict(item.ID, Verdict{Verifier: VerifierTests, Result: VerdictPass, Source: "P13", Tree: "p13-tree", CommandDigest: digest}, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("P13 task rows: count=%d err=%v", len(list), err)
	}
	if !store.SnapshotSealed(list[0]) {
		t.Fatal("P13 snapshot row refused or raised a tamper alarm")
	}
	verdict, why := store.SealedVerdict(list[0])
	if verdict == nil || why != "" {
		t.Fatalf("P13 verdict row refused or raised a tamper alarm: %s", why)
	}
	t.Logf("P13 verdict and snapshot accepted: task=%s result=%s", list[0].ID, verdict.Result)
}

func TestRC1TaskSealedRowKeyGuard(t *testing.T) {
	// v3.1.2 and v3.2.0-rc.1 have the same seal bodies as this base:
	// internal/task/verdict.go:65-90 at 3966afb7.
	want := map[string]map[string]bool{
		"verdictBody": {
			"task": true, "attempt": true, "holder": true, "verifier": true,
			"result": true, "source": true, "detail": true, "tree": true,
			"command_digest": true, "prev_command_digest": true, "at": true,
		},
		"snapshotBody": {
			"task": true, "attempt": true, "holder": true, "tree": true, "command_digest": true,
		},
	}
	types := map[string]reflect.Type{"verdictBody": reflect.TypeOf(verdictBody{}), "snapshotBody": reflect.TypeOf(snapshotBody{})}
	for typeName, typ := range types {
		for key := range taskRollbackKeys(typ) {
			if !want[typeName][key] {
				t.Errorf("add a frozen rc.1 row for %s.%s and list it here", typeName, key)
			}
		}
	}
}
