package mergetrain

import (
	"context"
	"slices"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

// Brief 3.1.1-1, rows 1 and 5 at the escalator: the merge gate a refusal
// raises reads the PR's title (from the forge, gh pr view's title) and the
// base the train merges into, and its second line names the refused rule in
// words: the question never says the checks passed when the train refused
// them.
func TestTheMergeGateAsksInPlainWordsFromWhatTheTrainRead(t *testing.T) {
	for _, sub := range []bool{false, true} {
		var f *fixture
		if sub {
			f = newSubprocessFixture(t) // gh's argv and output parsing end to end
		} else {
			f = newFixture(t)
		}
		f.edit(func(s *fakeState) {
			s.RedCI = []string{f.head("pr1")}
			s.PRs["1"].Title = "pkg: add A, the first function"
		})
		req := f.req
		req.PRs = []Item{f.req.PRs[0]}
		res, err := f.train.Run(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := f.store.Load(res.PRs[0].GateID)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"Allow a merge of pull request #1 into main?", `"pkg: add A, the first function"`, "The train refused it: its checks are not green.", "Yes lets the train merge it. No leaves it open."}
		if got := gatestore.PlainLines(rec); !slices.Equal(got, want) || rec.Facts == nil || rec.Facts.Branch != "main" {
			t.Fatalf("subprocess %v: plain lines %q, facts %+v", sub, got, rec.Facts)
		}
	}
}
