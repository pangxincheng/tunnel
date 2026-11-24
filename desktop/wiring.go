package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/pangxincheng/tunnel/cli/tunnel"
	"github.com/pangxincheng/tunnel/desktop/internal/bundled"
	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/core"
	"github.com/pangxincheng/tunnel/desktop/internal/sharesvc"
)

// configPathEnv overrides the config file location (isolated manual runs).
const configPathEnv = "TUNNEL_APP_CONFIG"

var errNoBundled = errors.New("未找到附带的 tunnel 程序")

// missingShare stands in for the share service when the bundled tunnel CLI
// is absent: nothing is installed, and every operation fails clearly.
type missingShare struct{}

func (missingShare) Status(context.Context, string) (sharesvc.State, error) {
	return sharesvc.State{}, nil
}
func (missingShare) Install(context.Context, sharesvc.InstallOpts) error { return errNoBundled }
func (missingShare) Uninstall(context.Context, string, bool) error       { return errNoBundled }
func (missingShare) Pause(context.Context, string) error                 { return errNoBundled }
func (missingShare) Resume(context.Context, string) error                { return errNoBundled }
func (missingShare) Retry(context.Context, string) error                 { return errNoBundled }
func (missingShare) SetName(context.Context, string, string) error       { return errNoBundled }
func (missingShare) LogPath(string, bool) string                         { return "" }

// parseTunnelVersion extracts the version from `tunnel version` output
// ("tunnel 1.2.0", "tunnel dev"); "" if the last token isn't a version.
func parseTunnelVersion(out string) string {
	f := strings.Fields(out)
	if len(f) == 0 {
		return ""
	}
	v := f[len(f)-1]
	if v == "dev" {
		return v
	}
	if r := strings.TrimPrefix(v, "v"); r != "" && r[0] >= '0' && r[0] <= '9' {
		return v
	}
	return ""
}

func tunnelVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _, code, err := sharesvc.ExecRunner{}.Run(ctx, bin, "version")
	if err != nil || code != 0 {
		return ""
	}
	return parseTunnelVersion(string(out))
}

func configPath() (string, error) {
	if p := os.Getenv(configPathEnv); p != "" {
		return p, nil
	}
	return config.DefaultPath()
}

// newManager wires the core manager to the real system.
func newManager(onChange func(core.AppState)) (*core.Manager, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	var share core.ShareCtl = missingShare{}
	tv := ""
	if bin, err := bundled.TunnelPath(); err != nil {
		slog.Warn("bundled tunnel not found; sharing is unavailable", "err", err)
	} else {
		name := ""
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		share = sharesvc.New(bin, sharesvc.ExecRunner{}, runtime.GOOS, name)
		tv = tunnelVersion(bin)
	}
	id, err := tunnel.DeviceID()
	if err != nil {
		slog.Error("cannot determine device id", "err", err)
		id = ""
	}
	return core.New(core.Deps{
		ConfigPath:    path,
		NewSession:    core.RealSession,
		Share:         share,
		DeviceID:      id,
		Now:           time.Now,
		OnChange:      onChange,
		TunnelVersion: tv,
	})
}
