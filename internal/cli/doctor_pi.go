package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// The pi runtime rows of doctor (plan 4.1, 16.2, brief 4-1). The main row is
// the full re-hash of the runtime this binary embeds, opened from HOME
// through descriptors, against the embedded manifest; the host probe
// (doctor_host.go) shows it and runs the R12 probe from the Runtime it
// returns. The versions row lists the runtimes kept under
// ~/.amq-squad/runtime. With --fix doctor removes a rejected current runtime
// (the next launch unpacks it again) and every runtime beyond the last two
// (ruling 3). Without --fix these rows write nothing.

// syncRegistryRuntimePin gives 2b-3's registry the embedded runtime's
// manifest digest and directory name (the trust root this binary carries),
// so detection and RehashRuntime use the runtime this binary embeds.
func syncRegistryRuntimePin() {
	if e, err := piLoad(); err == nil {
		registry.EmbeddedRuntimeManifestSHA256 = e.Digest
		registry.EmbeddedRuntimeDirName = e.Manifest.DirName()
	}
}

// doctorCheckPiRuntime is the main row and the versions row together (used
// when doctor runs without the host probe, and by tests).
func doctorCheckPiRuntime(fix bool) []doctorCheck {
	e, err := piLoad()
	if errors.Is(err, piruntime.ErrNotEmbedded) {
		return []doctorCheck{{Name: "pi runtime", Status: doctorOK, Detail: "not embedded in this build (pi lead unavailable)"}}
	}
	if err != nil {
		return []doctorCheck{{Name: "pi runtime", Status: doctorFail, Kind: "pi_runtime_manifest", Detail: err.Error()}}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return []doctorCheck{{Name: "pi runtime", Status: doctorFail, Detail: err.Error()}}
	}
	main, _ := doctorPiRuntimeRow(home, e, fix)
	return append([]doctorCheck{main}, doctorPiRuntimeVersions(home, e, fix)...)
}

// doctorPiRuntimeVersionsRows is the versions row alone (doctor with the
// host probe, which shows the main row itself).
func doctorPiRuntimeVersionsRows(fix bool) []doctorCheck {
	e, err := piLoad()
	if err != nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return doctorPiRuntimeVersions(home, e, fix)
}

// doctorPiRuntimeRow verifies the embedded runtime under home and returns
// the row and, when it verified, the Runtime.
func doctorPiRuntimeRow(home string, e piruntime.Embedded, fix bool) (doctorCheck, *piruntime.Runtime) {
	const name = "pi runtime"
	dir := filepath.Join(piruntime.DefaultBase(home), e.Manifest.DirName())
	rt, tm, verr := piruntime.Open(home, e)
	switch {
	case errors.Is(verr, fs.ErrNotExist):
		return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("pi %s not unpacked yet; the first lead or pi seat launch unpacks it", e.Manifest.PiVersion)}, nil
	case verr == nil:
		return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("pi %s, node %s: %d files re-hashed in %.0f ms", e.Manifest.PiVersion, e.Manifest.NodeVersion, tm.Files, ms(tm.Rehash))}, &rt
	case fix:
		if rerr := removeRuntimeDir(dir); rerr != nil {
			return doctorCheck{Name: name, Status: doctorFail, Kind: "pi_runtime_rejected", Detail: fmt.Sprintf("%v; removing it failed: %v", verr, rerr)}, nil
		}
		return doctorCheck{Name: name, Status: doctorWarn, Kind: "pi_runtime_repaired", Detail: fmt.Sprintf("rejected runtime removed (%v); the next launch unpacks it again", verr)}, nil
	default:
		return doctorCheck{Name: name, Status: doctorFail, Kind: "pi_runtime_rejected", Detail: verr.Error() + " (run doctor --fix to remove it)"}, nil
	}
}

func removeRuntimeDir(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// doctorPiRuntimeVersions is the versions row, with the --fix prune.
func doctorPiRuntimeVersions(home string, e piruntime.Embedded, fix bool) []doctorCheck {
	base := piruntime.DefaultBase(home)
	list, _ := piruntime.List(base, e.Manifest.DirName())
	var old []string
	for i, rt := range list {
		if i >= piruntime.KeepRuntimes && !rt.Current {
			old = append(old, rt.Name)
		}
	}
	switch {
	case len(old) == 0:
		return []doctorCheck{{Name: "pi runtime versions", Status: doctorOK, Detail: fmt.Sprintf("%d kept under %s", len(list), base)}}
	case fix:
		removed, err := piruntime.Prune(base, e.Manifest.DirName(), piruntime.KeepRuntimes)
		st, detail := doctorOK, "removed "+strings.Join(removed, ", ")
		if err != nil {
			st, detail = doctorFail, fmt.Sprintf("%s; %v", detail, err)
		}
		return []doctorCheck{{Name: "pi runtime versions", Status: st, Detail: detail}}
	default:
		return []doctorCheck{{Name: "pi runtime versions", Status: doctorWarn, Kind: "pi_runtime_old",
			Detail: fmt.Sprintf("%d older than the last %d: %s (doctor --fix removes them)", len(old), piruntime.KeepRuntimes, strings.Join(old, ", "))}}
	}
}

func isNotExist(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, os.ErrNotExist)
}
