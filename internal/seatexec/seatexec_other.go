//go:build !unix

package seatexec

import "errors"

// Run is unix-only: the anchored traversal needs openat with O_NOFOLLOW and
// the provider is exec'd in place. amq-squad supports the Mac (R45).
func Run(o Options) error {
	return errors.New("seat-exec is supported on unix hosts only")
}

func execProvider(path string, argv, env []string) error {
	return errors.New("seat-exec is supported on unix hosts only")
}
