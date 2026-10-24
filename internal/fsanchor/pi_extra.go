package fsanchor

import (
	"fmt"
	"os"
)

// OpenPath opens the regular file at rel below d through descriptors,
// without following a symlink at any component (4-1: the identity re-check
// of the pi launch set right before exec).
func OpenPath(d *AnchoredDir, rel []string) (*os.File, error) {
	if len(rel) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	cur := d
	closeCur := func() {
		if cur != d {
			cur.Close()
		}
	}
	for _, part := range rel[:len(rel)-1] {
		f, kind, _, _, err := cur.OpenEntry(part)
		if err == nil && kind != KindDir {
			f.Close()
			err = fmt.Errorf("not a directory")
		}
		if err != nil {
			closeCur()
			return nil, fmt.Errorf("%s: %w", part, err)
		}
		next, err := AsDir(f)
		closeCur()
		if err != nil {
			return nil, err
		}
		cur = next
	}
	f, kind, _, _, err := cur.OpenEntry(rel[len(rel)-1])
	closeCur()
	if err != nil {
		return nil, err
	}
	if kind != KindRegular {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", rel[len(rel)-1])
	}
	return f, nil
}
