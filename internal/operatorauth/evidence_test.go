package operatorauth

import (
	"strings"
	"testing"
)

func TestStrictEvidenceRejectsUnknownFields(t *testing.T) {
	var approval ApprovalContext
	_, _, err := DecodeStrictEvidence(strings.NewReader(`{"schema_version":1,"source":"human","unknown":true}`), &approval)
	if err == nil {
		t.Fatal("unknown evidence field accepted")
	}
}
