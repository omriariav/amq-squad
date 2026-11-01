# t35: committed symlink destination policy

The commit step now evaluates every added or retargeted `120000` entry through
the exact-path resolver. An otherwise ordinary changed link is refused with
`link_target_refused`, routed to the lead, when its destination is outside the
repository, protected, a managed name, dangling, or cyclic. The refusal detail
names both the link and the destination. A link to an ordinary declared file
inside the repository remains allowed.

The verifier checkout now reads all link blobs before creating any link. It
resolves complete link chains, including links in parent components, against
the snapshot tree and applies the same protected and managed-name policy.
Refused links are never materialized in the verifier worktree and produce the
same lead-routed policy reason.

Evidence:

- `go test ./internal/seatcommit -run 'TestCommitJudgesChangedSymlinksByDestination|TestManagedPathMustStayARegularFile|TestVerifierCheckoutNeverMaterializesARefusedSymlink' -count=1`
- `go test ./internal/seatcommit -count=1`

Both commands passed on 2026-11-02.
