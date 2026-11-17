package seatlaunch

// The launch codes the launcher and its proof server writes (S2-3, 3.3.0). Each reaches a task through
// scheduler.(LaunchFailure).Code: the pool's tasks wait on it and, when the
// launch fails the same way again, block on it. Each is on the state
// contract's block and wait lists (internal/statecontract reasons.go), and
// its value is the string literal it replaced, byte for byte.
const (
	ReasonApplyAmbiguous     = "apply_ambiguous"
	ReasonApplyFailed        = "apply_failed"
	ReasonDecisionsPending   = "decisions_pending"
	ReasonIdentityForged     = "identity_forged"
	ReasonNoProof            = "no_proof"
	ReasonNotObserved        = "not_observed"
	ReasonPrepareRefused     = "prepare_refused"
	ReasonProofLedgerFailed  = "proof_ledger_failed"
	ReasonProofMalformed     = "proof_malformed"
	ReasonProofMarkerFailed  = "proof_marker_failed"
	ReasonProofOriginUnknown = "proof_origin_unknown"
	ReasonProofTokenInvalid  = "proof_token_invalid"
	ReasonProofUnknown       = "proof_unknown"
	ReasonRefused            = "refused"
	ReasonResourcePresent    = "resource_present"
	ReasonSeatNotUp          = "seat_not_up"
)

// The proof server's codes that never reach a task (S2-3): a forged proof's
// alarm code, which the seat's refusal carries in its detail under
// ReasonIdentityForged, and the dropped-proof log's code. The state contract
// lists them outside the launch family (statecontract.LaunchReasons).
const (
	ReasonProofConnectionsFull = "proof_connections_full"
	ReasonProofOrigin          = "proof_origin"
	ReasonProofReplayed        = "proof_replayed"
)
