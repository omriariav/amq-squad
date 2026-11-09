package flock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// JSONLFileLockName names the advisory lock used by append-only JSONL
// writers. The lock is the JSONL file itself, not a sidecar, so every opener
// of one log coordinates on the same inode.
func JSONLFileLockName(path string) string { return path }

// WithJSONLFile opens path and holds its named exclusive lock for fn. A
// caller may scan, repair a torn tail and append while no other writer can
// interleave.
func WithJSONLFile(path string, fn func(*os.File) error) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return WithFile(f, JSONLFileLockName(path), func() error { return fn(f) })
}

// TerminateJSONLTail appends a newline when an already locked JSONL file has
// an unterminated tail. Durability is the caller's concern.
func TerminateJSONLTail(f *os.File) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			n, err := f.Write([]byte{'\n'})
			if err == nil && n != 1 {
				err = io.ErrShortWrite
			}
			return err
		}
	}
	return nil
}

// AppendJSONLLine appends line as one whole line to an already locked JSONL
// file. If a crashed writer left a torn tail, it first terminates that tail so
// the new record cannot be glued to it. Durability is the caller's concern.
func AppendJSONLLine(f *os.File, line []byte) error {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	if len(line) == 0 || bytes.ContainsRune(line, '\n') {
		return fmt.Errorf("JSONL record must be exactly one non-empty line")
	}
	if err := TerminateJSONLTail(f); err != nil {
		return err
	}
	buf := append(append(make([]byte, 0, len(line)+1), line...), '\n')
	n, err := f.Write(buf)
	if err == nil && n != len(buf) {
		err = io.ErrShortWrite
	}
	return err
}

// AppendJSONL encodes v, appends it under the JSONL file lock and fsyncs the
// file before releasing the lock.
func AppendJSONL(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return AppendJSONLBytes(path, buf.Bytes())
}

// AppendJSONLBytes appends one encoded record under the JSONL file lock and
// fsyncs the file before releasing the lock.
func AppendJSONLBytes(path string, line []byte) error {
	return WithJSONLFile(path, func(f *os.File) error {
		return errors.Join(AppendJSONLLine(f, line), f.Sync())
	})
}

// JSONLWriter adapts a locked, durable JSONL path to io.Writer. Each Write
// must contain exactly one encoded line and takes the named file lock.
type JSONLWriter struct{ Path string }

func (w JSONLWriter) Write(p []byte) (int, error) {
	err := AppendJSONLBytes(w.Path, p)
	if err != nil {
		return len(p), err
	}
	return len(p), nil
}
