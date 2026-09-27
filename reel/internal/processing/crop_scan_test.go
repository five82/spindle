package processing

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"testing"
)

func TestCropScanMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 17))
	for trial := 0; trial < 3000; trial++ {
		width, height := 1+rng.IntN(256), 1+rng.IntN(256)
		is10Bit := rng.IntN(2) == 0
		bytesPerSample := 1
		if is10Bit {
			bytesPerSample = 2
		}
		shift := []int{0, 2, 8}[rng.IntN(3)]
		stride := width*bytesPerSample + rng.IntN(33)
		data := make([]byte, stride*height)
		// Bright padding must not count as picture activity.
		for i := range data {
			data[i] = 255
		}
		x, y := rng.IntN(width), rng.IntN(height)
		w, h := 1+rng.IntN(width-x), 1+rng.IntN(height-y)
		for row := 0; row < height; row++ {
			for col := 0; col < width; col++ {
				v := uint16(16)
				switch trial % 6 {
				case 0: // Asymmetric bars, sometimes a single active pixel/line.
					if col >= x && col < x+w && row >= y && row < y+h {
						v = uint16(25 + rng.IntN(231))
					}
				case 1: // Dark, low-contrast noise straddling the black threshold.
					v = uint16(15 + rng.IntN(12))
				case 2: // Sparse activity near the whole-frame rejection threshold.
					if rng.IntN(200) == 0 {
						v = 25
					}
				case 3: // Full-range texture.
					v = uint16(rng.IntN(256))
				case 4: // Uniform black or near-black.
					v = uint16(trial % 26)
				case 5: // Subtitles or isolated highlights outside the picture.
					if (row == y && col >= x && col < x+w) || rng.IntN(100) == 0 {
						v = 255
					}
				}
				off := row*stride + col*bytesPerSample
				if is10Bit {
					s := shift
					if s == 0 {
						s = 2
					}
					v = v<<s | uint16(rng.IntN(1<<s))
					binary.LittleEndian.PutUint16(data[off:], v)
				} else {
					data[off] = byte(v)
				}
			}
		}
		got, ok := detectLumaCrop(data, width, height, stride, is10Bit, shift)
		want, wantOK := referenceLumaCrop(data, width, height, stride, is10Bit, shift)
		if got != want || ok != wantOK {
			t.Fatalf("trial %d (%dx%d stride=%d 10bit=%t shift=%d): got %+v/%t, want %+v/%t",
				trial, width, height, stride, is10Bit, shift, got, ok, want, wantOK)
		}
	}
}

func TestCropScanActivityThresholds(t *testing.T) {
	for _, tc := range []struct {
		name       string
		background byte
		bright     byte
		count      int
		want       bool
	}{
		{"black", 16, 24, 200, false},
		{"contrast-below", 16, 25, 1, false},
		{"contrast-at", 15, 25, 1, true},
		{"count-at", 16, 25, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := makeLuma8(200, 1, 200, tc.background)
			// Put bright pixels first so a later dark pixel can establish contrast.
			for i := 0; i < tc.count; i++ {
				data[i] = tc.bright
			}
			if got := activeLumaLine(data, 0, 1, 200, false, 0); got != tc.want {
				t.Fatalf("active line = %t, want %t", got, tc.want)
			}
		})
	}
	for _, count := range []int{199, 200, 201} {
		data := makeLuma8(200, 200, 208, 16)
		for i := 0; i < count; i++ {
			data[(i/200)*208+i%200] = 96
		}
		if got := hasActiveLuma(data, 200, 200, 208, false, 0); got != (count >= 200) {
			t.Fatalf("frame activity at %d pixels = %t", count, got)
		}
	}
	// Enough active pixels overall does not imply any row meets its own floor.
	data := makeLuma8(200, 200, 200, 16)
	for row := 0; row < 200; row++ {
		data[row*200] = 25
	}
	if crop, ok := detectLumaCrop(data, 200, 200, 200, false, 0); ok {
		t.Fatalf("expected no active rows, got %+v", crop)
	}
}

func TestCropScanColumnsIncludeInactiveRows(t *testing.T) {
	data := makeLuma8(400, 400, 416, 16)
	fillRect8(data, 416, 100, 100, 200, 200, 96)
	// Each of these rows is inactive (one pixel, contrast 9), but together
	// their pixels make column zero active. Column scans cannot be restricted
	// to the active row rectangle without changing the crop.
	for row := 0; row < 4; row++ {
		data[row*416] = 25
	}
	want := detectedCrop{Top: 100, Bottom: 100, Left: 0, Right: 100}
	if got, ok := detectLumaCrop(data, 400, 400, 416, false, 0); !ok || got != want {
		t.Fatalf("got %+v/%t, want %+v/true", got, ok, want)
	}
}

func TestCropScanSinglePixel(t *testing.T) {
	data := makeLuma8(9, 7, 16, 16)
	data[3*16+5] = 96
	want := detectedCrop{Top: 3, Bottom: 3, Left: 5, Right: 3}
	if got, ok := detectLumaCrop(data, 9, 7, 16, false, 0); !ok || got != want {
		t.Fatalf("got %+v/%t, want %+v/true", got, ok, want)
	}
}

func TestCropScanInvalidGeometry(t *testing.T) {
	for _, tc := range []struct {
		width, height, stride, size int
		is10Bit                     bool
	}{
		{0, 8, 16, 128, false},
		{8, -1, 16, 128, false},
		{8, 8, 0, 128, false},
		{8, 8, 7, 128, false},
		{8, 8, 15, 128, true},
		{8, 8, 16, 127, true},
	} {
		if got, ok := detectLumaCrop(make([]byte, tc.size), tc.width, tc.height, tc.stride, tc.is10Bit, 0); ok || got != (detectedCrop{}) {
			t.Fatalf("geometry %+v: got %+v/%t", tc, got, ok)
		}
	}
}

func FuzzCropScanMatchesReference(f *testing.F) {
	f.Add([]byte{16, 16, 96, 96, 16}, uint8(12), uint8(8), uint8(4), false, false)
	f.Add([]byte{255, 255, 0, 0, 64, 0}, uint8(20), uint8(12), uint8(3), true, true)
	f.Fuzz(func(t *testing.T, pattern []byte, w, h, pad uint8, is10Bit, shifted bool) {
		if len(pattern) == 0 {
			return
		}
		width, height := int(w)+1, int(h)+1
		bpp, shift := 1, 0
		if is10Bit {
			bpp = 2
			if shifted {
				shift = 8
			}
		}
		stride := width*bpp + int(pad%32)
		data := make([]byte, stride*height)
		for i := range data {
			data[i] = pattern[i%len(pattern)]
		}
		got, ok := detectLumaCrop(data, width, height, stride, is10Bit, shift)
		want, wantOK := referenceLumaCrop(data, width, height, stride, is10Bit, shift)
		if got != want || ok != wantOK {
			t.Fatalf("got %+v/%t, want %+v/%t", got, ok, want, wantOK)
		}
	})
}

func BenchmarkCropScan(b *testing.B) {
	for _, width := range []int{1920, 3840} {
		height := width * 9 / 16
		for _, is10Bit := range []bool{false, true} {
			for _, shape := range []string{"full", "letterbox", "pillarbox", "windowbox", "black"} {
				bpp := 1
				if is10Bit {
					bpp = 2
				}
				stride := width*bpp + 64
				var data []byte
				x, y := 0, 0
				if shape == "letterbox" || shape == "windowbox" {
					y = height / 8
				}
				if shape == "pillarbox" || shape == "windowbox" {
					x = width / 8
				}
				if is10Bit {
					data = makeLuma10(width, height, stride, 64)
					if shape != "black" {
						fillRect10(data, stride, x, y, width-2*x, height-2*y, 384)
					}
				} else {
					data = makeLuma8(width, height, stride, 16)
					if shape != "black" {
						fillRect8(data, stride, x, y, width-2*x, height-2*y, 96)
					}
				}
				for _, impl := range []struct {
					name string
					fn   func([]byte, int, int, int, bool, int) (detectedCrop, bool)
				}{
					{"reference", referenceLumaCrop},
					{"edges", detectLumaCrop},
				} {
					bits := 8
					if is10Bit {
						bits = 10
					}
					b.Run(fmt.Sprintf("%d/%dbit/%s/%s", width, bits, shape, impl.name), func(b *testing.B) {
						b.ReportAllocs()
						for b.Loop() {
							impl.fn(data, width, height, stride, is10Bit, 2)
						}
					})
				}
			}
		}
	}
}
