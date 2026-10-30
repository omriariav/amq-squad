//go:build !unix

package registry

import "errors"

func mkfifo(string) error { return errors.New("no FIFOs off unix") }
