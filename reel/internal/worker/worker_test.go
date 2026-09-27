package worker

import "testing"

func TestProgressPercent(t *testing.T) {
	for _, tt := range []struct {
		done, total int
		want        float64
	}{{0, 0, 0}, {0, 10, 0}, {3, 8, 37.5}, {8, 8, 100}} {
		p := Progress{FramesComplete: tt.done, FramesTotal: tt.total}
		if got := p.Percent(); got != tt.want {
			t.Errorf("%d/%d: got %v, want %v", tt.done, tt.total, got, tt.want)
		}
	}
}
