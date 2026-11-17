package seatexec

// The launch codes seat-exec writes (S2-3, 3.3.0). Each reaches a task through
// scheduler.(LaunchFailure).Code: the pool's tasks wait on it and, when the
// launch fails the same way again, block on it. Each is on the state
// contract's block and wait lists (internal/statecontract reasons.go), and
// its value is the string literal it replaced, byte for byte.
const (
	ReasonArgvMissing          = "argv_missing"
	ReasonCodexApprovalMissing = "codex_approval_missing"
	ReasonCodexSandboxMissing  = "codex_sandbox_missing"
	ReasonDirWritableByOthers  = "dir_writable_by_others"
	ReasonEnvNeedsInvalid      = "env_needs_invalid"
	ReasonIdentityMismatch     = "identity_mismatch"
	ReasonIdentityProofFailed  = "identity_proof_failed"
	ReasonIdentityProofTimeout = "identity_proof_timeout"
	ReasonMailboxMissing       = "mailbox_missing"
	ReasonMkdirFailed          = "mkdir_failed"
	ReasonPathInvalid          = "path_invalid"
	ReasonProofExpired         = "proof_expired"
	ReasonProofRefused         = "proof_refused"
	ReasonProofTicketConsumed  = "proof_ticket_consumed"
	ReasonProofTicketMissing   = "proof_ticket_missing"
	ReasonProofUnacknowledged  = "proof_unacknowledged"
	ReasonProviderMissing      = "provider_missing"
	ReasonRecordInvalid        = "record_invalid"
	ReasonRecordMissing        = "record_missing"
	ReasonRoleMismatch         = "role_mismatch"
	ReasonRoleMissing          = "role_missing"
	ReasonRootInvalid          = "root_invalid"
	ReasonRootUnreadable       = "root_unreadable"
	ReasonRulesMismatch        = "rules_mismatch"
	ReasonRulesMissing         = "rules_missing"
	ReasonRuntimeMismatch      = "runtime_mismatch"
	ReasonSeatInvalid          = "seat_invalid"
	ReasonStatFailed           = "stat_failed"
	ReasonSymlinkInPath        = "symlink_in_path"
	ReasonWriteFailed          = "write_failed"
	ReasonWrongOwner           = "wrong_owner"
)
