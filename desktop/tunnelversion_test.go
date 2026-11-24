package main

import (
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

// minTunnel is the oldest tunnel release the app works with: the bundled CLI
// must support `status --json` (mode, exit codes 3 / 4), `set-name` and
// automatic profile selection, all added in v1.3.0. The Go tests cannot
// notice an older CLI on their own, so `wails3 task tunnel:bump` relies on
// this test to refuse a downgrade.
const minTunnel = "v1.3.0"

func TestTunnelModuleAtLeastMinimum(t *testing.T) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("no build info")
	}
	for _, d := range bi.Deps {
		if d.Path != "github.com/pangxincheng/tunnel" {
			continue
		}
		if d.Replace != nil {
			t.Skipf("tunnel replaced by %s (local development)", d.Replace.Path)
		}
		if !versionAtLeast(d.Version, minTunnel) {
			t.Fatalf("go.mod requires tunnel %s; the app needs at least %s", d.Version, minTunnel)
		}
		return
	}
	t.Fatal("tunnel module not found in build info")
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"v1.3.0", true},
		{"v1.3.1", true},
		{"v1.10.0", true},
		{"v2.0.0", true},
		{"v1.2.9", false},
		{"v0.9.0", false},
		{"v1.3.0-rc.1", false}, // a pre-release sorts before its release
		{"v1.3.1-rc.1", true},
		{"garbage", false},
	}
	for _, c := range cases {
		if got := versionAtLeast(c.v, "v1.3.0"); got != c.want {
			t.Errorf("versionAtLeast(%q, v1.3.0) = %v, want %v", c.v, got, c.want)
		}
	}
}

// versionAtLeast reports whether semver tag v >= min (both "vX.Y.Z[-pre]";
// min has no pre-release). Unparsable versions are never enough.
func versionAtLeast(v, min string) bool {
	pv, pre, ok := parseSemver(v)
	pm, _, okm := parseSemver(min)
	if !ok || !okm {
		return false
	}
	for i := range pv {
		if pv[i] != pm[i] {
			return pv[i] > pm[i]
		}
	}
	return !pre // equal X.Y.Z: the release itself counts, its pre-releases don't
}

func parseSemver(v string) (nums [3]int, pre bool, ok bool) {
	core, _, hasPre := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(core, ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return nums, false, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, false, false
		}
		nums[i] = n
	}
	return nums, hasPre, true
}
