package mergetrain

import (
	"os"
	"testing"
)

// Credential custody: an owner environment carrying every GitHub and release
// credential name reaches a seat with none of them.
func TestSeatEnvironmentCarriesNoForgeCredential(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/Users/o"}
	for _, n := range ForgeCredentialNames {
		env = append(env, n+"=x")
	}
	if leaks := SeatCredentialLeaks(env); len(leaks) != 0 {
		t.Fatalf("seat environment carries %v", leaks)
	}
	// and this host's real environment (the 3-2 measurement): names only
	if leaks := SeatCredentialLeaks(os.Environ()); len(leaks) != 0 {
		t.Fatalf("this host's seat environment carries %v", leaks)
	}
}
