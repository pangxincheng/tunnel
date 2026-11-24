package logtail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sink collects everything Follow emits.
type sink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *sink) write(b []byte) { s.mu.Lock(); s.buf.Write(b); s.mu.Unlock() }
func (s *sink) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.buf.String() }

func waitFor(t *testing.T, s *sink, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.String() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("got %q, want %q", s.String(), want)
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func follow(t *testing.T, path string, lines int) (*sink, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &sink{}
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, lines, 10*time.Millisecond, s.write) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, done
}

func TestFollowTailThenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.log")
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	os.WriteFile(path, []byte(b.String()), 0o600)
	s, _ := follow(t, path, 3)
	waitFor(t, s, "line 8\nline 9\nline 10\n")
	appendTo(t, path, "line 11\n")
	waitFor(t, s, "line 8\nline 9\nline 10\nline 11\n")
}

func TestFollowShortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.log")
	os.WriteFile(path, []byte("only\n"), 0o600)
	s, _ := follow(t, path, 1000)
	waitFor(t, s, "only\n")
}

func TestFollowTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.log")
	os.WriteFile(path, []byte("old 1\nold 2\n"), 0o600)
	s, _ := follow(t, path, 10)
	waitFor(t, s, "old 1\nold 2\n")
	os.WriteFile(path, []byte("new\n"), 0o600) // rotated / truncated in place
	waitFor(t, s, "old 1\nold 2\nnew\n")
}

func TestFollowMissingFile(t *testing.T) {
	err := Follow(context.Background(), filepath.Join(t.TempDir(), "none.log"), 10, time.Millisecond, func([]byte) {})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err %v", err)
	}
}

func TestTailStartSkipsPartialFirstLine(t *testing.T) {
	data := []byte("aaaa\nbbbb\ncccc\n")
	if got := string(data[tailStart(data, 2, true):]); got != "bbbb\ncccc\n" {
		t.Fatalf("%q", got)
	}
	// Window cut mid-line (not the file start): its first partial line is dropped.
	if got := string(data[tailStart(data[2:], 10, false)+2:]); got != "bbbb\ncccc\n" {
		t.Fatalf("%q", got)
	}
}

func TestFollowCmd(t *testing.T) {
	s := &sink{}
	err := FollowCmd(context.Background(), "sh", []string{"-c", "echo out; echo err >&2"}, s.write)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String(); !strings.Contains(got, "out\n") || !strings.Contains(got, "err\n") {
		t.Fatalf("%q", got)
	}
}
