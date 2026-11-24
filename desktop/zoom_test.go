package main

import (
	"path/filepath"
	"testing"

	"github.com/pangxincheng/tunnel/desktop/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestNormZoom(t *testing.T) {
	cases := map[float64]float64{0: 1, -1: 1, 1: 1, 1.1: 1.1, 1.12: 1.1, 1.3: 1.25, 9: 2, 0.5: 0.8}
	for in, want := range cases {
		if got := normZoom(in); got != want {
			t.Errorf("normZoom(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestStepZoom(t *testing.T) {
	cases := []struct {
		cur  float64
		dir  int
		want float64
	}{
		{1, 1, 1.1}, {1.1, 1, 1.25}, {2, 1, 2}, // clamped at the top
		{1, -1, 0.9}, {0.8, -1, 0.8}, // clamped at the bottom
		{1.5, 0, 1}, {0, 1, 1.1}, // 0 = unset = 100%
	}
	for _, c := range cases {
		if got := stepZoom(c.cur, c.dir); got != c.want {
			t.Errorf("stepZoom(%v, %d) = %v, want %v", c.cur, c.dir, got, c.want)
		}
	}
}

// Menu zoom changes the window and is remembered; a zoom chosen in the
// settings dialog is applied to the window too.
func TestZoomAppliedAndSaved(t *testing.T) {
	m, err := core.New(core.Deps{ConfigPath: filepath.Join(t.TempDir(), "config.json"), Share: fakeShare{}})
	if err != nil {
		t.Fatal(err)
	}
	var applied []float64
	s := &AppService{mgr: m, auto: &fakeAuto{}, applyZoom: func(z float64) { applied = append(applied, z) }}
	s.zoom(1)
	s.zoom(1)
	if got := m.State().Settings.Zoom; got != 1.25 {
		t.Fatalf("saved zoom %v", got)
	}
	s.zoom(0)
	st := m.State().Settings
	st.Zoom = 1.5
	if err := s.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	st.Mode = "dark" // unrelated change: zoom not re-applied
	if err := s.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	want := []float64{1.1, 1.25, 1, 1.5}
	if len(applied) != len(want) {
		t.Fatalf("applied %v want %v", applied, want)
	}
	for i := range want {
		if applied[i] != want[i] {
			t.Fatalf("applied %v want %v", applied, want)
		}
	}
}

func TestViewMenuShortcuts(t *testing.T) {
	m := application.NewMenu()
	addViewMenu(m, "darwin", func(int) {})
	for label, key := range map[string]string{"放大": "=", "缩小": "-", "实际大小": "0"} {
		item := m.FindByLabel(label)
		if item == nil {
			t.Fatalf("no %s item", label)
		}
		if got := item.GetAccelerator(); !cmdOrCtrl(got, key) {
			t.Errorf("%s accelerator = %q", label, got)
		}
	}
}
