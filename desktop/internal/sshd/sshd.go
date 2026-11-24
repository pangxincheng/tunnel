// Package sshd detects whether a local sshd is listening and on which port.
package sshd

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Result is the detection outcome.
type Result struct {
	Running bool `json:"running"`
	Port    int  `json:"port"`
}

// dial is replaceable in tests.
var dial = (&net.Dialer{}).DialContext

const probeTimeout = time.Second

// DefaultConfigReader returns a reader of the sshd_config for goos.
func DefaultConfigReader(goos string) func() ([]byte, error) {
	path := "/etc/ssh/sshd_config"
	if goos == "windows" {
		path = filepath.Join(os.Getenv("ProgramData"), "ssh", "sshd_config")
	}
	return func() ([]byte, error) { return os.ReadFile(path) }
}

// parsePorts returns the deduplicated Port values in file order.
// Include directives are not followed.
func parsePorts(conf []byte) []int {
	var ports []int
	seen := map[int]bool{}
	for _, line := range strings.Split(string(conf), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "Port") {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 1 || n > 65535 || seen[n] {
			continue
		}
		seen[n] = true
		ports = append(ports, n)
	}
	return ports
}

// Detect probes the ports from the sshd config (plus 22) on 127.0.0.1 and
// returns the first one answering with an SSH banner. goos is currently
// informational; the caller picks the reader via DefaultConfigReader.
func Detect(ctx context.Context, goos string, readConfig func() ([]byte, error)) Result {
	var ports []int
	if readConfig != nil {
		if conf, err := readConfig(); err == nil {
			ports = parsePorts(conf)
		}
	}
	has22 := false
	for _, p := range ports {
		if p == 22 {
			has22 = true
		}
	}
	if !has22 {
		ports = append(ports, 22)
	}
	for _, p := range ports {
		if ctx.Err() != nil {
			break
		}
		if probe(ctx, p) {
			return Result{Running: true, Port: p}
		}
	}
	return Result{}
}

func probe(ctx context.Context, port int) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := dial(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer conn.Close()
	dl, _ := ctx.Deadline()
	conn.SetDeadline(dl)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	line, _ := bufio.NewReaderSize(conn, 256).Peek(4)
	return bytes.Equal(line, []byte("SSH-"))
}
