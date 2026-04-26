// Package logs reads the tail of a log file efficiently for the panel's
// /api/logs endpoint. We don't shell out to `tail -n N` because that
// adds another fork per request; instead we seek backwards from the end
// of the file in 4 KB chunks until we have enough newlines.
package logs

import (
	"bytes"
	"errors"
	"io"
	"os"
)

// Tail returns the last n lines of the file at path. Lines are returned
// in chronological order (oldest first). Trailing empty line from a
// final newline is dropped. A missing file is treated as empty.
func Tail(path string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size == 0 {
		return nil, nil
	}

	const chunk = 4096
	var data []byte
	pos := size
	newlines := 0

	// Read backwards in chunks, prepending each block, until we have
	// n+1 newlines (we need n+1 because counting from end we want to
	// SKIP the partial line before the first kept newline).
	for pos > 0 && newlines <= n {
		readSize := int64(chunk)
		if pos < readSize {
			readSize = pos
		}
		pos -= readSize

		buf := make([]byte, readSize)
		if _, err := f.ReadAt(buf, pos); err != nil && err != io.EOF {
			return nil, err
		}
		data = append(buf, data...)
		newlines = bytes.Count(data, []byte{'\n'})
	}

	// Split, drop trailing empty (file ended with \n), keep last n.
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out, nil
}
