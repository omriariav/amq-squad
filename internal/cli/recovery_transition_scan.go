package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// recoveryTransitionBlocker is one reason this pause cannot be claimed. It always carries an
// operator-runnable next step: a refusal without one is a dead end, and #498's whole complaint
// was a lifecycle step that stalled on a human who did not know what to type.
type recoveryTransitionBlocker struct {
	Path   string
	Reason string
}

func (b recoveryTransitionBlocker) describe() string {
	return b.Reason + " (" + b.Path + ")"
}

func (b recoveryTransitionBlocker) recovery() string {
	return "inspect " + b.Path + " and either consume or clear that transition deliberately; automatic resume will not retry an indeterminate pause"
}

// pauseLedgerScan is the kind-agnostic result for ONE pause.
type pauseLedgerScan struct {
	Blockers []recoveryTransitionBlocker
	// Ours is the exact path of this executor's own reservation, set only by the post-reserve
	// rescan so step 6.5 can exclude it by EXACT PATH EQUALITY and nothing looser.
	Ours string
}

func (s pauseLedgerScan) blocking() *recoveryTransitionBlocker {
	for i := range s.Blockers {
		if s.Blockers[i].Path != s.Ours {
			return &s.Blockers[i]
		}
	}
	return nil
}

func (s pauseLedgerScan) competingWith(ourPath string) *recoveryTransitionBlocker {
	s.Ours = ourPath
	return s.blocking()
}

// scanRecoveryTransitionsForPause enumerates every recovery transition for ONE pause, across
// BOTH derivations and ALL kinds, and reports what blocks.
//
// No prefix literal appears here. Today's kind-blindness exists because the scan carried its own
// prefix filter (resume_goal.go:790) separate from the path builder (:345) -- two filters, one of
// which nobody updated when a kind was added. Everything goes through the one parser.
//
// FAILS CLOSED EVERYWHERE. The ledger is the only thing standing between one audited /goal
// resume and two, so every ambiguity blocks:
//   - a reservation with no .consumed.json     -> indeterminate
//   - a companion that cannot be read/parsed   -> cannot prove consumption
//   - a name that fails to parse               -> a reservation we cannot understand
//   - a body whose kind disagrees with its name -> ambiguous evidence
//   - an ORPHAN companion for this pause       -> a reservation existed and is gone
//   - any stat error other than not-exist      -> cannot prove anything
//   - a reservation that IS consumed            -> a delivery may have reached the pane
//
// THERE IS NO NON-BLOCKING LIFECYCLE STATE for a matching current or exact-matching legacy key.
// Reserved, bound and consumed all refuse. An earlier version of this comment said consumed was
// the one ignorable state, which contradicted the ratified checklist AND the confirmed sequence,
// and would have made a COMPLETED redelivery invisible to supervision resume -- one delivered
// resume authorising another. Only records proven to belong to a DIFFERENT current claim key or a
// different legacy attempt+binding are outside this pause.
func scanRecoveryTransitionsForPause(dir, claimKey, legacyKey string) (pauseLedgerScan, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Refusal, INCLUDING not-exist. An absent directory is the normal fresh case, but it is
		// also exactly what a wrong project/profile/session triple produces, and the error
		// cannot distinguish them. Unlike #577's fresh-workstream case there is no successful
		// observation to prove absence with, so none is manufactured. The resolved path is named
		// so the operator can see which namespace was inspected.
		return pauseLedgerScan{}, fmt.Errorf("cannot read the recovery transition directory %s: %w", dir, err)
	}

	var scan pauseLedgerScan
	// Companions are collected first so orphan detection can ask "does a reservation exist for
	// this companion?" after every name is known. Doing it in one pass would depend on
	// directory order, which is not guaranteed.
	reservations := map[string]string{} // claimKey+kind -> path
	companions := map[string]string{}   // reservation base name -> companion path

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if base, ok := companionReservationBase(name); ok {
			if companionBelongsToPause(base, claimKey, legacyKey) {
				companions[base] = filepath.Join(dir, name)
			}
			continue
		}
		parsed, recognition := recognizeRecoveryTransitionName(name)
		switch recognition {
		case recoveryNameNotATransition:
			// Ordinary <attempt>.json and <attempt>.claim.json live in this directory too.
			// Blocking on those would let normal attempt files wedge all recovery permanently --
			// amendment 1 overcorrected into an outage.
			continue
		case recoveryNameMalformed:
			// TRANSITION-LIKE but malformed, unknown-kind or missing structure. This blocks
			// WITHOUT requiring a key match, and my earlier key-match heuristic was FAIL-OPEN:
			// if corruption destroyed the key, requiring the key to be present proves nothing
			// about ownership while quietly permitting delivery. Ambiguous ledger integrity is
			// an operator recovery condition, and it may wedge the namespace -- accepted, per
			// ruling, because the alternative is delivering against evidence we cannot read.
			scan.Blockers = append(scan.Blockers, recoveryTransitionBlocker{
				Path:   filepath.Join(dir, name),
				Reason: "a recovery-transition-like file is malformed, unknown-kind or missing structure, so no claim here can be honoured or dismissed",
			})
			continue
		}
		if !parsedBelongsToPause(parsed, claimKey, legacyKey) {
			continue
		}
		reservations[string(parsed.Kind)+":"+parsed.ClaimKey] = filepath.Join(dir, name)
	}

	// AMENDMENT 1: orphan companions block. A .consumed.json or .bound.json whose reservation is
	// ABSENT is evidence that a reservation existed and something removed it -- and nothing in
	// any code path deletes a reservation. That is tampering or partial state, not vacancy.
	for base, companionPath := range companions {
		if _, err := os.Stat(filepath.Join(dir, base+".json")); err != nil {
			if !os.IsNotExist(err) {
				scan.Blockers = append(scan.Blockers, recoveryTransitionBlocker{
					Path:   companionPath,
					Reason: "cannot stat the reservation this companion belongs to, so consumption cannot be proven",
				})
				continue
			}
			scan.Blockers = append(scan.Blockers, recoveryTransitionBlocker{
				Path:   companionPath,
				Reason: "ORPHAN companion: a transition companion exists for this pause but its reservation is gone, and nothing in this system deletes reservations",
			})
		}
	}

	for _, path := range reservations {
		if blocker := reservationBlocker(path); blocker != nil {
			scan.Blockers = append(scan.Blockers, *blocker)
		}
	}
	return scan, nil
}

// reservationBlocker decides whether ONE reservation blocks, reading its body and companion.
//
// AMENDMENT 2 / ruling (B): the body IS read. A name-trusting scan is shape-not-proof at exactly
// the spot this milestone keeps getting burned, and the cost is 0-3 file reads for one pause.
func reservationBlocker(path string) *recoveryTransitionBlocker {
	data, err := os.ReadFile(path)
	if err != nil {
		return &recoveryTransitionBlocker{Path: path, Reason: "cannot read this pause's reservation, so its claim state is unknown"}
	}
	var record struct {
		Kind         string `json:"recovery_kind"`
		TransitionID string `json:"transition_id"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return &recoveryTransitionBlocker{Path: path, Reason: "this pause's reservation does not parse, so its claim state is unknown"}
	}
	parsed, ok := parseRecoveryTransitionName(filepath.Base(path))
	if !ok {
		return &recoveryTransitionBlocker{Path: path, Reason: "reservation name no longer parses"}
	}
	// PREFIX-KIND vs RECORD-KIND must AGREE. A renamed file would otherwise silently change what
	// a reservation means, and disagreement is ambiguous evidence rather than a tie to break.
	// Legacy names carry no kind in the filename, so only current-derivation names are compared.
	if !parsed.Legacy && strings.TrimSpace(record.Kind) != string(parsed.Kind) {
		return &recoveryTransitionBlocker{
			Path:   path,
			Reason: fmt.Sprintf("reservation kind disagrees: filename says %q, record says %q", parsed.Kind, record.Kind),
		}
	}

	// RULED (#498 checklist section 2 + confirmed sequence step 3a): a matching reservation
	// BLOCKS in EVERY lifecycle state. There is no ignored state for this pause.
	//
	// My first version treated proven-consumed as non-blocking and called that "ignoring". It
	// was wrong against two ratified texts, and the semantics matter more than the wording: a
	// COMPLETED redelivery would have been INVISIBLE to supervision resume, which is exactly the
	// forbidden case -- one delivered resume authorising another. Consumed means a delivery may
	// have reached the pane. That is terminal ownership evidence about this pause, never
	// permission for a second automatic action.
	consumedPath := resumeGoalTransitionConsumedPath(path)
	if _, err := os.Stat(consumedPath); err == nil {
		return &recoveryTransitionBlocker{
			Path:   consumedPath,
			Reason: "a recovery transition for this pause is already CONSUMED: a delivery may have reached the pane, so a second automatic delivery is refused",
		}
	} else if !os.IsNotExist(err) {
		return &recoveryTransitionBlocker{Path: consumedPath, Reason: "cannot stat the consumption record, so delivery cannot be proven"}
	}
	return &recoveryTransitionBlocker{
		Path:   path,
		Reason: "a recovery transition for this pause is reserved and NOT consumed: delivery is indeterminate, so a second delivery is refused",
	}
}

// companionReservationBase maps a companion filename to the reservation base name it belongs to.
func companionReservationBase(name string) (string, bool) {
	for _, suffix := range []string{".consumed.json", ".bound.json"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix), true
		}
	}
	return "", false
}

func companionBelongsToPause(base, claimKey, legacyKey string) bool {
	return strings.Contains(base, claimKey) || (legacyKey != "" && strings.Contains(base, legacyKey))
}

func parsedBelongsToPause(parsed parsedRecoveryTransitionName, claimKey, legacyKey string) bool {
	if parsed.Legacy {
		// A legacy record for a DIFFERENT attempt is someone else's claim: directory-wide legacy
		// blocking would wedge every future pause behind any historical record.
		return legacyKey != "" && parsed.ClaimKey == legacyKey
	}
	return parsed.ClaimKey == claimKey
}
