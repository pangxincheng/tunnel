package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pangxincheng/tunnel/desktop/internal/core"
	"github.com/pangxincheng/tunnel/desktop/internal/logtail"
)

// logEvent carries LogChunks of share log streams to the frontend.
const logEvent = "share-log"

const (
	logLines = 1000 // lines shown when a stream starts
	logPoll  = 500 * time.Millisecond
)

// LogChunk is one piece of a share log stream. The last chunk of a stream
// has Done set (and Err if it failed).
type LogChunk struct {
	ID   int    `json:"id"`
	Data string `json:"data"`
	Done bool   `json:"done"`
	Err  string `json:"err"`
}

// StartLog starts streaming the share service log of server (its last 1000
// lines, then new lines as they are written) as "share-log" events and
// returns the stream id. A stream already running for server is stopped.
func (s *AppService) StartLog(server string) (int, error) {
	for _, v := range s.mgr.State().Servers {
		if v.Name == server {
			return s.startLogStream(server, v.Share.LogPath, v.Share.System)
		}
	}
	return 0, wrapErr(core.ErrUnknownServer)
}

// StopLog stops the stream id; unknown ids are ignored.
func (s *AppService) StopLog(id int) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	for server, st := range s.logs {
		if st.id == id {
			st.cancel()
			delete(s.logs, server)
		}
	}
}

type logStream struct {
	id     int
	cancel context.CancelFunc
}

// journalArgs follows the share unit in the systemd journal (Linux).
func journalArgs(server string, system bool) []string {
	args := []string{"-u", "tunnel-" + server, "-n", fmt.Sprint(logLines), "-f", "--no-pager", "-o", "short-iso"}
	if !system {
		args = append([]string{"--user"}, args...)
	}
	return args
}

func (s *AppService) startLogStream(server, path string, system bool) (int, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return 0, fmt.Errorf("nolog: %s", path)
		}
	}
	base := s.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	s.logMu.Lock()
	if s.logs == nil {
		s.logs = map[string]logStream{}
	}
	if old, ok := s.logs[server]; ok {
		old.cancel()
	}
	s.logSeq++
	id := s.logSeq
	s.logs[server] = logStream{id: id, cancel: cancel}
	s.logMu.Unlock()

	out := func(b []byte) { s.emit(logEvent, LogChunk{ID: id, Data: string(b)}) }
	go func() {
		var err error
		if path != "" {
			err = logtail.Follow(ctx, path, logLines, logPoll, out)
		} else {
			err = logtail.FollowCmd(ctx, "journalctl", journalArgs(server, system), out)
		}
		end := LogChunk{ID: id, Done: true}
		if err != nil {
			end.Err = err.Error()
		}
		s.emit(logEvent, end)
		s.StopLog(id)
	}()
	return id, nil
}
