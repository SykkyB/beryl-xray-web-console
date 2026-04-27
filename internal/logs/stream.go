package logs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"
)

// Streamer follows a log file as it grows and pushes each appended
// line into the consumer's channel. It detects truncation / rotation
// (file shrinks below our cursor, or inode/path replaced) and reopens
// from the start, so it survives `> /var/log/sing-box.log` redirects
// and procd-style rotation.
//
// Polling-based on purpose: OpenWrt may not have inotify and the log
// volume here is tiny.
type Streamer struct {
	Path     string
	Interval time.Duration // default 500ms
}

func (s *Streamer) interval() time.Duration {
	if s.Interval == 0 {
		return 500 * time.Millisecond
	}
	return s.Interval
}

// Follow opens the file, emits each appended line on out, returns
// when ctx is cancelled.
//
// Implementation note: each tick we re-Stat the path, then if the file
// has grown, open it fresh and ReadAt(pos). Avoiding bufio + a
// long-lived file handle dodges a macOS edge case where reads past
// EOF on an open handle fail to see appended bytes without a re-seek.
func (s *Streamer) Follow(ctx context.Context, out chan<- string) error {
	t := time.NewTicker(s.interval())
	defer t.Stop()

	var (
		pos    int64  // bytes already emitted
		ino    uint64 // last seen inode
		carry  []byte // partial line carried across reads (no trailing \n)
		opened bool
	)

	// Initial seek-to-end so we don't dump the entire historical log
	// to the first subscriber. Backfill is the HTTP layer's job.
	if st, err := os.Stat(s.Path); err == nil {
		pos = st.Size()
		ino = inodeOf(st)
		opened = true
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}

		st, err := os.Stat(s.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// File gone; reset, wait for it to come back.
				pos = 0
				carry = nil
				opened = false
				continue
			}
			continue
		}
		newIno := inodeOf(st)
		size := st.Size()

		// Detect rotation: size shrunk, or inode changed, or we
		// hadn't opened yet.
		if !opened || size < pos || (ino != 0 && newIno != 0 && newIno != ino) {
			pos = 0
			carry = nil
			ino = newIno
			opened = true
			// Don't break — fall through and read from 0.
		} else {
			ino = newIno
		}

		if size <= pos {
			continue
		}

		// Read everything new in one go. log files here are small;
		// even a 1MB delta is fine.
		f, err := os.Open(s.Path)
		if err != nil {
			continue
		}
		buf := make([]byte, size-pos)
		n, _ := f.ReadAt(buf, pos)
		f.Close()
		buf = buf[:n]
		pos += int64(n)

		// Prepend any carry-over from the previous read.
		if len(carry) > 0 {
			buf = append(carry, buf...)
			carry = nil
		}

		// Split into lines. Trailing chunk without \n is carried.
		for {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				if len(buf) > 0 {
					carry = append(carry[:0], buf...)
				}
				break
			}
			line := buf[:i]
			if l := len(line); l > 0 && line[l-1] == '\r' {
				line = line[:l-1]
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- string(line):
			}
			buf = buf[i+1:]
		}
	}
}
