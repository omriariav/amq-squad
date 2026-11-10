package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
)

// 3.2.0-2b-1 item 3 and item 9: `seat clean` is a lead verb that goes
// through the scheduler's decision socket (the CLI sends the decision, the
// scheduler proves the sender and acts; R9's refusal and acceptance are
// schedproc's row). Here: no running scheduler is an error in words; a
// socket not open yet is scheduler_starting; a refusal prints the words
// above the rule and the code below it, with the commit-refused exit code;
// an accepted clean prints the scheduler's sentence; --json carries the
// reply. --me is refused: the lead is proved from the process, never named.
func TestSeatCleanSendsTheDecisionAndWordsTheReply(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", filepath.Join(root, "home"))
	var out bytes.Buffer
	if err := runSeatClean([]string{"--root", root, "--seat", "dev"}, &out); err == nil || !strings.Contains(err.Error(), "no running scheduler for "+root) {
		t.Fatalf("no launch record: %v", err)
	}
	if err := runSeatClean([]string{"--root", root, "--seat", "dev", "--me", "lead"}, &out); err == nil || !strings.Contains(err.Error(), "takes no --me") {
		t.Fatalf("--me: %v", err)
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 4242}); err != nil {
		t.Fatal(err)
	}
	var coded *codedExitError
	err := runSeatClean([]string{"--root", root, "--seat", "dev"}, &out)
	if !errors.As(err, &coded) || coded.code != ExitCommitRefused || !strings.Contains(err.Error(), "code: "+schedproc.RefusedStarting) {
		t.Fatalf("a socket not open yet: %v", err)
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 4242, DecisionSocket: filepath.Join(root, "decide.sock")}); err != nil {
		t.Fatal(err)
	}
	prev := seatCleanSend
	t.Cleanup(func() { seatCleanSend = prev })
	var sent []schedproc.CleanRequest
	reply := schedproc.DecideReply{Code: schedproc.RefusedNotLead, Detail: "pid 7 does not descend from the lead lead's recorded incarnation"}
	seatCleanSend = func(gotRoot, path string, req schedproc.CleanRequest) (schedproc.DecideReply, error) {
		if gotRoot != root || path != filepath.Join(root, "decide.sock") {
			t.Fatalf("sent to %s %s", gotRoot, path)
		}
		sent = append(sent, req)
		return reply, nil
	}
	err = runSeatClean([]string{"--root", root, "--seat", " dev "}, &out)
	if !errors.As(err, &coded) || coded.code != ExitCommitRefused || len(sent) != 1 || sent[0].Seat != "dev" {
		t.Fatalf("a refused clean: %v, sent=%+v", err, sent)
	}
	if lines := strings.Split(err.Error(), "\n"); len(lines) != 2 || lines[0] != "seat clean refused: "+reply.Detail || strings.TrimSpace(lines[1]) != "code: not_lead" {
		t.Fatalf("the refusal's words and code: %q", err.Error())
	}
	reply = schedproc.DecideReply{OK: true, Detail: "seat dev's worktree was cleaned"}
	out.Reset()
	if err := runSeatClean([]string{"--root", root, "--seat", "dev"}, &out); err != nil || strings.TrimSpace(out.String()) != "seat dev's worktree was cleaned" {
		t.Fatalf("an accepted clean: %v, out=%q", err, out.String())
	}
	out.Reset()
	if err := runSeatClean([]string{"--root", root, "--seat", "dev", "--json"}, &out); err != nil || !strings.Contains(out.String(), `"seat_clean"`) || !strings.Contains(out.String(), `"ok": true`) {
		t.Fatalf("the json envelope: %v, out=%q", err, out.String())
	}
}
