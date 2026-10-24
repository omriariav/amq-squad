package cli

import (
	"fmt"
	"os"
	"strings"
)

// 2b-1: the v2 task-bound subverbs (run, show, list, recover, lookup) bound
// immutable command evidence to a v2 task through its profile/session
// namespace, its command-evidence links and its dispatch thread. The 3.0
// task store has none of those (docs/task-store-design.md), so the
// subverbs are cut with the v2 store; the hook ledger (`evidence ingest`)
// is the 3.0 evidence. 3-2 deletes this file and moves the redaction rule
// below into the ledger readers (brief 3-2, "Replaces").

func runEvidence(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad evidence - the seat's evidence ledger

Usage:
  amq-squad evidence ingest --me HANDLE < hook-payload.json

ingest appends one hook payload to <session root>/extensions/amq-squad/ledger/
HANDLE.jsonl and renews the seat's claimed task lease.
`)
		return nil
	}
	switch args[0] {
	case "ingest":
		return runEvidenceIngest(args[1:])
	default:
		return unknownSubcommandError("evidence", args[0], "ingest")
	}
}

// evidenceSecretName is the v2 redaction rule for environment names, kept
// for 3-2's ledger readers (brief 3-2 moves it there).
func evidenceSecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY", "API_KEY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
