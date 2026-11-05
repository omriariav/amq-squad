# t37 tree-link policy

The commit step resolves added or retargeted links from the immutable Git tree
through the object-only resolver in `treelink.go`. It walks raw components,
follows at most 40 links, and never judges unchanged links.

Evidence: focused tree-link and commit rows passed; `go test
./internal/seatcommit -count=1` passed.
