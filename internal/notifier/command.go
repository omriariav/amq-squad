package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/attention"
)

type CommandSink struct {
	SinkID  string
	Argv    []string
	Timeout time.Duration
	Run     func(context.Context, string, []string, []byte, []string) error
}

func (s CommandSink) ID() string { return s.SinkID }
func (s CommandSink) Deliver(ctx context.Context, e attention.Event) error {
	if len(s.Argv) == 0 {
		return fmt.Errorf("empty command argv")
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	env := []string{"AMQ_SQUAD_NOTIFY_EVENT=" + e.EventType, "AMQ_SQUAD_NOTIFY_PROFILE=" + e.Profile, "AMQ_SQUAD_NOTIFY_SESSION=" + e.Session}
	if s.Run != nil {
		return s.Run(ctx, s.Argv[0], s.Argv[1:], body, env)
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command sink: %w: %.512s", err, stderr.String())
	}
	return nil
}
