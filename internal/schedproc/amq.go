package schedproc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The doorbell is an AMQ message (ruling 15: the AMQ wake as the doorbell):
// a seat's AMQ wake delivers it into the seat's terminal. Like every
// doorbell it is a hint: the lease, not the mail, decides (2b-1), and the
// lead's notice of a refusal follows the store record.

// Handle is the scheduler's own mailbox handle, the sender of every
// doorbell and lead notice. Status does not list it as a seat.
const Handle = scheduler.SenderHandle

// Sender sends one AMQ message as the scheduler.
type Sender struct {
	AMQ      string // the installed amq (R51)
	Root     string // AM_ROOT: the session root
	BaseRoot string // AM_BASE_ROOT
	Session  string // AM_SESSION
	// Run runs amq; default exec with a 20 s timeout. Tests replace it.
	Run func(ctx context.Context, amq string, args, env []string) ([]byte, error)
}

func (s Sender) send(to, kind, subject, body string) error {
	run := s.Run
	if run == nil {
		run = defaultRun
	}
	// A harness subprocess gets no credential (milestone B first pass P2).
	env := append(redact.StripEnv(scrubAMQ(os.Environ())), "AM_ME="+Handle, "AM_ROOT="+s.Root, "AMQ_NO_UPDATE_CHECK=1")
	if s.BaseRoot != "" {
		env = append(env, "AM_BASE_ROOT="+s.BaseRoot)
	}
	if s.Session != "" {
		env = append(env, "AM_SESSION="+s.Session)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := []string{"send", "--me", Handle, "--to", to, "--kind", kind, "--subject", subject, "--body", body}
	if out, err := run(ctx, firstNonEmpty(s.AMQ, "amq"), args, env); err != nil {
		return fmt.Errorf("amq send to %s: %v: %s", to, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func defaultRun(ctx context.Context, amq string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, amq, args...)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// scrubAMQ drops the caller's AMQ identity: the scheduler sends as itself.
func scrubAMQ(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "AM_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Doorbell rings a seat with an AMQ message naming its new lease. Start,
// when set, runs first: it puts the seat's worktree on the task's own branch
// (seatcommit.StartTask, 3.0.2 t23) before the seat is told. A Start error
// rings nothing: Ring returns it as a scheduler.LeaseUnpreparedError, and
// the scheduler releases the lease and blocks the task for the lead
// (task_branch_unprepared), so no seat works on the resting branch.
type Doorbell struct {
	Sender
	Start func(handle string, t task.Task) error
}

func (d Doorbell) Ring(handle string, t task.Task) error {
	if d.Start != nil {
		if err := d.Start(handle, t); err != nil {
			return &scheduler.LeaseUnpreparedError{Err: fmt.Errorf("seat %s could not be put on task %s's branch: %w", handle, t.ID, err)}
		}
	}
	return d.ring(handle, t)
}

func (d Doorbell) ring(handle string, t task.Task) error {
	body := fmt.Sprintf("The scheduler leased you task %s (%s, class %s). Claim it with: amq-squad task claim %s --me %s. Declared files: %s.",
		t.ID, t.Title, t.Class, t.ID, handle, orNone(strings.Join(t.DeclaredFiles, ", ")))
	return d.send(handle, "todo", "task "+t.ID+": "+t.Title, body)
}

// LeadNotice tells the lead that the commit step refused a task.
type LeadNotice struct {
	Sender
	Lead string
}

func (l LeadNotice) CommitRefused(t task.Task) error {
	if l.Lead == "" {
		return nil
	}
	body := fmt.Sprintf("The harness commit step refused task %s (%s): %s, routed to the %s. Detail: %s.",
		t.ID, t.Title, t.BlockReason, t.BlockRoute, t.BlockDetail)
	if t.BlockRoute == task.RouteLead {
		body += fmt.Sprintf(" Widen inside the grant with: amq-squad task widen %s --files <globs> --reason <why> (it reaches the scheduler as your AMQ message); or reset it.", t.ID)
	}
	return l.send(l.Lead, "status", "commit refused: "+t.ID+" "+t.BlockReason, body)
}

// SeatQuarantined tells the lead that a seat exit off the done path
// quarantined the seat (R3); the owner clears it.
func (l LeadNotice) SeatQuarantined(handle, taskID, reason, detail string) error {
	if l.Lead == "" {
		return nil
	}
	body := fmt.Sprintf("Seat %s is quarantined (%s) at its exit from task %s: %s. It gets no new lease until the owner clears it (amq-squad seat clear --seat %s, from the owner's terminal).",
		handle, reason, orNone(taskID), detail, handle)
	return l.send(l.Lead, "status", "seat quarantined: "+handle+" "+reason, body)
}

var (
	_ scheduler.Doorbell               = Doorbell{}
	_ scheduler.LeadNotifier           = LeadNotice{}
	_ scheduler.SeatQuarantineNotifier = LeadNotice{}
)

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
