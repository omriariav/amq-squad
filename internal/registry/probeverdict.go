package registry

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The R12 probe's predicate as a verdict (v3-5-verifier rule 3, the lead's
// ruling on it): every run yields a task.Verdict built the way the commit
// step's are: verifier "r12", pass exactly when the predicate holds, the
// tree of the probe's checkout (written from the run directory after the
// model's processes are gone, its git metadata left out: it is the model's
// to edit), and the command digest over the predicate version and the
// prompt. doctor runs the probe outside any session: its verdicts carry no
// seal and say so. When the probe runs as a scheduled task (milestone B's
// probe role through the scheduler and the commit step) the harness records
// the run's verdict on that task's lease with RecordProbeVerdict: the
// session seed seals it through task.Store.RecordVerdict like any verifier's,
// and the task completes only on it.

// ProbeVerifier is the verifier name of a probe run's own verdict.
const ProbeVerifier = "r12"

// ProbeSourceUnsealed is the source of a doctor probe run's verdict.
const ProbeSourceUnsealed = "r12 probe predicate " + ProbePredicate + " (doctor, outside a session: unsealed)"

// ProbeSourceScheduled is the source of a scheduled probe task's verdict.
const ProbeSourceScheduled = "r12 probe predicate " + ProbePredicate + " (scheduled task)"

// PredicateDigest is the probe's command digest: the predicate version and
// the prompt.
func PredicateDigest() string { return task.CommandDigest(ProbePredicate + "\n" + ProbePrompt) }

// PredicateVerdict is the run's predicate as a verdict of the named verifier
// (ProbeVerifier for doctor's record; the task class's verifier for a
// scheduled probe task), unsealed.
func (r ProbeRun) PredicateVerdict(verifier, source string) task.Verdict {
	v := task.Verdict{Verifier: verifier, Result: task.VerdictFail, Source: source, Tree: r.Tree, CommandDigest: PredicateDigest()}
	if r.Pass {
		v.Result = task.VerdictPass
	}
	var failed []string
	for name, ok := range r.Checks {
		if !ok {
			failed = append(failed, name)
		}
	}
	sort.Strings(failed)
	if len(failed) > 0 {
		v.Detail = "failed checks: " + strings.Join(failed, ", ")
	}
	if r.FalseSuccess {
		v.Detail = strings.TrimSpace(v.Detail + "; a false success")
	}
	return v
}

// RecordProbeVerdict is the harness's record of a scheduled probe task's
// run: the probe checkout's tree as the lease's snapshot, then the run's
// predicate as the verdict of the task class's verifier, sealed by the
// store with the session seed.
func RecordProbeVerdict(s task.Store, id string, run ProbeRun, now time.Time) (task.Task, error) {
	t, err := s.Get(id)
	if err != nil {
		return t, err
	}
	if run.Tree == "" {
		return t, fmt.Errorf("the probe run carries no checkout tree")
	}
	if _, err := s.RecordSnapshot(id, run.Tree, now); err != nil {
		return t, err
	}
	return s.RecordVerdict(id, run.PredicateVerdict(task.ClassVerifier(t.Class), ProbeSourceScheduled), now)
}

// probeTree is the tree of a probe run directory: every regular file and
// symbolic link outside .git, hashed with no filter into a scratch object
// store of the probe's own (never the run directory's repository).
func probeTree(ctx context.Context, dir string) (string, error) {
	gd, err := os.MkdirTemp("", "amq-squad-r12-tree-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(gd)
	env := []string{"GIT_DIR=" + gd, "GIT_INDEX_FILE=" + filepath.Join(gd, "index"), "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_ATTR_NOSYSTEM=1", "HOME=" + gd, "LC_ALL=C", "PATH=" + os.Getenv("PATH")}
	git := func(stdin []byte, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.attributesFile=" + os.DevNull}, args...)...)
		cmd.Dir, cmd.Env = gd, env
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git(nil, "init", "-q", "--bare", gd); err != nil {
		return "", err
	}
	var info bytes.Buffer
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		var oid, mode string
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if oid, err = git([]byte(target), "hash-object", "-w", "--no-filters", "--stdin"); err != nil {
				return err
			}
			mode = "120000"
		case d.Type().IsRegular():
			st, err := d.Info()
			if err != nil {
				return err
			}
			if oid, err = git(nil, "hash-object", "-w", "--no-filters", "--", p); err != nil {
				return err
			}
			mode = "100644"
			if st.Mode().Perm()&0o100 != 0 {
				mode = "100755"
			}
		default:
			return nil
		}
		fmt.Fprintf(&info, "%s %s\t%s\x00", mode, oid, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	if info.Len() > 0 {
		if _, err := git(info.Bytes(), "update-index", "-z", "--add", "--index-info"); err != nil {
			return "", err
		}
	}
	return git(nil, "write-tree")
}
