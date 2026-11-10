package buzz

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

const evidenceAnswerAgainEnv = "AMQ_SQUAD_EVIDENCE_ANSWER_AGAIN"

func evidenceAnswerAgain() bool { return os.Getenv(evidenceAnswerAgainEnv) == "1" }

func TestAnswerAgainEvidenceIsOffByDefault(t *testing.T) {
	t.Setenv(evidenceAnswerAgainEnv, "")
	if evidenceAnswerAgain() {
		t.Fatal("answer-again evidence branch is on without its variable")
	}
	t.Setenv(evidenceAnswerAgainEnv, "1")
	if !evidenceAnswerAgain() {
		t.Fatal("answer-again evidence branch did not turn on explicitly")
	}
}

// mailboxTimeline samples names and modification times only. It never reads a
// prompt body. The one-second cadence distinguishes a prompt that never
// arrived from one that moved through new while the attach was waiting.
type mailboxTimeline struct {
	mu    sync.Mutex
	lines []string
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func startMailboxTimeline(ctx context.Context, root, handle string) *mailboxTimeline {
	m := &mailboxTimeline{stop: make(chan struct{}), done: make(chan struct{})}
	dirs := []string{
		filepath.Join(root, "agents", handle, "inbox", "new"),
		filepath.Join(root, "agents", handle, "inbox", "cur"),
		filepath.Join(root, "agents", handle, "dlq", "new"),
		filepath.Join(root, "agents", handle, "dlq", "cur"),
	}
	sample := func() {
		at := time.Now().UTC().Format(time.RFC3339Nano)
		for _, dir := range dirs {
			entries, err := os.ReadDir(dir)
			var names []string
			for _, entry := range entries {
				info, infoErr := entry.Info()
				if infoErr != nil {
					names = append(names, entry.Name()+"@stat-error")
					continue
				}
				names = append(names, entry.Name()+"@"+info.ModTime().UTC().Format(time.RFC3339Nano))
			}
			sort.Strings(names)
			line := fmt.Sprintf("%s %s names=%v err=%v", at, dir, names, err)
			m.mu.Lock()
			m.lines = append(m.lines, line)
			m.mu.Unlock()
		}
	}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		sample()
		for {
			select {
			case <-ticker.C:
				sample()
			case <-ctx.Done():
				return
			case <-m.stop:
				return
			}
		}
	}()
	return m
}

func (m *mailboxTimeline) finish() string {
	m.once.Do(func() { close(m.stop) })
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.lines, "\n")
}

type mailboxJournalEntry struct {
	MessageID   string          `json:"message_id"`
	Created     string          `json:"created"`
	Prompt      json.RawMessage `json:"prompt"`
	Undelivered string          `json:"undelivered"`
	Cancelled   bool            `json:"cancelled"`
	Acked       bool            `json:"acked"`
}

// captureMailboxJournals records only the fields needed to classify an open
// request. The prompt is represented by a boolean and its text is never read
// into the test log. The nonblocking lock probe releases immediately when it
// acquires the lock; "free" therefore means the probe held it for that one
// instant, not that no submit could start immediately afterwards.
func captureMailboxJournals(t *testing.T, label, root string) {
	t.Helper()
	dir := filepath.Join(root, "extensions", "remote", "mailbox", DefaultShareSession)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Logf("%s mailbox journals: %v", label, err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".outcome.json") {
			continue
		}
		base := strings.TrimSuffix(name, ".json")
		if _, err := os.Stat(filepath.Join(dir, base+".outcome.json")); err == nil {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, name))
		var got mailboxJournalEntry
		decodeErr := json.Unmarshal(raw, &got)
		promptPresent := len(got.Prompt) > 0 && string(got.Prompt) != "null"
		lockPath := filepath.Join(dir, base+".lock")
		lock, acquired, lockErr := flock.TryExclusive(lockPath, false)
		state := "held"
		if acquired {
			state = "free (the probe acquired and released it for this instant)"
			if lock != nil {
				_ = lock.Close()
			}
		}
		t.Logf("%s mailbox journal %s: undelivered=%q cancelled=%t created=%q message_id=%q acked=%t prompt_present=%t read_err=%v decode_err=%v lock=%s lock_err=%v",
			label, name, got.Undelivered, got.Cancelled, got.Created, got.MessageID, got.Acked, promptPresent, readErr, decodeErr, state, lockErr)
	}
}

func spawnedServePIDs(root int) ([]int, map[int]string, error) {
	if root <= 0 {
		return nil, nil, fmt.Errorf("invalid supervisor pid %d", root)
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, nil, err
	}
	children := map[int][]int{}
	commands := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pErr := strconv.Atoi(fields[0])
		ppid, ppErr := strconv.Atoi(fields[1])
		if pErr != nil || ppErr != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
		commands[pid] = strings.Join(fields[2:], " ")
	}
	var serves []int
	queue := []int{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			queue = append(queue, child)
			cmd := commands[child]
			if strings.Contains(cmd, "amq-remote") && strings.Contains(cmd, " serve") {
				serves = append(serves, child)
			}
		}
	}
	return serves, commands, nil
}

// signalSpawnedServes signals only serve processes descended from the
// supervisor pid file created by this test. It never searches by process name
// outside that tree, so the live squad's amq-remote cannot be selected.
func signalSpawnedServes(t *testing.T, label string, root int, upLog string) {
	t.Helper()
	serves, commands, err := spawnedServePIDs(root)
	if err != nil {
		t.Logf("%s serve SIGQUIT: supervisor=%d serves=%v err=%v", label, root, serves, err)
		return
	}
	for _, pid := range serves {
		t.Logf("%s serve SIGQUIT: supervisor=%d pid=%d command=%q", label, root, pid, commands[pid])
		_ = syscall.Kill(pid, syscall.SIGQUIT)
	}
	if len(serves) == 0 {
		t.Logf("%s serve SIGQUIT: supervisor=%d has no reachable serve child", label, root)
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(upLog)
		if bytesContainGoroutineDump(raw) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func bytesContainGoroutineDump(raw []byte) bool {
	text := string(raw)
	return strings.Contains(text, "SIGQUIT") || strings.Contains(text, "goroutine ")
}

func captureOwnerHang(t *testing.T, label, shareRoot, buzzDir string, relay *wsRelay, serveRoot int) {
	t.Helper()
	captureMailboxJournals(t, label, shareRoot)
	if serveRoot > 0 {
		signalSpawnedServes(t, label, serveRoot, filepath.Join(shareRoot, "attach", "up.log"))
	} else {
		raw, err := os.ReadFile(filepath.Join(buzzDir, "enroll-up.pid"))
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || parseErr != nil || pid <= 0 {
			t.Logf("%s serve SIGQUIT: no held supervisor pid (read_err=%v parse_err=%v)", label, err, parseErr)
		} else {
			signalSpawnedServes(t, label, pid, filepath.Join(shareRoot, "attach", "up.log"))
		}
	}
	for _, path := range []string{filepath.Join(shareRoot, "attach", "up.log"), filepath.Join(buzzDir, "doctor.json")} {
		raw, err := os.ReadFile(path)
		t.Logf("%s %s:\n%s\nread_err=%v", label, path, raw, err)
	}
	t.Logf("%s relay event trace:\n%s", label, relay.eventTrace())
}

// awaitWithin is the answer-again measurement's bounded wait. It does not
// fail the test when the first answer hangs because the captures and the
// second answer must run before the final assertion.
func awaitWithin(ctx context.Context, b *bell, d time.Duration, ok func() bool) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		rung := b.next()
		if ok() {
			return true
		}
		select {
		case <-rung:
		case <-timer.C:
			return ok()
		case <-ctx.Done():
			return ok()
		}
	}
}
