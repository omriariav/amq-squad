//go:build !unix

package cli

import (
	"fmt"
	"os"
)

var namespaceLockBeforeContainedCreate = func(string, string) error { return nil }

func openNamespaceNoFollow(string, bool) (*os.File, os.FileInfo, error) {
	return nil, nil, fmt.Errorf("no-follow filesystem inspection is unsupported on this platform")
}

func openContainedNamespaceLock(string, string) (*os.File, error) {
	return nil, fmt.Errorf("contained no-follow namespace lock creation is unsupported on this platform")
}
