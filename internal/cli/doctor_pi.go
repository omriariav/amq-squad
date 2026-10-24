package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

// doctorCheckPiRuntime is the `pi runtime` row (plan 4.1, 16.2, brief 4-1):
// the full re-hash of the unpacked runtime against the embedded manifest,
// and the runtimes kept under ~/.amq-squad/runtime. With --fix it removes a
// rejected current runtime (the next launch unpacks it again) and every
// runtime beyond the last two (ruling 3). Without --fix it writes nothing.
func doctorCheckPiRuntime(fix bool) []doctorCheck {
	const name = "pi runtime"
	e, err := piLoad()
	if errors.Is(err, piruntime.ErrNotEmbedded) {
		return []doctorCheck{{Name: name, Status: doctorOK, Detail: "not embedded in this build (pi lead unavailable)"}}
	}
	if err != nil {
		return []doctorCheck{{Name: name, Status: doctorFail, Kind: "pi_runtime_manifest", Detail: err.Error()}}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return []doctorCheck{{Name: name, Status: doctorFail, Detail: err.Error()}}
	}
	base := piruntime.DefaultBase(home)
	dir := filepath.Join(base, e.Manifest.DirName())
	var checks []doctorCheck
	switch tm, verr := piruntime.Verify(home, e); {
	case isNotExist(dir):
		checks = append(checks, doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("pi %s not unpacked yet; the first lead or pi seat launch unpacks it", e.Manifest.PiVersion)})
	case verr == nil:
		checks = append(checks, doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("pi %s, node %s: %d files re-hashed in %.0f ms", e.Manifest.PiVersion, e.Manifest.NodeVersion, tm.Files, ms(tm.Rehash))})
	case fix:
		if rerr := os.RemoveAll(dir); rerr != nil {
			_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && d.IsDir() {
					_ = os.Chmod(p, 0o700)
				}
				return nil
			})
			rerr = os.RemoveAll(dir)
			if rerr != nil {
				checks = append(checks, doctorCheck{Name: name, Status: doctorFail, Kind: "pi_runtime_rejected", Detail: fmt.Sprintf("%v; removing it failed: %v", verr, rerr)})
				break
			}
		}
		checks = append(checks, doctorCheck{Name: name, Status: doctorWarn, Kind: "pi_runtime_repaired", Detail: fmt.Sprintf("rejected runtime removed (%v); the next launch unpacks it again", verr)})
	default:
		checks = append(checks, doctorCheck{Name: name, Status: doctorFail, Kind: "pi_runtime_rejected", Detail: verr.Error() + " (run doctor --fix to remove it)"})
	}
	list, _ := piruntime.List(base, e.Manifest.DirName())
	var old []string
	for i, rt := range list {
		if i >= piruntime.KeepRuntimes && !rt.Current {
			old = append(old, rt.Name)
		}
	}
	switch {
	case len(old) == 0:
		checks = append(checks, doctorCheck{Name: "pi runtime versions", Status: doctorOK, Detail: fmt.Sprintf("%d kept under %s", len(list), base)})
	case fix:
		removed, err := piruntime.Prune(base, e.Manifest.DirName(), piruntime.KeepRuntimes)
		st, detail := doctorOK, "removed "+strings.Join(removed, ", ")
		if err != nil {
			st, detail = doctorFail, fmt.Sprintf("%s; %v", detail, err)
		}
		checks = append(checks, doctorCheck{Name: "pi runtime versions", Status: st, Detail: detail})
	default:
		checks = append(checks, doctorCheck{Name: "pi runtime versions", Status: doctorWarn, Kind: "pi_runtime_old",
			Detail: fmt.Sprintf("%d older than the last %d: %s (doctor --fix removes them)", len(old), piruntime.KeepRuntimes, strings.Join(old, ", "))})
	}
	return checks
}

func isNotExist(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, os.ErrNotExist)
}
