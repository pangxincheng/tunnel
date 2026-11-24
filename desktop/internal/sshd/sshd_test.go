package sshd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
)

// listen starts a listener that writes banner to every connection.
func listen(t *testing.T, banner string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte(banner))
			c.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// refuse22 makes dial refuse port 22 so results do not depend on this host.
func refuse22(t *testing.T) {
	t.Helper()
	orig := dial
	t.Cleanup(func() { dial = orig })
	dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "127.0.0.1:22" {
			return nil, errors.New("refused")
		}
		return orig(ctx, network, addr)
	}
}

func cfg(s string) func() ([]byte, error) {
	return func() ([]byte, error) { return []byte(s), nil }
}

func TestDetectBanner(t *testing.T) {
	refuse22(t)
	p := listen(t, "SSH-2.0-OpenSSH_9\r\n")
	got := Detect(context.Background(), "linux", cfg(fmt.Sprintf("Port %d\n", p)))
	if !got.Running || got.Port != p {
		t.Fatalf("got %+v, want running on %d", got, p)
	}
}

func TestDetectNonSSH(t *testing.T) {
	refuse22(t)
	p := listen(t, "HTTP/1.1 400 Bad Request\r\n")
	got := Detect(context.Background(), "linux", cfg(fmt.Sprintf("Port %d\n", p)))
	if got.Running {
		t.Fatalf("got %+v, want not running", got)
	}
}

func TestDetectConfigParse(t *testing.T) {
	refuse22(t)
	p := listen(t, "SSH-2.0-x\r\n")
	conf := fmt.Sprintf("# Port 1\n   #Port 2\nPort notanumber\nport   %d  \nPORT 99999\n", p)
	got := Detect(context.Background(), "darwin", cfg(conf))
	if !got.Running || got.Port != p {
		t.Fatalf("got %+v, want running on %d", got, p)
	}
}

func TestParsePorts(t *testing.T) {
	got := parsePorts([]byte("Port 2222\r\n\tpOrT 2200\nPort 2222\nPort 0\nPort 22 # c\nInclude x\n"))
	want := []int{2222, 2200, 22}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDetectFallback22AndNilConfig(t *testing.T) {
	orig := dial
	t.Cleanup(func() { dial = orig })
	p := listen(t, "SSH-2.0-x\r\n")
	dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "127.0.0.1:22" {
			return orig(ctx, network, fmt.Sprintf("127.0.0.1:%d", p))
		}
		return nil, errors.New("refused")
	}
	got := Detect(context.Background(), "linux", nil)
	if !got.Running || got.Port != 22 {
		t.Fatalf("got %+v", got)
	}
	got = Detect(context.Background(), "linux", func() ([]byte, error) { return nil, errors.New("x") })
	if !got.Running || got.Port != 22 {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectCanceled(t *testing.T) {
	refuse22(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Detect(ctx, "linux", nil).Running {
		t.Fatal("running with canceled ctx")
	}
}

func TestDefaultConfigReader(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	if DefaultConfigReader("windows") == nil || DefaultConfigReader("linux") == nil {
		t.Fatal("nil reader")
	}
}
