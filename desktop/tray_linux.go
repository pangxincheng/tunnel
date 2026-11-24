//go:build linux

package main

import "github.com/godbus/dbus/v5"

// trayAvailable reports whether a StatusNotifier host is running (GNOME
// without the AppIndicator extension has none).
func trayAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var has bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has); err != nil {
		return false
	}
	return has
}
