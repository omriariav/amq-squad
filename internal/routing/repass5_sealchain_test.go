package routing

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// independentTag is the routing tag as the routing package computed it
// before the extraction: hex HMAC-SHA256 under key of the label, a NUL and
// the value's JSON.
func independentTag(key []byte, label string, v any) string {
	body, _ := json.Marshal(v)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(label))
	m.Write([]byte{0})
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// The sealchain re-extraction over routing's final code: the outcome row's
// and the head record's tag bytes are unchanged, and the routing log and
// the sealed files share one seed per session root (written by whichever
// starts first).
func TestSealchainKeepsRoutingTagsAndOneSeed(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	row := Row{Schema: RowSchema, At: time.Unix(1_700_000_000, 0).UTC(), Task: "t1", Attempt: 1, Class: "implement", Seq: 1}
	unsigned := row
	unsigned.Tag = ""
	if got := row.Sign(key).Tag; got != independentTag(key, "amq-squad routing outcome row v1", unsigned) {
		t.Fatalf("the outcome row's tag changed: %s", got)
	}
	h := headRecord{Schema: 1, OutcomeSeq: 3, OutcomeTag: "abc", Open: []Problem{}, At: time.Unix(1_700_000_000, 0).UTC()}
	if got := h.sign(key).Tag; got != independentTag(key, "amq-squad routing head v1", h) {
		t.Fatalf("the routing head's tag changed: %s", got)
	}
	for _, routingFirst := range []bool{true, false} {
		root := t.TempDir()
		var seed string
		var sealed sealchain.Seed
		var err error
		if routingFirst {
			if seed, err = SessionSeed(root); err == nil {
				sealed, err = sealchain.SessionKey(root)
			}
		} else {
			if sealed, err = sealchain.SessionKey(root); err == nil {
				seed, err = SessionSeed(root)
			}
		}
		if err != nil || !bytes.Equal(SeedKey(seed), sealed.Key) {
			t.Fatalf("routing first %v: two seeds for one root (%v)", routingFirst, err)
		}
		_, dev, ino, _ := SessionSeedID(root)
		if dev != sealed.Dev || ino != sealed.Ino {
			t.Fatalf("routing first %v: the seed's identity differs", routingFirst)
		}
	}
}
