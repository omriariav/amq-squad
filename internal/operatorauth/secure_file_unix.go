//go:build unix

package operatorauth

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func secureReadAuthorizationFile(path string, private bool, maxSize int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("path must be absolute and clean")
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(components) == 0 || components[len(components)-1] == "" {
		return nil, fmt.Errorf("path must name a file")
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	current := fd
	for _, component := range components[:len(components)-1] {
		if component == "" || component == "." || component == ".." {
			return nil, fmt.Errorf("path contains an invalid component")
		}
		next, openErr := unix.Openat(current, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			return nil, fmt.Errorf("unsafe ancestor %q", component)
		}
		if current != fd {
			unix.Close(current)
		}
		current = next
	}
	if current != fd {
		defer unix.Close(current)
	}
	name := components[len(components)-1]
	fileFD, err := unix.Openat(current, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open no-follow file")
	}
	f := os.NewFile(uintptr(fileFD), name)
	if f == nil {
		unix.Close(fileFD)
		return nil, fmt.Errorf("open file descriptor")
	}
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fileFD, &st); err != nil {
		return nil, fmt.Errorf("stat opened file")
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("file must be an owner-controlled regular single-link file")
	}
	mode := os.FileMode(st.Mode).Perm()
	if private && mode != 0o600 {
		return nil, fmt.Errorf("private key mode must be exactly 0600")
	}
	if !private && mode&0o022 != 0 {
		return nil, fmt.Errorf("trust store must not be group/world writable")
	}
	if st.Size < 0 || st.Size > maxSize {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || int64(len(data)) > maxSize {
		return nil, fmt.Errorf("read bounded file")
	}
	var after unix.Stat_t
	if err := unix.Fstat(fileFD, &after); err != nil || after.Dev != st.Dev || after.Ino != st.Ino || after.Size != st.Size {
		zeroBytes(data)
		return nil, fmt.Errorf("file identity changed during read")
	}
	return data, nil
}
