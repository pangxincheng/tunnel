//go:build !windows

package autostart

import "errors"

var errNoRegistry = errors.New("autostart: registry is only available on Windows")

func realRegSet(name, value string) error   { return errNoRegistry }
func realRegDel(name string) error          { return errNoRegistry }
func realRegGet(name string) (string, bool) { return "", false }
