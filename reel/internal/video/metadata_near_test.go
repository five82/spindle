package video

import (
	"testing"
)

func TestFrameNearReadsAndRejectsInvalidIndex(t *testing.T) {
	path := writeTestY4M(t, 16, 16, 5)
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	frame, tenBit, err := src.readFrameNear(3, 4)
	if err != nil || frame == nil || tenBit {
		t.Fatalf("near frame: %v, ten bit %t: %v", frame, tenBit, err)
	}
	if _, _, err := src.readFrameNear(-1, 1); err == nil {
		t.Fatal("negative index accepted")
	}
}

func TestSVTHDRStrings(t *testing.T) {
	mastering := formatMasteringDisplay(0.68, 0.32, 0.265, 0.69, 0.15, 0.06, 0.3127, 0.329, 1000, 0.005)
	if want := "G(0.2650,0.6900)B(0.1500,0.0600)R(0.6800,0.3200)WP(0.3127,0.3290)L(1000.0000,0.0050)"; *mastering != want {
		t.Fatalf("mastering: %s", *mastering)
	}
	if cl := formatContentLight(1000, 400); cl == nil || *cl != "1000,400" {
		t.Fatalf("content light: %v", cl)
	}
}
