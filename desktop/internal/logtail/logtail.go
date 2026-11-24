// Package logtail streams a log to a callback like `tail -n N -f`: from a
// file (macOS / Windows share service logs) or a command's output
// (`journalctl -f` on Linux).
package logtail

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

const (
	tailWindow = 512 << 10 // bytes read from the end of the file for the first N lines
	chunkSize  = 32 << 10
)

// Follow emits the last `lines` lines of path, then everything appended to
// it, checking every poll until ctx is done. A file that shrinks (truncated
// or replaced) is read again from the start. A missing file returns an error
// wrapping os.ErrNotExist; out is never called concurrently.
func Follow(ctx context.Context, path string, lines int, poll time.Duration, out func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	start := max(st.Size()-tailWindow, 0)
	window := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(window, start); err != nil && err != io.EOF {
		return err
	}
	if tail := window[tailStart(window, lines, start == 0):]; len(tail) > 0 {
		out(tail)
	}
	off := st.Size()

	buf := make([]byte, chunkSize)
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if cur, err := os.Stat(path); err == nil && (cur.Size() < off || !os.SameFile(cur, st)) {
			if nf, err := os.Open(path); err == nil { // rotated or truncated: start over
				f.Close()
				f, off = nf, 0
				st, _ = nf.Stat()
			}
		}
		for {
			n, err := f.ReadAt(buf, off)
			if n > 0 {
				out(append([]byte(nil), buf[:n]...))
				off += int64(n)
			}
			if err != nil || n < len(buf) {
				break
			}
		}
	}
}

// tailStart returns the offset in data where its last n lines begin. When
// data does not start at the beginning of the file its first, possibly
// partial line is never included.
func tailStart(data []byte, n int, atFileStart bool) int {
	end := len(data)
	if end > 0 && data[end-1] == '\n' {
		end-- // the final newline ends the last line
	}
	for i := 0; i < n; i++ {
		j := bytes.LastIndexByte(data[:end], '\n')
		if j < 0 {
			switch {
			case atFileStart:
				return 0
			case i == 0:
				return len(data) // only a partial line in the window
			}
			return end + 1 // drop the partial first line
		}
		end = j
	}
	return end + 1
}

// FollowCmd runs name with args and emits its stdout and stderr until it
// exits or ctx is done (which kills it). A command killed by ctx is not an error.
func FollowCmd(ctx context.Context, name string, args []string, out func([]byte)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	w := &serialWriter{out: out}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// serialWriter forwards writes to out; exec serializes writes when Stdout
// and Stderr are the same writer.
type serialWriter struct{ out func([]byte) }

func (w *serialWriter) Write(p []byte) (int, error) {
	w.out(append([]byte(nil), p...))
	return len(p), nil
}
