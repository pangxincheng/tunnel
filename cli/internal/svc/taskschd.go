package svc

import (
	"strings"
	"unicode/utf16"
)

func (m *manager) taskXMLPath(name string) string {
	return PathsFor(m.mode, name, "windows", m.home).ConfigDir + `\task.xml`
}

func (m *manager) installTask(s Spec) error {
	p := m.taskXMLPath(s.Name)
	if err := m.fs.WriteFile(p, encodeUTF16LE(renderTaskXML(s))); err != nil {
		return err
	}
	if err := m.r.Run("schtasks", "/Create", "/TN", s.Name, "/XML", p, "/F"); err != nil {
		return err
	}
	// 任务已在运行时 /Run 会被 IgnoreNew 忽略;先 /End(未运行时失败,忽略)以应用新定义。
	_ = m.r.Run("schtasks", "/End", "/TN", s.Name)
	return m.r.Run("schtasks", "/Run", "/TN", s.Name)
}

func (m *manager) uninstallTask(name string) error {
	_ = m.r.Run("schtasks", "/End", "/TN", name) // 任务未在运行时会失败,忽略
	if err := m.r.Run("schtasks", "/Delete", "/TN", name, "/F"); err != nil {
		return err
	}
	return m.fs.Remove(m.taskXMLPath(name))
}

// encodeUTF16LE 带 BOM,schtasks 要求任务 XML 与其 encoding="UTF-16" 声明一致。
func encodeUTF16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2+len(u)*2)
	b = append(b, 0xFF, 0xFE)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// winQuote 按 CommandLineToArgvW 规则引用一个参数。
func winQuote(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"") {
		return a
	}
	var b strings.Builder
	b.WriteByte('"')
	bs := 0
	for _, r := range a {
		switch r {
		case '\\':
			bs++
			b.WriteRune(r)
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, bs+1))
		}
		bs = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat(`\`, bs)) // 结尾反斜杠须翻倍,以免转义收尾引号
	b.WriteByte('"')
	return b.String()
}

func renderTaskXML(s Spec) string {
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = winQuote(a)
	}
	// 不带 UserId 的登录触发器表示「任何用户登录」,注册需要管理员权限;
	// 限定为当前用户后普通用户即可注册。
	userID := ""
	if s.User != "" {
		userID = "\n      <UserId>" + xmlEsc(s.User) + "</UserId>"
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Tunnel service ` + xmlEsc(s.Name) + `</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>` + userID + `
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">` + userID + `
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Hidden>true</Hidden>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEsc(s.Exec) + `</Command>
      <Arguments>` + xmlEsc(strings.Join(args, " ")) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}
