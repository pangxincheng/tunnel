package svc

import (
	"encoding/xml"
	"strconv"
	"strings"
	"time"
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
	m.bootoutLaunchd(s.Name)
	// 旧服务刚卸载完时 launchd 偶尔仍拒绝同名 bootstrap,稍后重试。
	var err error
	for i := 0; i < bootstrapTries; i++ {
		if i > 0 {
			m.sleep(bootstrapRetry)
		}
		if err = m.r.Run("launchctl", "bootstrap", m.domain(), p); err == nil {
			return nil
		}
	}
	return err
}

func (m *manager) uninstallLaunchd(name string) error {
	m.bootoutLaunchd(name)
	return m.fs.Remove(m.plistPath(name))
}

const (
	bootoutPoll    = 200 * time.Millisecond
	bootoutPolls   = 50 // 最多等 10 秒
	bootstrapTries = 3
	bootstrapRetry = time.Second
)

// bootoutLaunchd 卸载已加载的服务并等它真正消失。bootout 是异步的:命令返回时
// 旧进程可能还在退出(共享进程退出前要撤销 VPS 上的 socket),此时 bootstrap
// 同名服务会失败("Bootstrap failed: 5: Input/output error")。未加载时
// bootout 失败,直接返回;等满 10 秒仍在则交给 bootstrap 的重试。
func (m *manager) bootoutLaunchd(name string) {
	target := m.domain() + "/" + plistLabel(name)
	if m.r.Run("launchctl", "bootout", target) != nil {
		return
	}
	for i := 0; i < bootoutPolls; i++ {
		if m.r.Run("launchctl", "print", target) != nil {
			return
		}
		m.sleep(bootoutPoll)
	}
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
