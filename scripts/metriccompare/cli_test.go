package main

import (
	"errors"
	"strings"
	"testing"
)

func TestCLISelectsModeAndForwardsOptions(t *testing.T) {
	calls := 0
	jobs := func(input, out, display string, perFrame bool, metrics metricSet) error {
		calls++
		if input != "jobs.json" || out != "out.json" || display != "screen.json" || !perFrame || metrics.cvvdp || !metrics.ssimu2 {
			t.Errorf("jobs options: %s %s %s %t %+v", input, out, display, perFrame, metrics)
		}
		return errors.New("jobs failed")
	}
	bench := func(src, dist string, start, frames, reps int, crop, display string, metrics metricSet) error {
		calls++
		if src != "src.y4m" || dist != "dist.y4m" || start != 3 || frames != 4 || reps != 2 || crop != "8:8:0:0" || display != "screen.json" || !metrics.cvvdp || metrics.ssimu2 {
			t.Errorf("bench options: %s %s %d %d %d %s %s %+v", src, dist, start, frames, reps, crop, display, metrics)
		}
		return errors.New("bench failed")
	}
	if err := runCLI([]string{"--jobs", "jobs.json", "--out", "out.json", "--display", "screen.json", "--metrics", "ssimu2", "--per-frame"}, jobs, bench); err == nil || err.Error() != "jobs failed" {
		t.Fatalf("jobs error: %v", err)
	}
	if err := runCLI([]string{"--gpubench", "--src", "src.y4m", "--dist", "dist.y4m", "--start", "3", "--frames", "4", "--reps", "2", "--crop", "8:8:0:0", "--display", "screen.json", "--metrics", "cvvdp"}, jobs, bench); err == nil || err.Error() != "bench failed" {
		t.Fatalf("bench error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "usage:"},
		{[]string{"--metrics", "unknown"}, "unknown metric"},
		{[]string{"--frames", "oops"}, "invalid value"},
	} {
		if err := runCLI(tc.args, jobs, bench); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
	if calls != 2 {
		t.Fatalf("unexpected dispatch: %d", calls)
	}
}
