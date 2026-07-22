//go:build windows

package liveidentity

import "fmt"

func symlinkDir(_, _ string) error { return fmt.Errorf("test symlink not configured") }
