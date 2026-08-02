package cli

import "fmt"

// Bootstrap drift evidence for the readiness row (#598 root cause 4).
//
// The row used to read:
//
//	evidence: generated bootstrap differs from accepted preview
//	fix:      review the bootstrap diff and approve preparation again
//
// and no flag, artifact, or verbose mode printed that diff. That is not a
// missing print statement. The accepted bootstrap TEXT is never persisted:
// preparation records only manifest.BootstrapDigests[role] and discards the
// prompt. There is nothing on disk to diff against, so the remedy named an
// action that is impossible rather than merely unimplemented -- and the same
// gap rules out the "at minimum the differing field names" fallback, which
// needs the identical accepted render.
//
// Persisting the accepted preview per role is the real fix and it belongs with
// the other preparation-surface work in #609, because it has to be declared in
// the preparation proposal's MutationPaths and covered by the transaction's
// rollback. What is fixed HERE is the lie: the row now states everything that
// is actually known, and names a remedy the CLI can actually perform.

// preparedBootstrapDriftFix is the remedy for a drifted bootstrap row.
//
// It deliberately does NOT promise a diff. Re-running preparation is an action
// that exists today and does resolve the drift; the field-level comparison
// arrives with the persisted preview in #609. A remedy that overstates what the
// tool can do is the defect this row is being fixed for, so it must not be
// reintroduced in the fix text itself.
const preparedBootstrapDriftFix = "re-run preparation for this exact namespace and accept it again (`amq-squad run start ... --prepare`); a field-level accepted-vs-generated diff is not available yet because the accepted preview is not persisted (#609)"

// preparedBootstrapDriftEvidence states everything known about a bootstrap
// digest mismatch at the moment it is detected.
//
// Naming the namespace, role, generation and BOTH digests is what makes the row
// actionable without source access: the operator can tell which member drifted,
// in which accepted generation, and can compare the recorded digest against a
// re-rendered one after re-preparing. Reporting only "differs" forced the
// operator to reproduce the pane command by hand to learn anything at all,
// which is exactly how the fresh-namespace brick stayed undiagnosed.
func preparedBootstrapDriftEvidence(profile, session, role, generation, accepted, generated string) string {
	return fmt.Sprintf(
		"generated bootstrap differs from the accepted preview: namespace=%s/%s role=%s generation=%s accepted_sha256=%s generated_sha256=%s",
		profile, session, role, generation, accepted, generated,
	)
}
