package main

import (
	"errors"
	"strings"

	"github.com/pangxincheng/tunnel/cli/tunnel"
	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/core"
	"github.com/pangxincheng/tunnel/desktop/internal/probe"
	"github.com/pangxincheng/tunnel/desktop/internal/sharesvc"
)

// errCode returns the stable code the frontend matches on, or "" when the
// error has none.
func errCode(err error) string {
	var (
		auth *probe.AuthError
		dial *probe.DialError
		sock *probe.SocketDirError
		lis  *tunnel.LocalListenError
	)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, probe.ErrPassphrase):
		return "passphrase"
	case errors.As(err, &auth):
		return "auth"
	case errors.As(err, &dial):
		return "dial"
	case errors.As(err, &sock):
		return "socketdir"
	case errors.Is(err, sharesvc.ErrCancelled):
		return "cancelled"
	case errors.Is(err, sharesvc.ErrNotRunning):
		return "not_running"
	case errors.Is(err, config.ErrPortRange):
		return "port_invalid"
	case errors.Is(err, config.ErrPortConflict):
		return "port_conflict"
	case errors.Is(err, core.ErrDeviceOffline):
		return "offline"
	case errors.Is(err, core.ErrNoSSHD):
		return "no_sshd"
	case errors.Is(err, core.ErrNameTaken):
		return "name_taken"
	case errors.As(err, &lis):
		return "port_busy"
	case errors.Is(err, core.ErrUnknownServer), errors.Is(err, core.ErrUnknownDevice):
		return "unknown"
	}
	return ""
}

// coded is an error whose message starts with its stable code ("code: msg").
type coded struct {
	code string
	err  error
}

func (e *coded) Error() string { return e.code + ": " + e.err.Error() }
func (e *coded) Unwrap() error { return e.err }

// wrapErr prefixes err's message with its code; errors without a code (and
// nil) are returned unchanged.
func wrapErr(err error) error {
	c := errCode(err)
	if c == "" || strings.HasPrefix(err.Error(), c+":") {
		return err
	}
	return &coded{code: c, err: err}
}
