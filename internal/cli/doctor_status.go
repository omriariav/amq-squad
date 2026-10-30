package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
)

// statusContractCheck is the status contract self-check of plan 16.2
// against 2b-1's schema 1: this build's types decode the golden fixture
// strictly, re-encode it additively both ways (no field lost, none the
// fixture lacks), and a document this build produces is schema 1 and
// additive against the fixture. golden is status.Golden; tests pass a
// changed copy.
func statusContractCheck(golden []byte, projectDir string, now time.Time) doctorCheck {
	const name = "status contract"
	fail := func(format string, args ...any) doctorCheck {
		return doctorCheck{Name: name, Status: doctorFail, Detail: fmt.Sprintf(format, args...)}
	}
	dec := json.NewDecoder(bytes.NewReader(golden))
	dec.DisallowUnknownFields()
	var fixture status.Status
	if err := dec.Decode(&fixture); err != nil {
		return fail("the golden fixture does not decode into this build's status types: %v", err)
	}
	reencoded, err := json.Marshal(fixture)
	if err != nil {
		return fail("re-encode: %v", err)
	}
	if err := status.CheckAdditive(golden, reencoded); err != nil {
		return fail("this build drops a schema %d field: %v", status.SchemaVersion, err)
	}
	if err := status.CheckAdditive(reencoded, golden); err != nil {
		return fail("this build emits a field the fixture lacks: %v", err)
	}
	built, err := json.Marshal(status.Build(status.Input{Now: now, ProjectPath: projectDir, ProducerVersion: "doctor"}))
	if err != nil {
		return fail("build: %v", err)
	}
	var head struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(built, &head); err != nil || head.SchemaVersion != status.SchemaVersion || fixture.SchemaVersion != status.SchemaVersion {
		return fail("schema_version %d (fixture %d), want %d", head.SchemaVersion, fixture.SchemaVersion, status.SchemaVersion)
	}
	if err := status.CheckAdditive(golden, built); err != nil {
		return fail("a produced document is not additive against the fixture: %v", err)
	}
	return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("schema %d: the golden fixture round-trips through this build and a produced document is additive against it", status.SchemaVersion)}
}
