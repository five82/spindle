package quality

import (
	"testing"
)

func TestSSIMU2StatsRetainNegativeFramesWithoutMutatingScores(t *testing.T) {
	mean, min, p5, p10 := ssimu2Stats(nil)
	if mean != 0 || min != 0 || p5 != 0 || p10 != 0 || ssimu2Percentile(nil, 0.5) != 0 {
		t.Fatal("empty statistics")
	}
	scores := []float64{90, -10, 80, 70}
	mean, min, p5, p10 = ssimu2Stats(scores)
	if mean != 57.5 || min != -10 || p5 != -10 || p10 != -10 {
		t.Fatalf("statistics: %v %v %v %v", mean, min, p5, p10)
	}
	if scores[0] != 90 || scores[1] != -10 {
		t.Fatal("mutated input")
	}
	if got := ssimu2Percentile([]float64{1, 3, 5}, 1); got != 5 {
		t.Fatal(got)
	}
}

func TestYUV420P10PlanesValidateSizeAndOffsets(t *testing.T) {
	if got := yuv420p10Size(4, 2); got != 24 {
		t.Fatalf("size=%d", got)
	}
	if _, err := PlanesFromYUV420P10(make([]byte, 23), 4, 2); err == nil {
		t.Fatal("short frame accepted")
	}
	buf := make([]byte, 24)
	planes, err := PlanesFromYUV420P10(buf, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if planes.Planes[0] != &buf[0] || planes.Planes[1] != &buf[16] || planes.Planes[2] != &buf[20] || planes.Strides != [3]int64{8, 4, 4} {
		t.Fatalf("planes: %+v", planes)
	}
}
