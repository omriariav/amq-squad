//go:build amqpi

package piruntime

import _ "embed"

// The payload files are produced by scripts/build-pi-runtime.sh and are
// gitignored; a build with the amqpi tag fails without them.

//go:embed payload/manifest.json
var embeddedManifest []byte

//go:embed payload/manifest.sha256
var embeddedDigest []byte

//go:embed payload/payload.tar.gz
var embeddedPayload []byte

// Load returns the payload compiled into this binary, after checking the
// manifest against its pinned digest (the trust root).
func Load() (Embedded, error) {
	return ParseEmbedded(embeddedManifest, embeddedDigest, func() ([]byte, error) { return embeddedPayload, nil })
}
