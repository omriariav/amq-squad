//go:build race

package redact

import "time"

// linearBound is TestBlockIsLinearOnALongLine's bound under the race
// detector, whose instrumentation slows the scan about tenfold (4.2 s
// measured on the gate host against 0.18 s without it).
const linearBound = 20 * time.Second
