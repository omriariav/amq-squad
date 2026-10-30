package schedproc

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/usagelimit"
)

// Limits is the scheduler's usage-limit source: 2b-3's detector over the
// owner's home, plus every Claude seat's own config dir (each Claude seat
// writes its session files under its CLAUDE_CONFIG_DIR/projects, not the
// owner's ~/.claude/projects), found again at every poll because each
// launch gets a fresh dir.
type Limits struct {
	Home  usagelimit.Detector
	Root  string // the session root
	globs func(pattern string) ([]string, error)
}

func (l Limits) UsageLimitEvents(since time.Time) ([]scheduler.UsageLimitEvent, error) {
	out, err := l.Home.UsageLimitEvents(since)
	if err != nil {
		return nil, err
	}
	glob := l.globs
	if glob == nil {
		glob = filepath.Glob
	}
	dirs, _ := glob(filepath.Join(launch.StagedDir(l.Root, "*"), "claude-config", "*", "projects"))
	for _, d := range dirs {
		evs, err := usagelimit.Detector{ClaudeProjects: d}.UsageLimitEvents(since)
		if err != nil {
			return nil, err
		}
		out = append(out, evs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

var _ scheduler.UsageLimitSource = Limits{}
