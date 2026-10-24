//go:build unix

package cli

import "syscall"

// execProcess replaces this process with the embedded pi (absolute path, the
// prepared environment). It returns only on failure.
func execProcess(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}
