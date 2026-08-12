package worktreeplan

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func inspectCoordinationState(set Set, worktree string) (bool, []string) {
	worktree = filepath.Clean(worktree)
	for _, root := range []string{set.TeamHome, set.ControlRoot, set.AMQRoot} {
		if pathWithin(filepath.Clean(root), worktree) {
			return true, nil
		}
	}
	for _, local := range []string{filepath.Join(worktree, ".amqrc"), filepath.Join(worktree, team.DirName, DirName)} {
		if pathExists(local) {
			return true, nil
		}
	}
	localAMQ := filepath.Join(worktree, ".agent-mail")
	if !pathExists(localAMQ) {
		return false, nil
	}
	if !removableAgentMailTree(set, localAMQ) {
		return true, nil
	}
	return false, []string{localAMQ}
}

func removableAgentMailTree(set Set, root string) bool {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	canonicalBase := filepath.Join(set.TeamHome, ".agent-mail")
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink residue")
		}
		entryInfo, err := entry.Info()
		if err != nil || !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("non-regular residue")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("residue escaped root")
		}
		if bootstrapConfig(path, rel) || canonicalCopy(set, canonicalBase, rel, path) {
			return nil
		}
		return fmt.Errorf("unique coordination state")
	})
	return err == nil
}

func bootstrapConfig(path, rel string) bool {
	if !strings.HasSuffix(filepath.ToSlash(rel), "/meta/config.json") && filepath.ToSlash(rel) != "meta/config.json" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return false
	}
	agents, ok := document["agents"]
	if !ok {
		return false
	}
	var handles []string
	return json.Unmarshal(agents, &handles) == nil
}

func canonicalCopy(set Set, canonicalBase, rel, localPath string) bool {
	candidates := []string{filepath.Join(canonicalBase, rel)}
	if alternate := alternateInboxState(rel); alternate != rel {
		candidates = append(candidates, filepath.Join(canonicalBase, alternate))
	}
	for _, prefix := range localAMQPrefixes(set) {
		suffix, ok := trimPathPrefix(rel, prefix)
		if !ok {
			continue
		}
		candidates = append(candidates, filepath.Join(set.AMQRoot, suffix))
		if alternate := alternateInboxState(suffix); alternate != suffix {
			candidates = append(candidates, filepath.Join(set.AMQRoot, alternate))
		}
	}
	for _, candidate := range candidates {
		if sameRegularFile(localPath, candidate) {
			return true
		}
	}
	return false
}

func localAMQPrefixes(set Set) []string {
	prefixes := []string{filepath.Join(normalizedProfile(set.Profile), set.Session), set.Session}
	if prefixes[0] == prefixes[1] {
		return prefixes[:1]
	}
	return prefixes
}

func trimPathPrefix(path, prefix string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(prefix), filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return rel, true
}

func alternateInboxState(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] != "inbox" {
			continue
		}
		switch parts[i] {
		case "new":
			parts[i] = "cur"
		case "cur":
			parts[i] = "new"
		default:
			continue
		}
		return filepath.FromSlash(strings.Join(parts, "/"))
	}
	return path
}

func sameRegularFile(left, right string) bool {
	leftInfo, err := os.Lstat(left)
	if err != nil || !leftInfo.Mode().IsRegular() {
		return false
	}
	rightInfo, err := os.Lstat(right)
	if err != nil || !rightInfo.Mode().IsRegular() || leftInfo.Size() != rightInfo.Size() {
		return false
	}
	leftDigest, leftOK := fileDigest(left)
	rightDigest, rightOK := fileDigest(right)
	return leftOK && rightOK && leftDigest == rightDigest
}

func fileDigest(path string) ([sha256.Size]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return [sha256.Size]byte{}, false
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, true
}

func removeCoordinationResidue(set Set, record Record, paths []string) error {
	for _, path := range paths {
		expected := filepath.Join(record.Path, ".agent-mail")
		if filepath.Clean(path) != filepath.Clean(expected) || !removableAgentMailTree(set, path) {
			return fmt.Errorf("refuse cleanup: coordination residue at %s is no longer removable", path)
		}
		var entries []string
		if err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("refuse symlink in coordination residue %s", current)
			}
			entries = append(entries, current)
			return nil
		}); err != nil {
			return fmt.Errorf("inspect removable coordination residue %s: %w", path, err)
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if err := os.Remove(entries[i]); err != nil {
				return fmt.Errorf("remove coordination residue %s: %w", entries[i], err)
			}
		}
	}
	return nil
}
