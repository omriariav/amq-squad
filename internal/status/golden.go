package status

import _ "embed"

// Golden is the schema 1 golden fixture (testdata/status-v1.golden.json),
// embedded so the binary can check its own producer against the contract
// (doctor's status contract self-check, plan 16.2).
//
//go:embed testdata/status-v1.golden.json
var Golden []byte
