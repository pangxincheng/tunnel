package svc

import (
	"encoding/xml"
	"strconv"
	"strings"
)

func plistLabel(name string) string { return "com.pangxincheng." + name }

func (m *manager) plistPath(name string) string {
	if m.mode == ModeSystem {
		return "/Library/LaunchDaemons/" + plistLabel(name) + ".plist"
	}
	return m.home + "/Library/LaunchAgents/" + plistLabel(name) + ".plist"
}

func (m *manager) domain() string {
	if m.mode == ModeSystem {
		return "system"
	}
	return "gui/" + strconv.Itoa(m.uid())
}

func (m *manager) installLaunchd(s Spec) error {
	s.Mode = m.mode
	p := m.plistPath(s.Name)
	if err := m.fs.WriteFile(p, []byte(renderLaunchdPlist(s))); err != nil {
		return err
	}
	// 已加载则先卸载;未加载时 bootout 会失败,忽略。
	_ = m.r.Run("launchctl", "bootout", m.domain()+"/"+plistLabel(s.Name))
	return m.r.Run("launchctl", "bootstrap", m.domain(), p)
}

func (m *manager) uninstallLaunchd(name string) error {
	_ = m.r.Run("launchctl", "bootout", m.domain()+"/"+plistLabel(name))
	return m.fs.Remove(m.plistPath(name))
}

func xmlEsc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func renderLaunchdPlist(s Spec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlEsc(plistLabel(s.Name)) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEsc(s.Exec) + `</string>
`)
	for _, a := range s.Args {
		b.WriteString("\t\t<string>" + xmlEsc(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
`)
	if s.LogPath != "" {
		b.WriteString("\t<key>StandardOutPath</key>\n\t<string>" + xmlEsc(s.LogPath) + "</string>\n")
		b.WriteString("\t<key>StandardErrorPath</key>\n\t<string>" + xmlEsc(s.LogPath) + "</string>\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}
