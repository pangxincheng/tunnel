package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type emitted struct {
	mu     sync.Mutex
	chunks []LogChunk
}

func (e *emitted) emit(name string, data any) {
	if name != logEvent {
		return
	}
	e.mu.Lock()
	e.chunks = append(e.chunks, data.(LogChunk))
	e.mu.Unlock()
}

func (e *emitted) text(id int) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	done := false
	for _, c := range e.chunks {
		if c.ID == id {
			b.WriteString(c.Data)
			done = done || c.Done
		}
	}
	return b.String(), done
}

func TestLogStreamFileFollowAndStop(t *testing.T) {
	s, _ := newTestService(t)
	em := &emitted{}
	s.emit = em.emit
	path := filepath.Join(t.TempDir(), "tunnel.log")
	os.WriteFile(path, []byte("a\nb\n"), 0o600)
	id, err := s.startLogStream("home", path, false)
	if err != nil {
		t.Fatal(err)
	}
	waitText := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if got, _ := em.text(id); got == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		got, _ := em.text(id)
		t.Fatalf("got %q want %q", got, want)
	}
	waitText("a\nb\n")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("c\n")
	f.Close()
	waitText("a\nb\nc\n")

	// A second stream for the same server replaces the first.
	id2, err := s.startLogStream("home", path, false)
	if err != nil || id2 == id {
		t.Fatalf("id2 %d err %v", id2, err)
	}
	s.StopLog(id2)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, d1 := em.text(id)
		_, d2 := em.text(id2)
		if d1 && d2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("streams did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLogStreamMissingFile(t *testing.T) {
	s, _ := newTestService(t)
	s.emit = func(string, any) {}
	missing := filepath.Join(t.TempDir(), "gone.log")
	if _, err := s.startLogStream("home", missing, false); err == nil || err.Error() != "nolog: "+missing {
		t.Fatalf("err %v", err)
	}
	if _, err := s.StartLog("nope"); err == nil || !strings.HasPrefix(err.Error(), "unknown:") {
		t.Fatalf("err %v", err)
	}
}

func TestJournalArgs(t *testing.T) {
	got := strings.Join(journalArgs("home", false), " ")
	if got != "--user -u tunnel-home -n 1000 -f --no-pager -o short-iso" {
		t.Fatal(got)
	}
	if got := strings.Join(journalArgs("home", true), " "); strings.Contains(got, "--user") {
		t.Fatal(got)
	}
}
