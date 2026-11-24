package tunnel

import (
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := NewBackoff(time.Second, 8*time.Second)
	wantBase := []float64{1, 2, 4, 8, 8, 8}
	for i, w := range wantBase {
		d := b.Next()
		lo := time.Duration(w * 0.8 * float64(time.Second))
		hi := time.Duration(w * 1.2 * float64(time.Second))
		if d < lo || d > hi {
			t.Fatalf("第 %d 次 = %v,期望 [%v, %v]", i, d, lo, hi)
		}
	}
	b.Reset()
	if d := b.Next(); d > 1200*time.Millisecond {
		t.Fatalf("Reset 后 = %v,期望 ~1s", d)
	}
}
