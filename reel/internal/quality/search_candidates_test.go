package quality

import "testing"

func TestSearchFindsNearestUntriedCRFInsideBounds(t *testing.T) {
	ctx := SearchContext{CRFMin: 10, CRFMax: 11, InitialCRF: 10.5, Target: 9.5, Tolerance: 0.1, MaxProbes: 10}
	state := NewSearchState(ctx)
	for _, tc := range []struct {
		tried []float32
		want  float32
		ok    bool
	}{
		{nil, 10.5, true},
		{[]float32{10.5}, 10.25, true},
		{[]float32{10.5, 10.25}, 10.75, true},
		{[]float32{10.5, 10.25, 10.75}, 10, true},
		{[]float32{10.5, 10.25, 10.75, 10}, 11, true},
		{[]float32{10.5, 10.25, 10.75, 10, 11}, 0, false},
	} {
		state.tried = map[int]bool{}
		for _, crf := range tc.tried {
			state.tried[crfKey(crf)] = true
		}
		got, ok := state.firstUntriedInBounds(10.5)
		if got != tc.want || ok != tc.ok {
			t.Errorf("tried %v: got %.2f/%v, want %.2f/%v", tc.tried, got, ok, tc.want, tc.ok)
		}
	}
	// Candidate clamping must never expand the caller's search bounds.
	state.tried = map[int]bool{}
	if got, ok := state.firstUntriedInBounds(50); !ok || got != 11 {
		t.Fatalf("upper bound: %v/%v", got, ok)
	}
	if got, ok := state.firstUntriedInBounds(0); !ok || got != 10 {
		t.Fatalf("lower bound: %v/%v", got, ok)
	}
}
