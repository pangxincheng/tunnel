package main

import (
	"log/slog"
	"math"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// zoomLevels are the interface sizes offered by View → Zoom In / Out and the
// settings dialog (1 = 100%).
var zoomLevels = []float64{0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2}

// normZoom snaps a stored zoom to the nearest level; 0 or negative (unset)
// means 100%.
func normZoom(z float64) float64 {
	if z <= 0 {
		return 1
	}
	best := zoomLevels[0]
	for _, l := range zoomLevels {
		if math.Abs(l-z) < math.Abs(best-z) {
			best = l
		}
	}
	return best
}

// stepZoom returns the level after cur in direction dir (+1 larger, -1
// smaller, 0 back to 100%), clamped to the ends.
func stepZoom(cur float64, dir int) float64 {
	if dir == 0 {
		return 1
	}
	cur = normZoom(cur)
	i := 0
	for j, l := range zoomLevels {
		if l == cur {
			i = j
		}
	}
	i = min(max(i+dir, 0), len(zoomLevels)-1)
	return zoomLevels[i]
}

// zoom steps the interface size (see stepZoom), applies it to the window and
// remembers it.
func (s *AppService) zoom(dir int) {
	st := s.mgr.State().Settings
	st.Zoom = stepZoom(st.Zoom, dir)
	if err := s.mgr.UpdateSettings(st); err != nil {
		slog.Warn("saving zoom failed", "err", err)
	}
	if s.applyZoom != nil {
		s.applyZoom(st.Zoom)
	}
}

// addViewMenu adds View → Zoom In / Zoom Out / Actual Size on Cmd/Ctrl + = - 0.
func addViewMenu(m *application.Menu, goos string, zoom func(dir int)) {
	label := "显示"
	if goos != "darwin" {
		label = "View"
	}
	v := m.AddSubmenu(label)
	v.Add("放大").SetAccelerator("CmdOrCtrl+=").OnClick(func(*application.Context) { zoom(1) })
	v.Add("缩小").SetAccelerator("CmdOrCtrl+-").OnClick(func(*application.Context) { zoom(-1) })
	v.Add("实际大小").SetAccelerator("CmdOrCtrl+0").OnClick(func(*application.Context) { zoom(0) })
}
