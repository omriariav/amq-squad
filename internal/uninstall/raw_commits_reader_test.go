//go:build unix

package uninstall

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

func TestCommitLedgerWorktreeCandidatesReadRawAndLegacyLayouts(t *testing.T) {
	for _, layout := range []string{"raw", "legacy"} {
		t.Run(layout, func(t *testing.T) {
			f := newFixture(t)
			if err := os.RemoveAll(filepath.Join(f.x, "launch", "seat-a")); err != nil {
				t.Fatal(err)
			}
			ledger := filepath.Join(f.x, "commits", "seat-a.json")
			if layout == "legacy" {
				raw, err := os.ReadFile(ledger)
				if err != nil {
					t.Fatal(err)
				}
				body, _, err := sealchain.SplitRaw(raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(ledger, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(ledger)
			if err != nil {
				t.Fatal(err)
			}

			p, err := discover(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			after, err := os.ReadFile(ledger)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("uninstall planning changed the commit ledger")
			}
			for _, op := range p.ops {
				if op.group == GroupWorktree && op.display == f.wt {
					return
				}
			}
			t.Fatalf("%s commit ledger did not contribute retired seat worktree %s", layout, f.wt)
		})
	}
}

func TestMalformedCommitLedgerAddsNoWorktreeCandidateAndPlanContinues(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(filepath.Join(f.x, "launch", "seat-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.x, "commits", "seat-a.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := discover(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, op := range p.ops {
		if op.group == GroupWorktree && op.display == f.wt {
			t.Fatalf("malformed commit ledger contributed worktree candidate %s", f.wt)
		}
	}
}
