package cli

import (
	"testing"
)

func TestMergeOperatorAttentionActiveWinsOverClearedInEitherOrder(t *testing.T) {
	active := operatorAttention{Key: "gate-key", Thread: "gate/release", LatestID: "request"}
	cleared := operatorAttention{Key: "gate-key", Thread: "gate/release", Cleared: true}
	for _, pair := range []struct {
		base, extra []operatorAttention
	}{{[]operatorAttention{active}, []operatorAttention{cleared}}, {[]operatorAttention{cleared}, []operatorAttention{active}}} {
		got := mergeOperatorAttention(pair.base, pair.extra)
		if len(got) != 1 || got[0].Cleared || got[0].LatestID != "request" {
			t.Fatalf("mergeOperatorAttention = %+v, want active projection", got)
		}
	}
}
