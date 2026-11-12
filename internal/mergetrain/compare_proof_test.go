package mergetrain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// legacyCompletenessJQ is the pre-change patch-presence question kept by the fake.
const legacyCompletenessJQ = `{file_count: (.files | length), missing_patch: ([.files[] | select(.patch == null)] | length)}`

// compareProofBehind builds the asserted ground for a bound, BEHIND PR 6.
// The ground is asserted, never assumed from a command's side effect.
func compareProofBehind(t *testing.T, files map[string]string) (*fixture, string, string, Request) {
	t.Helper()
	f := newFixture(t)
	setStrict(f)
	f.branch("pr6", "main", files)
	f.edit(func(s *fakeState) { s.PRs["6"] = &fakePR{Head: "pr6", Base: "main", Draft: true, State: "OPEN"} })
	old := f.head("pr6")
	declared := make([]string, 0, len(files))
	for path := range files {
		declared = append(declared, path)
	}
	slices.Sort(declared)
	taskID := f.bindTask(6, old, declared)
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 6}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base.txt": "advance\n"})
	return f, taskID, old, req
}

// fakeCompareAnswer asks the fake itself for one compare representation.
func fakeCompareAnswer(t *testing.T, f *fixture, base, head, jq string, diff bool) []byte {
	t.Helper()
	args := []string{"api", "repos/" + fixtureRepo + "/compare/" + base + "..." + head}
	if diff {
		args = append(args, "-H", "Accept: application/vnd.github.diff", "--allow-escape-sequences")
	} else {
		args = append(args, "--jq", jq)
	}
	out, stderr, err := f.gh.runner(context.Background(), args)
	if err != nil {
		t.Fatalf("fake compare: %v: %s", err, stderr)
	}
	return out
}

// assertCompareRefused checks the exact pre-update refusal and proof error.
func assertCompareRefused(t *testing.T, f *fixture, taskID, old string, req Request, mismatch string) {
	t.Helper()
	res, err := f.train.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	p := res.PRs[0]
	bound := boundHead(t, f, taskID)
	branch := f.head("pr6")
	if p.Status != "refused" || len(p.Refusals) != 1 || p.Refusals[0].Rule != RuleRebasePatch || p.Refusals[0].Detail != "the update could not be proved: GitHub returned an incomplete diff" {
		t.Fatalf("not refused in the fixed words: %+v", p)
	}
	if p.PatchProof == nil || p.PatchProof.OldHead != old || p.PatchProof.NewHead != "" || bound != old || branch != old || strings.Contains(f.calls(), "updatePullRequestBranch") || strings.Contains(f.calls(), "-X PUT") {
		t.Fatalf("the update moved despite the refusal: proof=%+v bound=%s branch=%s calls=%s", p.PatchProof, bound, branch, f.calls())
	}
	_, err = f.gh.PatchID(context.Background(), fixtureRepo, f.head("main"), old)
	if !errors.Is(err, ErrIncompleteDiff) || !strings.HasPrefix(err.Error(), "GitHub returned an incomplete diff: "+mismatch+": ") {
		t.Fatalf("proof error %v, want %s", err, mismatch)
	}
}

// TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged proves patch
// absence in JSON is irrelevant when the file list and API diff agree.
func TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged(t *testing.T) {
	f, taskID, old, req := compareProofBehind(t, map[string]string{"large.txt": "one\ntwo\n"})
	f.edit(func(s *fakeState) { s.NoPatchFiles = []string{"large.txt"} })
	base := f.head("main")
	legacy := fakeCompareAnswer(t, f, base, old, legacyCompletenessJQ, false)
	diff := fakeCompareAnswer(t, f, base, old, "", true)
	if string(legacy) != "{\"file_count\":1,\"missing_patch\":1}\n" || !strings.Contains(string(diff), "diff --git a/large.txt b/large.txt") || !strings.Contains(string(diff), "+two") {
		t.Fatalf("ground: legacy=%q diff=%q", legacy, diff)
	}
	rebasedAndMerged(t, f, taskID, old, req)
}

// TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate proves the
// exact file-set mismatch stops before branch mutation.
func TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate(t *testing.T) {
	f, taskID, old, req := compareProofBehind(t, map[string]string{"one.txt": "one\n", "two.txt": "two\n"})
	f.edit(func(s *fakeState) { s.OmitDiffFiles = []string{"two.txt"} })
	base := f.head("main")
	list := fakeCompareAnswer(t, f, base, old, compareProofFilesJQ, false)
	diff := fakeCompareAnswer(t, f, base, old, "", true)
	if !strings.Contains(string(list), `"path":"one.txt"`) || !strings.Contains(string(list), `"path":"two.txt"`) || !strings.Contains(string(diff), "b/one.txt") || strings.Contains(string(diff), "two.txt") {
		t.Fatalf("ground: list=%q diff=%q", list, diff)
	}
	assertCompareRefused(t, f, taskID, old, req, "the diff's files are not the compare's files")
}

// TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate proves
// per-file additions and deletions, not totals, bind the proof.
func TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate(t *testing.T) {
	f, taskID, old, req := compareProofBehind(t, map[string]string{"one.txt": "one\n", "two.txt": "two\n"})
	f.edit(func(s *fakeState) { s.ExtraAdditionFiles = []string{"one.txt"} })
	base := f.head("main")
	list := fakeCompareAnswer(t, f, base, old, compareProofFilesJQ, false)
	diff := fakeCompareAnswer(t, f, base, old, "", true)
	if !strings.Contains(string(list), `"path":"one.txt","status":"added","additions":2`) || strings.Count(string(diff), "+one") != 1 || !strings.Contains(string(diff), "b/two.txt") {
		t.Fatalf("ground: list=%q diff=%q", list, diff)
	}
	assertCompareRefused(t, f, taskID, old, req, "the diff's added and removed lines are not the compare's counts")
}

// TestBehindPRWithABinaryFileIsUpdatedAndMerged proves a zero-count binary
// section with an index line is complete even though JSON has no patch.
func TestBehindPRWithABinaryFileIsUpdatedAndMerged(t *testing.T) {
	f, taskID, old, req := compareProofBehind(t, map[string]string{"asset.bin": "\x00", "note.txt": "text\n"})
	base := f.head("main")
	list := fakeCompareAnswer(t, f, base, old, compareProofFilesJQ, false)
	legacy := fakeCompareAnswer(t, f, base, old, legacyCompletenessJQ, false)
	diff := fakeCompareAnswer(t, f, base, old, "", true)
	section := string(diff)
	if !strings.Contains(string(list), `"path":"asset.bin","status":"added","additions":0,"deletions":0`) || string(legacy) != "{\"file_count\":2,\"missing_patch\":1}\n" || !strings.Contains(section, "diff --git a/asset.bin b/asset.bin\n") || !strings.Contains(section, "\nindex ") || strings.Contains(section[strings.Index(section, "b/asset.bin"):strings.Index(section, "b/note.txt")], "@@ ") {
		t.Fatalf("ground: list=%q legacy=%q diff=%q", list, legacy, diff)
	}
	rebasedAndMerged(t, f, taskID, old, req)
}

// TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate proves the
// zero-count section must carry its header evidence.
func TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate(t *testing.T) {
	f, taskID, old, req := compareProofBehind(t, map[string]string{"asset.bin": "\x00", "note.txt": "text\n"})
	f.edit(func(s *fakeState) { s.OmitDiffIndex = []string{"asset.bin"} })
	base := f.head("main")
	diff := string(fakeCompareAnswer(t, f, base, old, "", true))
	asset, note := strings.Index(diff, "b/asset.bin"), strings.Index(diff, "b/note.txt")
	if asset < 0 || note < 0 || strings.Contains(diff[asset:note], "\nindex ") || !strings.Contains(diff[note:], "\nindex ") {
		t.Fatalf("ground: diff=%q", diff)
	}
	assertCompareRefused(t, f, taskID, old, req, "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines")
}

// TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead proves the 300 arm
// runs before the API diff read and the under-limit case does read it once.
func TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead(t *testing.T) {
	for _, tc := range []struct {
		name, count string
		n           int
		reads       int
	}{{"at the limit", "limit", MaxCompareFiles, 0}, {"one under", "under", MaxCompareFiles - 1, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var reads int
			g := &GH{runner: func(_ context.Context, args []string) ([]byte, string, error) {
				if slices.Contains(args, compareProofFilesJQ) {
					var b strings.Builder
					for i := 0; i < tc.n; i++ {
						fmt.Fprintf(&b, "{\"path\":\"f/%03d\",\"additions\":1,\"deletions\":0}\n", i)
					}
					return []byte(b.String()), "", nil
				}
				if slices.Contains(args, "Accept: application/vnd.github.diff") {
					reads++
					return nil, "", nil
				}
				return nil, "unexpected", errors.New("unexpected call")
			}}
			_, err := g.PatchID(context.Background(), fixtureRepo, "base", "head")
			if !errors.Is(err, ErrIncompleteDiff) || reads != tc.reads {
				t.Fatalf("%s: err=%v reads=%d want %d", tc.count, err, reads, tc.reads)
			}
		})
	}
}

// TestCompareDiffMismatchWords fixes the accepted forms and all four exact
// mismatch texts independently of the train.
func TestCompareDiffMismatchWords(t *testing.T) {
	fc := func(path, previous string, add, del int) FileChange {
		return FileChange{Path: path, PreviousPath: previous, Additions: add, Deletions: del}
	}
	const first = "the diff's files are not the compare's files"
	const second = "the diff's added and removed lines are not the compare's counts"
	const third = "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines"
	const fourth = "the diff is cut short inside a hunk"
	changed := "diff --git a/a.txt b/a.txt\nindex 1..2 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n"
	two := changed + "diff --git a/b.txt b/b.txt\nindex 3..4 100644\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-before\n+after\n"
	rename := "diff --git a/old.txt b/new.txt\nsimilarity index 100%\nrename from old.txt\nrename to new.txt\n"
	blankEnd := "diff --git a/a.txt b/a.txt\nindex 1..2 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1,4 +1,4 @@\n-a\n+A\n b\n c\n \n"
	binaryGH := "diff --git a/a.bin b/a.bin\nnew file mode 100644\nindex 000000000..8dbd5ec6b\nBinary files /dev/null and b/a.bin differ\n"
	binaryGit := "diff --git a/a.bin b/a.bin\nnew file mode 100644\nindex 0000000000000000000000000000000000000000..1111111111111111111111111111111111111111\nGIT binary patch\nliteral 1\nIc${Nk000310RR91\n\n"
	tests := []struct {
		name  string
		files []FileChange
		diff  string
		want  string
	}{
		{"one changed file", []FileChange{fc("a.txt", "", 1, 1)}, changed, ""},
		{"two changed files in other list order", []FileChange{fc("b.txt", "", 1, 1), fc("a.txt", "", 1, 1)}, two, ""},
		{"no files", nil, "", ""},
		{"header-looking content", []FileChange{fc("a.txt", "", 2, 1)}, strings.Replace(strings.Replace(changed, "@@ -1 +1 @@", "@@ -1 +1,2 @@", 1), "+new\n", "+new\n+diff --git a/x b/x\n", 1), ""},
		{"minus and plus content", []FileChange{fc("a.txt", "", 1, 1)}, strings.Replace(changed, "-old\n+new", "--- the copy\n+++ the copy", 1), ""},
		{"no newline marker", []FileChange{fc("a.txt", "", 1, 1)}, strings.TrimSuffix(changed, "\n") + "\\ No newline at end of file\n", ""},
		{"omitted hunk lengths", []FileChange{fc("a.txt", "", 1, 1)}, changed, ""},
		{"deleted file", []FileChange{fc("a.txt", "", 0, 1)}, "diff --git a/a.txt b/a.txt\ndeleted file mode 100644\nindex 1..0\n--- a/a.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n", ""},
		{"renamed with changes", []FileChange{fc("new.txt", "old.txt", 1, 1)}, strings.ReplaceAll(changed, "a.txt", "old.txt")[:0] + "diff --git a/old.txt b/new.txt\nindex 1..2 100644\n--- a/old.txt\n+++ b/new.txt\n@@ -1 +1 @@\n-old\n+new\n", ""},
		{"binary GitHub", []FileChange{fc("a.bin", "", 0, 0)}, binaryGH, ""},
		{"binary git", []FileChange{fc("a.bin", "", 0, 0)}, binaryGit, ""},
		{"zero count index", []FileChange{fc("a.txt", "", 0, 0)}, "diff --git a/a.txt b/a.txt\nindex 1..2 100644\n", ""},
		{"rename no change", []FileChange{fc("new.txt", "old.txt", 0, 0)}, rename, ""},
		{"added empty", []FileChange{fc("empty", "", 0, 0)}, "diff --git a/empty b/empty\nnew file mode 100644\nindex 0000000..e69de29\n", ""},
		{"deleted empty local form", []FileChange{fc("empty", "", 0, 0)}, "diff --git a/empty b/empty\ndeleted file mode 100644\nindex e69de29..0000000\n", ""},
		{"blank context end", []FileChange{fc("a.txt", "", 1, 1)}, blankEnd, ""},
		{"space in name local form", []FileChange{fc("a b.txt", "", 1, 1)}, strings.ReplaceAll(changed, "a.txt", "a b.txt"), ""},
		{"file empty diff", []FileChange{fc("a.txt", "", 1, 1)}, "", first},
		{"missing listed file", []FileChange{fc("a.txt", "", 1, 1), fc("b.txt", "", 1, 1)}, changed, first},
		{"unexpected file", []FileChange{fc("a.txt", "", 1, 1)}, two, first},
		{"duplicate section", []FileChange{fc("a.txt", "", 2, 2)}, changed + changed, first},
		{"rename without previous", []FileChange{fc("new.txt", "", 0, 0)}, rename, first},
		{"quoted name local form", []FileChange{fc("café.txt", "", 1, 1)}, "diff --git \"a/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\n", first},
		{"one addition missing", []FileChange{fc("a.txt", "", 2, 1)}, changed, second},
		{"removed line missing", []FileChange{fc("a.txt", "", 1, 2)}, changed, second},
		{"swapped per-file counts", []FileChange{fc("a.txt", "", 2, 0), fc("b.txt", "", 0, 2)}, two, second},
		{"cut final context", []FileChange{fc("a.txt", "", 1, 1)}, strings.TrimSuffix(blankEnd, " \n"), fourth},
		{"next section inside hunk", []FileChange{fc("a.txt", "", 1, 1), fc("b.txt", "", 1, 1)}, "diff --git a/a.txt b/a.txt\n@@ -1,2 +1,2 @@\n-old\n+new\ndiff --git a/b.txt b/b.txt\n@@ -1 +1 @@\n-x\n+y\n", fourth},
		{"blank end cut", []FileChange{fc("a.txt", "", 1, 1)}, strings.TrimSuffix(blankEnd, " \n"), fourth},
		{"zero with hunk", []FileChange{fc("a.txt", "", 0, 0)}, changed, third},
		{"binary no index", []FileChange{fc("a.bin", "", 0, 0)}, strings.Replace(binaryGH, "index 000000000..8dbd5ec6b\n", "", 1), third},
		{"zero only header", []FileChange{fc("a.txt", "", 0, 0)}, "diff --git a/a.txt b/a.txt\n", third},
		{"rename lacks to", []FileChange{fc("new.txt", "old.txt", 0, 0)}, strings.Replace(rename, "rename to new.txt\n", "", 1), third},
		{"rename wrong from", []FileChange{fc("new.txt", "old.txt", 0, 0)}, strings.Replace(rename, "rename from old.txt", "rename from other.txt", 1), third},
		{"rename 99 percent", []FileChange{fc("new.txt", "old.txt", 0, 0)}, strings.Replace(rename, "100%", "99%", 1), third},
		{"mode only local form", []FileChange{fc("a.txt", "", 0, 0)}, "diff --git a/a.txt b/a.txt\nold mode 100644\nnew mode 100755\n", third},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := compareDiffMismatch(tc.files, []byte(tc.diff))
			if tc.want == "" && got != "" {
				t.Fatalf("got %q", got)
			}
			if tc.want != "" && !strings.HasPrefix(got, tc.want+": ") {
				t.Fatalf("got %q, want prefix %q", got, tc.want+": ")
			}
		})
	}
}

// TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck pins the API
// bytes' text-only patch id independently of the new check.
func TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck(t *testing.T) {
	var saved struct {
		Repository string `json:"repository"`
		Base       string `json:"base"`
		Head       string `json:"head"`
		Files      []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
			Status           string `json:"status"`
			Additions        int    `json:"additions"`
			Deletions        int    `json:"deletions"`
		} `json:"files"`
		Diff string `json:"diff"`
	}
	raw, err := os.ReadFile("testdata/compare-b46cb806-18dd4997.json")
	if err != nil || json.Unmarshal(raw, &saved) != nil || len(saved.Files) != 3 || len(saved.Diff) != 2680 {
		t.Fatalf("saved ground: err=%v files=%d diff=%d", err, len(saved.Files), len(saved.Diff))
	}
	g := &GH{runner: func(_ context.Context, args []string) ([]byte, string, error) {
		switch {
		case slices.Contains(args, compareProofFilesJQ):
			var b strings.Builder
			for _, f := range saved.Files {
				out, _ := json.Marshal(FileChange{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions})
				b.Write(out)
				b.WriteByte('\n')
			}
			return []byte(b.String()), "", nil
		case slices.Contains(args, legacyCompletenessJQ):
			return []byte("{\"file_count\":3,\"missing_patch\":0}\n"), "", nil
		case slices.Contains(args, "Accept: application/vnd.github.diff"):
			return []byte(saved.Diff), "", nil
		default:
			return nil, "unexpected", errors.New("unexpected call")
		}
	}}
	id, err := g.PatchID(context.Background(), saved.Repository, saved.Base, saved.Head)
	if err != nil || id != "d98a700eeca038b3cf05beae0453c0fc52246412" {
		t.Fatalf("patch id %q err %v", id, err)
	}
}

// TestLeadRebindOverAFileWithNoPatchIsProvedAndBound proves the same PatchID
// path admits a lead rebind when JSON omits the patch at both heads.
func TestLeadRebindOverAFileWithNoPatchIsProvedAndBound(t *testing.T) {
	f, taskID, old, _ := strictBound(t)
	f.edit(func(s *fakeState) { s.NoPatchFiles = []string{"pkg/a.go"} })
	if got := string(fakeCompareAnswer(t, f, f.head("main"), old, legacyCompletenessJQ, false)); got != "{\"file_count\":1,\"missing_patch\":1}\n" {
		t.Fatalf("old ground %q", got)
	}
	_, moved := moveBranch(f, "pr1", nil, true)
	if got := string(fakeCompareAnswer(t, f, f.head("main"), moved, legacyCompletenessJQ, false)); got != "{\"file_count\":1,\"missing_patch\":1}\n" {
		t.Fatalf("new ground %q", got)
	}
	store := f.tasks()
	res, err := Rebind(context.Background(), RebindInput{Forge: f.gh, Audit: f.train.Audit, Tasks: store, Binder: StoreBinder{Store: store}, PR: 1, From: old, To: moved, Lead: "lead"})
	if err != nil || res.Task != taskID || res.PatchID == "" || boundHead(t, f, taskID) != moved {
		t.Fatalf("rebind: %v %+v bound=%s", err, res, boundHead(t, f, taskID))
	}
}

// TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged proves update
// recovery uses the same proof and records the recovered rebase.
func TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged(t *testing.T) {
	f, taskID, old, req := strictBound(t)
	f.edit(func(s *fakeState) { s.NoPatchFiles = []string{"pkg/a.go"} })
	base := f.head("main")
	if err := f.train.Audit.Append(Entry{Run: "mt-earlier", Kind: "update_requested", Repository: fixtureRepo, Base: "main", PR: 1, Task: taskID, Head: old, BaseSHA: base}); err != nil {
		t.Fatal(err)
	}
	_, moved := moveBranch(f, "pr1", nil, true)
	for _, head := range []string{old, moved} {
		if got := string(fakeCompareAnswer(t, f, base, head, legacyCompletenessJQ, false)); got != "{\"file_count\":1,\"missing_patch\":1}\n" {
			t.Fatalf("ground at %s: %q", head, got)
		}
	}
	res, err := f.train.Run(context.Background(), req)
	if err != nil || res.PRs[0].Status != "merged" || res.PRs[0].Head != moved || boundHead(t, f, taskID) != moved {
		t.Fatalf("recovery: %v %+v bound=%s", err, res.PRs[0], boundHead(t, f, taskID))
	}
	entries, _ := f.train.Audit.Read()
	if !slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Kind == "rebase" && e.Head == old && e.NewHead == moved && strings.HasPrefix(e.Detail, "recovered: the update journaled by run")
	}) {
		t.Fatalf("no recovered rebase: %+v", entries)
	}
}

// TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged proves the
// fake and the proof preserve the trailing context line byte for byte.
func TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged(t *testing.T) {
	f := newFixture(t)
	setStrict(f)
	advanceBase(f, map[string]string{"tail.txt": "a\nb\nc\n\n"})
	f.branch("pr6", "main", map[string]string{"tail.txt": "A\nb\nc\n\n"})
	f.edit(func(s *fakeState) { s.PRs["6"] = &fakePR{Head: "pr6", Base: "main", Draft: true, State: "OPEN"} })
	old := f.head("pr6")
	taskID := f.bindTask(6, old, []string{"tail.txt"})
	req := Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{{Number: 6}}}
	f.realRun(&req)
	advanceBase(f, map[string]string{"base-after.txt": "advance\n"})
	diff := fakeCompareAnswer(t, f, f.head("main"), old, "", true)
	if !strings.HasSuffix(string(diff), "\n-a\n+A\n b\n c\n \n") {
		t.Fatalf("diff end %q", diff[max(0, len(diff)-40):])
	}
	rebasedAndMerged(t, f, taskID, old, req)
}
