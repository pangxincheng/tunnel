package svc

import (
	"strings"
)

func (m *manager) unitPath(name string) string {
	if m.mode == ModeSystem {
		return "/etc/systemd/system/" + name + ".service"
	}
	return m.home + "/.config/systemd/user/" + name + ".service"
}

func (m *manager) systemctl(args ...string) error {
	if m.mode == ModeUser {
		args = append([]string{"--user"}, args...)
	}
	return m.r.Run("systemctl", args...)
}

func (m *manager) installSystemd(s Spec) error {
	s.Mode = m.mode
	if err := m.fs.WriteFile(m.unitPath(s.Name), []byte(renderSystemdUnit(s))); err != nil {
		return err
	}
	if err := m.systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := m.systemctl("enable", "--now", s.Name); err != nil {
		return err
	}
	// 重复安装 = 更新:enable --now 不会重启已在运行的单元,显式 restart 以应用新定义。
	return m.systemctl("restart", s.Name)
}

func (m *manager) uninstallSystemd(name string) error {
	// 服务可能已停用,disable 失败不阻止清理文件。
	_ = m.systemctl("disable", "--now", name)
	if err := m.fs.Remove(m.unitPath(name)); err != nil {
		return err
	}
	return m.systemctl("daemon-reload")
}

func renderSystemdUnit(s Spec) string {
	parts := make([]string, 0, len(s.Args)+1)
	parts = append(parts, systemdQuote(s.Exec))
	for _, a := range s.Args {
		parts = append(parts, systemdQuote(a))
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Tunnel service " + s.Name + "\n")
	if s.Mode == ModeSystem {
		b.WriteString("After=network-online.target\nWants=network-online.target\n")
	}
	b.WriteString("\n[Service]\nExecStart=" + strings.Join(parts, " ") + "\n")
	b.WriteString("Restart=on-failure\nRestartSec=10\n\n[Install]\n")
	if s.Mode == ModeSystem {
		b.WriteString("WantedBy=multi-user.target\n")
	} else {
		b.WriteString("WantedBy=default.target\n")
	}
	return b.String()
}

// systemdQuote 按 systemd 规则引用一个 ExecStart 词:含空白/引号/反斜杠时加双引号并转义;
// % 与 $ 总是翻倍(systemd 会做说明符与变量展开)。
func systemdQuote(a string) string {
	a = strings.ReplaceAll(a, "%", "%%")
	a = strings.ReplaceAll(a, "$", "$$")
	if a != "" && !strings.ContainsAny(a, " \t\n\"'\\;") {
		return a
	}
	a = strings.ReplaceAll(a, `\`, `\\`)
	a = strings.ReplaceAll(a, `"`, `\"`)
	return `"` + a + `"`
}
