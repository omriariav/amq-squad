//go:build !unix

package operatorauth

import (
	"fmt"
	"os"
	"path/filepath"
)

func secureReadAuthorizationFile(path string, private bool, maxSize int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("path must be absolute and clean")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxSize {
		return nil, fmt.Errorf("file is not a bounded regular file")
	}
	if private && info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("private key mode must be exactly 0600")
	}
	if !private && info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("trust store must not be group/world writable")
	}
	return os.ReadFile(path)
}
