package buzz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
)

// AMQInbox is the handle's inbox through the amq CLI: `amq list --new`,
// `amq read` (moves the message to cur) and `amq reply`, the body on stdin,
// never in argv.
type AMQInbox struct {
	Bin    string // amq, default from PATH
	Root   string // the share root
	Handle string // MailboxHandle
}

func (a AMQInbox) run(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	bin := a.Bin
	if bin == "" {
		bin = "amq"
	}
	if a.Root == "" || a.Handle == "" {
		return nil, errors.New("the inbox needs the share root and the handle")
	}
	args = append(args, "--root", a.Root, "--me", a.Handle, "--json")
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Env = amqexec.NoUpdateCheckEnv(nil)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("amq %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

type amqHeader struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	Thread  string   `json:"thread"`
	Labels  []string `json:"labels"`
	Created string   `json:"created"`
}

// New lists the unread messages, oldest first.
func (a AMQInbox) New(ctx context.Context) ([]Prompt, error) {
	raw, err := a.run(ctx, "", "list", "--new")
	if err != nil {
		return nil, err
	}
	var rows []amqHeader
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("%w: amq list output does not parse: %v", ErrInboxUnreadable, err)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Created != rows[j].Created {
			return rows[i].Created < rows[j].Created
		}
		return rows[i].ID < rows[j].ID
	})
	out := make([]Prompt, 0, len(rows))
	for _, r := range rows {
		out = append(out, Prompt{ID: r.ID, From: r.From, Thread: r.Thread, Labels: r.Labels})
	}
	return out, nil
}

// Take reads one message; amq moves it to cur.
func (a AMQInbox) Take(ctx context.Context, id string) (Prompt, error) {
	raw, err := a.run(ctx, "", "read", "--id", id)
	if err != nil {
		return Prompt{}, err
	}
	var m struct {
		Body   string    `json:"body"`
		Header amqHeader `json:"header"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Header.ID != id {
		return Prompt{}, fmt.Errorf("%w: amq read %s output does not parse", ErrInboxUnreadable, id)
	}
	return Prompt{ID: id, From: m.Header.From, Thread: m.Header.Thread, Labels: m.Header.Labels, Body: m.Body}, nil
}

// Reply answers a message with `amq reply --kind answer|status`.
func (a AMQInbox) Reply(ctx context.Context, id, text string, status bool) error {
	kind := "answer"
	if status {
		kind = "status"
	}
	_, err := a.run(ctx, text, "reply", "--id", id, "--kind", kind, "--body", "-")
	return err
}
