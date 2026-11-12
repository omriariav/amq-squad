package mergetrain

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

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
	fh, err := os.Open(list)
	if err != nil {
		t.Fatalf("the list: %v", err)
	}
	defer fh.Close()
	var pairs [][2]string
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		ids := strings.Fields(sc.Text())
		if len(ids) != 2 || len(ids[0]) != 40 || len(ids[1]) != 40 {
			t.Fatalf("the list: line %d is not two commit ids", len(pairs)+1)
		}
		pairs = append(pairs, [2]string{ids[0], ids[1]})
	}
	if len(pairs) == 0 {
		t.Fatal("the list: no pair")
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
