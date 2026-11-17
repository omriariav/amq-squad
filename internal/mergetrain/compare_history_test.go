package mergetrain

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// compareHistoryPairs reads a complete history list or returns an error and
// no partial list.
func compareHistoryPairs(path string) ([][2]string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open the list: %w", err)
	}
	defer fh.Close()
	var pairs [][2]string
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		ids := strings.Fields(sc.Text())
		if len(ids) != 2 || len(ids[0]) != 40 || len(ids[1]) != 40 {
			return nil, fmt.Errorf("line %d is not two commit ids", len(pairs)+1)
		}
		pairs = append(pairs, [2]string{ids[0], ids[1]})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %d pair before the list error: %w", len(pairs), err)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no pair")
	}
	return pairs, nil
}

// TestTheCheckOverTheHistoryRefusesNoWholeDiff runs compareDiffMismatch over
// every commit pair of a list file, with the file list local git gives for
// the pair: its subtest "plain" over `git diff -M`, its subtest "binary" over
// `git diff -M --binary`. Every such diff is whole, so every refusal is a
// wrong one: a subtest fails if it has any and prints each. It needs the
// repository's history and a list file, so it runs only when
// TRAINFIX_HISTORY_LIST names the list (one "<parent> <commit>" pair a line);
// the default run and CI skip it.
func TestTheCheckOverTheHistoryRefusesNoWholeDiff(t *testing.T) {
	list := os.Getenv("TRAINFIX_HISTORY_LIST")
	if list == "" {
		t.Skip("TRAINFIX_HISTORY_LIST is not set: the history run is the task's own, not the default run's")
	}
	pairs, err := compareHistoryPairs(list)
	if err != nil {
		t.Fatalf("the list: %v", err)
	}
	forms := []struct {
		name string
		args []string
	}{
		{"plain", []string{"diff", "-M"}},
		{"binary", []string{"diff", "-M", "--binary"}},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			git := func(args ...string) []byte {
				t.Helper()
				out, err := exec.Command("git", args...).Output()
				if err != nil {
					t.Fatalf("git %s: %v (the worktree needs the commits of the list)", strings.Join(args, " "), err)
				}
				return out
			}
			refused := 0
			for _, pair := range pairs {
				var files []FileChange
				rec := bytes.Split(git("diff", "--numstat", "-M", "-z", pair[0], pair[1]), []byte{0})
				for i := 0; i < len(rec) && len(rec[i]) > 0; i++ {
					field := strings.SplitN(string(rec[i]), "\t", 3)
					if len(field) != 3 {
						t.Fatalf("numstat of %s %s: %q", pair[0], pair[1], rec[i])
					}
					// a binary file's counts are "-" and "-": GitHub gives 0 and 0
					add, _ := strconv.Atoi(field[0])
					del, _ := strconv.Atoi(field[1])
					file := FileChange{Path: field[2], Additions: add, Deletions: del}
					if field[2] == "" {
						if i+2 >= len(rec) {
							t.Fatalf("numstat of %s %s: a rename with no names", pair[0], pair[1])
						}
						file.PreviousPath, file.Path = string(rec[i+1]), string(rec[i+2])
						i += 2
					}
					files = append(files, file)
				}
				diff := git(append(append([]string{}, form.args...), pair[0], pair[1])...)
				if why := compareDiffMismatch(files, diff); why != "" {
					refused++
					t.Errorf("git %s %s %s: %s", strings.Join(form.args, " "), pair[0], pair[1], why)
				}
			}
			t.Logf("git %s: commits %d, refused %d", strings.Join(form.args, " "), len(pairs), refused)
		})
	}
}

// TestTheHistoryListIsReadWholeOrItsErrorIsReturned proves a partial scan is
// never accepted as the complete history list.
func TestTheHistoryListIsReadWholeOrItsErrorIsReturned(t *testing.T) {
	dir := t.TempDir()
	one := strings.Repeat("1", 40)
	two := strings.Repeat("2", 40)
	goodLine := one + " " + two + "\n"
	good := dir + "/good"
	if err := os.WriteFile(good, []byte(goodLine+two+" "+one+"\n"), 0o600); err != nil {
		t.Fatal("write good list")
	}
	pairs, err := compareHistoryPairs(good)
	if err != nil || len(pairs) != 2 || pairs[0] != [2]string{one, two} || pairs[1] != [2]string{two, one} {
		t.Fatalf("good list: pairs=%d err=%v", len(pairs), err)
	}

	overlong := strings.Repeat("x", bufio.MaxScanTokenSize+1)
	if len(overlong) <= bufio.MaxScanTokenSize {
		t.Fatal("over-long ground is not longer than the scanner buffer")
	}
	longList := dir + "/long"
	if err := os.WriteFile(longList, []byte(goodLine+overlong+"\n"), 0o600); err != nil {
		t.Fatal("write long list")
	}
	pairs, err = compareHistoryPairs(longList)
	if pairs != nil || err == nil || !strings.Contains(err.Error(), "read 1 pair before") {
		t.Fatal("over-long list did not return its error after one pair")
	}

	bad := dir + "/bad"
	if err := os.WriteFile(bad, []byte("not a pair\n"), 0o600); err != nil {
		t.Fatal("write bad list")
	}
	empty := dir + "/empty"
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal("write empty list")
	}
	for _, tc := range []struct {
		name string
		path string
	}{{"bad line", bad}, {"empty", empty}, {"absent", dir + "/absent"}} {
		t.Run(tc.name, func(t *testing.T) {
			pairs, err := compareHistoryPairs(tc.path)
			if pairs != nil || err == nil {
				t.Fatal("list did not return an error without pairs")
			}
		})
	}
}
