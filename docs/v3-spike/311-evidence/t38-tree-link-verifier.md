# t38 tree-link verifier and review fixes

The verifier now preflights changed links through the shared Git-object
resolver before creating any symlink. Unchanged base links retain the previous
checkout behavior. Both commit and verifier callers accept the object-only
source, with a fake that panics when given a filesystem path.

The changed-link filter now includes regular-file-to-link mode flips even when
the blob ID is unchanged. Resolution uses the named 40-link bound as its only
cycle/chain limit, so finite revisits resolve while cycles and long chains fail.
This closes the mode-flip gap shipped in #850.

Evidence: focused t38 rows and `go test ./internal/seatcommit -count=1` passed.
