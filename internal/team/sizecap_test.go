package team

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// squad.yaml can lower the size cap's file half (merge_size_cap_files,
// 2b-2 milestone B with 3-2); it parses and round-trips.
func TestSquadSizeCapFilesRoundTrips(t *testing.T) {
	text := ExampleSquad + "merge_size_cap_files: 3\n"
	s, err := ParseSquad(text, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if s.Grants.MergeSizeCapFiles != 3 {
		t.Fatalf("narrowing = %+v", s.Grants)
	}
	if !strings.Contains(Render(s), "merge_size_cap_files: 3\n") {
		t.Fatalf("render lacks the file cap:\n%s", Render(s))
	}
	again, err := ParseSquad(Render(s), "render", grants.Defaults())
	if err != nil || !reflect.DeepEqual(again, s) {
		t.Fatalf("round trip: %v", err)
	}
}
