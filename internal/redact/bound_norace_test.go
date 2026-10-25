//go:build !race

package redact

import "time"

// linearBound is TestBlockIsLinearOnALongLine's bound on the gate host.
const linearBound = 2 * time.Second
