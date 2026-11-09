package seatcommit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The harness's git for a fetch of the grant's push URL, and the shadow
// git directory it reads and fetches in. Moved here from schedproc's
// launch-time base (slots.go, 3.0.2 t22) so the recovery's carry check
// (recover.go) and the launch's base fetch use one helper (the plan
// reviewer on #904): a shadow git directory of the harness's own on the
// repository's object store, so the shared repository's config, remotes
// and url rewrites are never read, with FetchCredentials (the pinned gh as
// the only credential helper, or anonymous) for an https URL.

// BaseGit is the harness's git with its R2 settings (no global or system
// config, hooks, attribute files, fsmonitor or replace objects; the push's
// environment, HarnessEnv) and the fetch's credential settings (Pre, from
// FetchCredentials). Env is the parent environment (nil: the process's).
type BaseGit struct {
	Bin string
	Pre []string
	Env []string
}

// Run runs one git call in dir with extra environment entries. It is
// unbounded, as the launch's fetch is (its bound is on the 3.2.x list);
// RunBounded is the bounded form the recovery uses.
func (g BaseGit) Run(dir string, extra []string, args ...string) (string, error) {
	return g.RunBounded(context.Background(), dir, extra, args...)
}

// RunBounded runs one git call under ctx in its own process group: when
// ctx ends the group is killed (git's helpers, ssh and a credential
// helper, go with it) and the error names the bound. The recovery's one
// transport call runs under RecoverTransportTimeout (Codex's P2 on #904:
// the decision server has cleared its deadline before the apply step, so
// an unreachable origin would otherwise hold the ledger lock and the
// handler for as long as git waits); a later PR can bound the launch's
// fetch the same way.
func (g BaseGit) RunBounded(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	argv := append(append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.attributesFile=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "protocol.ext.allow=never"}, g.Pre...), args...)
	cmd := exec.CommandContext(ctx, g.Bin, argv...)
	cmd.Dir = dir
	parent := g.Env
	if parent == nil {
		parent = os.Environ()
	}
	cmd.Env = append(harnessEnv(parent), extra...)
	ownGroup(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = verifierWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if cerr := ctx.Err(); cerr != nil && (err != nil || errors.Is(cerr, context.DeadlineExceeded)) {
		return "", fmt.Errorf("git %s did not answer within its bound; killed", args[0])
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// BaseShadow is a git directory of the harness's own (git's default
// config) on the repository's object store (Env: GIT_DIR and
// GIT_OBJECT_DIRECTORY). The caller removes Dir.
type BaseShadow struct {
	Dir string
	Env []string
}

// NewBaseShadow makes the shadow for the repository whose common git
// directory is common.
func NewBaseShadow(g BaseGit, common string) (BaseShadow, error) {
	dir, err := os.MkdirTemp("", "amq-squad-base-*")
	if err != nil {
		return BaseShadow{}, err
	}
	if _, err := g.Run(dir, nil, "init", "-q", "--template=", dir); err != nil {
		os.RemoveAll(dir)
		return BaseShadow{}, err
	}
	return BaseShadow{Dir: dir, Env: []string{"GIT_DIR=" + filepath.Join(dir, ".git"), "GIT_OBJECT_DIRECTORY=" + filepath.Join(common, "objects")}}, nil
}

// RecoverTransportTimeout bounds the one network call of a recovery (the
// ls-remote of origin's branch for a carried entry).
var RecoverTransportTimeout = 30 * time.Second
