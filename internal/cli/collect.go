package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCollect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ session/workstream name")
	meFlag := fs.String("me", "", "AMQ handle to collect for")
	timeoutFlag := fs.String("timeout", "0", "maximum time to wait for one message after an empty drain (0 = do not wait)")
	includeBody := fs.Bool("include-body", false, "include message bodies in drain output")
	projectFlag := fs.String("project", "", "project/team-home directory to resolve AMQ from (default: cwd)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad collect - drain once, optionally wait once, then drain once

Usage:
  amq-squad collect --session S --me HANDLE [--timeout D] [--include-body] [--project DIR]

Resolves the workstream AMQ root like 'amq-squad amq drain', then performs the
fixed report-collection procedure:
  1. Run one 'amq drain'.
  2. If that drain is empty and --timeout is greater than zero, run one bounded
     'amq watch --timeout D'.
  3. After that watch returns, run one final 'amq drain'.

This command deliberately does not poll. With the default --timeout 0 it drains
once and exits.

Examples:
  amq-squad collect --session issue-96 --me cto --include-body
  amq-squad collect --session issue-96 --me cto --timeout 60s --include-body
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*sessionFlag) == "" {
		return usageErrorf("collect requires --session")
	}
	if strings.TrimSpace(*meFlag) == "" {
		return usageErrorf("collect requires --me")
	}
	timeout, err := time.ParseDuration(*timeoutFlag)
	if err != nil {
		return usageErrorf("invalid --timeout %q: %v", *timeoutFlag, err)
	}
	if timeout < 0 {
		return usageErrorf("--timeout must be non-negative")
	}
	ctx, err := resolveAMQContext(*projectFlag, *sessionFlag, *meFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	return executeCollect(os.Stdout, ctx, timeout, *includeBody)
}

func executeCollect(out io.Writer, ctx amqContext, timeout time.Duration, includeBody bool) error {
	if out == nil {
		out = os.Stdout
	}
	first, err := runCollectDrain(ctx, includeBody)
	if err != nil {
		return err
	}
	if _, err := out.Write(first); err != nil {
		return err
	}
	if len(bytes.TrimSpace(first)) > 0 || timeout <= 0 {
		return nil
	}
	if err := runCollectWatch(ctx, timeout); err != nil {
		return err
	}
	final, err := runCollectDrain(ctx, includeBody)
	if err != nil {
		return err
	}
	_, err = out.Write(final)
	return err
}

func runCollectDrain(ctx amqContext, includeBody bool) ([]byte, error) {
	cmd := []string{"drain", "--root", ctx.Root, "--me", ctx.Me}
	if includeBody {
		cmd = append(cmd, "--include-body")
	}
	out, err := runAMQCommand(amqCommandRequest{Dir: ctx.ProjectDir, Env: amqCommandEnv(ctx), Arg: cmd})
	if err != nil {
		return nil, fmt.Errorf("collect drain: %w", err)
	}
	return out, nil
}

func runCollectWatch(ctx amqContext, timeout time.Duration) error {
	cmd := []string{"watch", "--root", ctx.Root, "--me", ctx.Me, "--timeout", timeout.String()}
	if _, err := runAMQCommand(amqCommandRequest{Dir: ctx.ProjectDir, Env: amqCommandEnv(ctx), Arg: cmd}); err != nil {
		if isCollectWatchTimeout(err) {
			return nil
		}
		return fmt.Errorf("collect watch: %w", err)
	}
	return nil
}

func isCollectWatchTimeout(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 4 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no new messages") && strings.Contains(msg, "timeout")
}
