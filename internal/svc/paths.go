package svc

import "os"

// getenv 可在测试中替换。
var getenv = os.Getenv

// Paths 是某个服务实例在各平台上的固定路径。
type Paths struct {
	ConfigDir   string
	StatePath   string
	ControlAddr string // 控制通道地址;Unix 下总在专用的 tunnel/ 目录或服务专属目录内(监听时会 chown 其父目录)
	BinPath     string
	LogPath     string
}

// PathsFor 返回 svc(ServiceName 的结果)在给定平台与模式下的路径。
func PathsFor(mode Mode, svc string, goos string, home string) Paths {
	switch {
	case goos == "windows":
		cfg := home + `\AppData\Roaming\tunnel\` + svc
		return Paths{
			ConfigDir:   cfg,
			StatePath:   cfg + `\state.json`,
			ControlAddr: `\\.\pipe\tunnel-` + svc,
			BinPath:     home + `\AppData\Local\tunnel\tunnel.exe`,
			LogPath:     cfg + `\tunnel.log`,
		}
	case mode == ModeSystem:
		p := Paths{
			ConfigDir:   "/etc/tunnel/" + svc,
			StatePath:   "/var/lib/tunnel/" + svc + "/state.json",
			ControlAddr: "/run/tunnel/" + svc + ".sock",
			BinPath:     "/usr/local/bin/tunnel",
		}
		if goos == "darwin" {
			p.ControlAddr = "/var/run/tunnel/" + svc + ".sock"
			p.LogPath = "/var/log/tunnel/" + svc + ".log"
		}
		return p
	case goos == "darwin":
		cfg := home + "/Library/Application Support/tunnel/" + svc
		return Paths{
			ConfigDir:   cfg,
			StatePath:   cfg + "/state.json",
			ControlAddr: cfg + "/control.sock",
			BinPath:     home + "/Library/Application Support/tunnel/bin/tunnel",
			LogPath:     cfg + "/tunnel.log",
		}
	default:
		cfg := home + "/.config/tunnel/" + svc
		ctl := cfg + "/control.sock"
		if x := getenv("XDG_RUNTIME_DIR"); x != "" {
			ctl = x + "/tunnel/" + svc + ".sock"
		}
		return Paths{
			ConfigDir:   cfg,
			StatePath:   cfg + "/state.json",
			ControlAddr: ctl,
			BinPath:     home + "/.local/bin/tunnel",
		}
	}
}
