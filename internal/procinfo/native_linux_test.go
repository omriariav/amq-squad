//go:build linux

package procinfo

import (
	"strings"
	"testing"
)

func TestProcessStartTicksFromStatUsesField22AfterComplexComm(t *testing.T) {
	fields := []string{"S"}
	for i := 4; i <= 21; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, "12345")
	stat := "4242 (agent (review) worker) " + strings.Join(fields, " ")
	if got, ok := processStartTicksFromStat(stat); !ok || got != 12345 {
		t.Fatalf("processStartTicksFromStat = %d, %v; want 12345, true", got, ok)
	}
}

func TestBootTimeFromProcStat(t *testing.T) {
	if got, ok := bootTimeFromProcStat("cpu 1 2 3\nbtime 1700000000\n"); !ok || got != 1700000000 {
		t.Fatalf("bootTimeFromProcStat = %d, %v", got, ok)
	}
}
