package svc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
)

// getenv / getuid 可在测试中替换。
var (
	getenv = os.Getenv
	getuid = os.Getuid
)

// maxSockPath 是控制 socket 完整路径的上限(macOS sun_path 可用 103 字节,留余量)。
const maxSockPath = 100

// controlSock 返回 dir 下服务 svc 的控制 socket 路径:通常为 <svc>.sock;
// 完整路径超过 maxSockPath 时文件名改用 hex(sha256(svc))[:12].sock。
func controlSock(dir, svc string) string {
	p := dir + "/" + svc + ".sock"
	if len(p) <= maxSockPath {
		return p
	}
	sum := sha256.Sum256([]byte(svc))
	return dir + "/" + hex.EncodeToString(sum[:])[:12] + ".sock"
}

// userRunDir 是 user 模式控制 socket 的短目录 /tmp/tunnel-<uid>(ControlListen 校验其属主与 0700)。
func userRunDir() string { return "/tmp/tunnel-" + strconv.Itoa(getuid()) }

// Paths 是某个服务实例在各平台上的固定路径。
type Paths struct {
	ConfigDir   string
	StatePath   string
	ControlAddr string // 控制通道地址;Unix 下总在专用目录内(system 监听时会 chown 其父目录,user 模式要求其属于当前用户且为 0700)
	BinPath     string
	LogPath     string
}

// PathsFor 返回 svc(ServiceName 的结果)在给定平台与模式下的路径。
// Windows 的 BinPath 按服务分开:多个服务可各自停止、替换自己的 tunnel.exe。
func PathsFor(mode Mode, svc string, goos string, home string) Paths {
	switch {
	case goos == "windows":
		cfg := home + `\AppData\Roaming\tunnel\` + svc
		return Paths{
			ConfigDir:   cfg,
			StatePath:   cfg + `\state.json`,
			ControlAddr: `\\.\pipe\tunnel-` + svc,
			BinPath:     home + `\AppData\Local\tunnel\` + svc + `\tunnel.exe`,
			LogPath:     cfg + `\tunnel.log`,
		}
	case mode == ModeSystem:
		p := Paths{
			ConfigDir:   "/etc/tunnel/" + svc,
			StatePath:   "/var/lib/tunnel/" + svc + "/state.json",
			ControlAddr: controlSock("/run/tunnel", svc),
			BinPath:     "/usr/local/bin/tunnel",
		}
		if goos == "darwin" {
			p.ControlAddr = controlSock("/var/run/tunnel", svc)
			p.LogPath = "/var/log/tunnel/" + svc + ".log"
		}
		return p
	case goos == "darwin":
		cfg := home + "/Library/Application Support/tunnel/" + svc
		return Paths{
			ConfigDir:   cfg,
			StatePath:   cfg + "/state.json",
			ControlAddr: controlSock(userRunDir(), svc), // 家目录下的路径可能超出 sun_path
			BinPath:     home + "/Library/Application Support/tunnel/bin/tunnel",
			LogPath:     cfg + "/tunnel.log",
		}
	default:
		cfg := home + "/.config/tunnel/" + svc
		ctl := controlSock(userRunDir(), svc)
		if x := getenv("XDG_RUNTIME_DIR"); x != "" {
			ctl = controlSock(x+"/tunnel", svc)
		}
		return Paths{
			ConfigDir:   cfg,
			StatePath:   cfg + "/state.json",
			ControlAddr: ctl,
			BinPath:     home + "/.local/bin/tunnel",
		}
	}
}
