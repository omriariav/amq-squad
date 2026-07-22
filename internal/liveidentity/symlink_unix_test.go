//go:build !windows

package liveidentity

import "os"

func symlinkDir(oldname, newname string) error { return os.Symlink(oldname, newname) }
