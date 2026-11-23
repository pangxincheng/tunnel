package main

import "testing"

func TestParseTunnelVersion(t *testing.T) {
	for in, want := range map[string]string{
		"tunnel 1.2.0\n": "1.2.0",
		"tunnel v1.2.0":  "v1.2.0",
		"tunnel dev\n":   "dev",
		"":               "",
		"tunnel":         "",
		"error: boom":    "",
	} {
		if got := parseTunnelVersion(in); got != want {
			t.Errorf("parseTunnelVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
