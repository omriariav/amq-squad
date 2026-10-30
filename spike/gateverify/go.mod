module github.com/omriariav/amq-squad/v2/spike/gateverify

go 1.25.12

require (
	github.com/btcsuite/btcd/btcec/v2 v2.5.0
	github.com/coder/websocket v1.8.15
	github.com/omriariav/amq-squad/v2 v2.0.0-00010101000000-000000000000
)

require (
	github.com/btcsuite/btcd/chainhash/v2 v2.0.0 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.1.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// The spike imports internal/operatorauth from the main module unchanged.
// Go admits the internal import because this module's path is under the
// main module's path; the replace keeps it on the working tree.
replace github.com/omriariav/amq-squad/v2 => ../..
