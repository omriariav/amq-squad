package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

type launchFileSnapshot struct {
	Path   string
	Exists bool
	Data   []byte
	Mode   fs.FileMode
}

func captureLaunchFileSnapshot(path string) (launchFileSnapshot, error) {
	snapshot := launchFileSnapshot{Path: path}
	if strings.TrimSpace(path) == "" {
		return snapshot, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, nil
		}
		return snapshot, err
	}
	if info.IsDir() {
		return snapshot, fmt.Errorf("launch file path %s is a directory", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot, err
	}
	snapshot.Exists = true
	snapshot.Data = data
	snapshot.Mode = info.Mode().Perm()
	return snapshot, nil
}

func (s launchFileSnapshot) restore() error {
	if strings.TrimSpace(s.Path) == "" {
		return nil
	}
	if !s.Exists {
		if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	current, err := os.ReadFile(s.Path)
	if err == nil && string(current) == string(s.Data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.Path, s.Data, s.Mode)
}

// prepareSelectedAMQRoots materializes only the context selected by launch
// resolution. Default profiles need their base container so AMQ's --session
// lookup is valid; named profiles use and prepare only their deterministic
// exact root. The returned paths are exactly the directories this call
// created and can be removed safely on a clean pre-backend failure.
func prepareSelectedAMQRoots(preflights []agentLaunchPreflight, profile string) ([]string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	var created []string
	seen := map[string]bool{}
	for _, preflight := range preflights {
		selected := preflight.Root
		if profile == team.DefaultProfile {
			selected = preflight.BaseRoot
		}
		selected = filepath.Clean(strings.TrimSpace(selected))
		if selected == "" || selected == "." || seen[selected] {
			continue
		}
		seen[selected] = true
		paths, err := ensureLaunchDirectoryTracked(selected)
		created = append(created, paths...)
		if err != nil {
			return created, err
		}
	}
	return created, nil
}

func ensureLaunchDirectoryTracked(path string) ([]string, error) {
	path = filepath.Clean(path)
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return missing, fmt.Errorf("prepare AMQ root: %s exists and is not a directory", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return missing, fmt.Errorf("prepare AMQ root %s: %w", current, err)
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return missing, fmt.Errorf("prepare AMQ root %s: no existing ancestor", path)
		}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return missing, fmt.Errorf("prepare AMQ root %s: %w", path, err)
	}
	return missing, nil
}

func cleanupCreatedLaunchDirectories(paths []string) error {
	unique := map[string]bool{}
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path != "" && path != "." {
			unique[path] = true
		}
	}
	ordered := make([]string, 0, len(unique))
	for path := range unique {
		ordered = append(ordered, path)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] > ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	var cleanupErrs []error
	for _, path := range ordered {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove newly created AMQ directory %s: %w", path, err))
		}
	}
	return errors.Join(cleanupErrs...)
}
