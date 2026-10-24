//go:build !amqpi

package piruntime

// Load reports that this build carries no pi runtime. Default builds
// (linux, windows, and darwin without the amqpi tag) compile without the
// payload and refuse to start a pi lead or seat.
func Load() (Embedded, error) { return Embedded{}, ErrNotEmbedded }
