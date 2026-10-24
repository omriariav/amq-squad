package routing

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// LedgerUsage reads an ended attempt's evidence from the 2a-3 hook ledger of
// its holder (<root>/extensions/amq-squad/ledger/<holder>.jsonl): the token
// counts of every payload in the attempt's window, summed. Tokens are nil
// when no payload in the window names any (Claude Code hook payloads carry
// none, briefs lead ruling 5; Codex evidence waits for the notify fan-out,
// plan 16.8). Cost is tokens times the pool's price, nil without a price.
func LedgerUsage(root string, prices map[task.Pool]Price) func(task.Task, task.Attempt) Usage {
	return func(_ task.Task, a task.Attempt) Usage {
		var u Usage
		if a.Holder == "" {
			return u
		}
		var in, out int64
		seen, partial := false, false
		path := filepath.Join(root, "extensions", "amq-squad", "ledger", a.Holder+".jsonl")
		_ = readLines(path, func(line []byte) {
			var l struct {
				At      string          `json:"at"`
				Payload json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(line, &l) != nil {
				return
			}
			at, err := time.Parse(time.RFC3339Nano, l.At)
			if err != nil || at.Before(a.GrantedAt) || at.After(a.EndedAt) {
				return
			}
			switch i, o, inOK, outOK := status.PayloadTokenCounts(l.Payload); {
			case inOK && outOK:
				in, out, seen = in+i, out+o, true
			case inOK || outOK:
				partial = true // one counter: never a zero for the other
			}
		})
		// Both counters or unknown usage; unknown usage is incomplete
		// evidence (Codex pass 1, P2-5).
		if !seen || partial {
			return u
		}
		u.TokensIn, u.TokensOut = &in, &out
		if a.Pool != nil {
			if p, ok := prices[*a.Pool]; ok {
				c := float64(in)/1e6*p.InputPerMTok + float64(out)/1e6*p.OutputPerMTok
				u.CostUSD = &c
			}
		}
		return u
	}
}
