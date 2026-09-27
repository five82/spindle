package grain

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// Synthetic originals have a known, truly clean partner. The gradient covers
// brightness bins without introducing texture that should be mistaken for grain.
func syntheticPair(width, height int, seed int64, sigma, horizontal, vertical float64, chroma bool) ([]byte, []byte) {
	src, dst := make([]byte, width*height*3), make([]byte, width*height*3)
	rng := rand.New(rand.NewSource(seed))
	noise := make([]float64, width*height)
	for y := range height {
		for x := range width {
			i := y*width + x
			n := rng.NormFloat64() * sigma
			if x > 0 {
				n += horizontal * noise[i-1]
			}
			if y > 0 {
				n += vertical * noise[i-width]
			}
			noise[i] = n
			clean := float64(160 + 640*x/width)
			binary.LittleEndian.PutUint16(dst[2*i:], uint16(clean))
			binary.LittleEndian.PutUint16(src[2*i:], uint16(math.Round(clean+n)))
		}
	}
	for i := width * height; i < len(src)/2; i++ {
		n := 0.0
		if chroma {
			n = rng.NormFloat64() * sigma / 2
		}
		binary.LittleEndian.PutUint16(dst[2*i:], 512)
		binary.LittleEndian.PutUint16(src[2*i:], uint16(math.Round(512+n)))
	}
	return src, dst
}

func estimateSynthetic(t testing.TB, sigma, horizontal, vertical float64, chroma bool) Estimate {
	t.Helper()
	const w, h = 512, 256
	e, err := New(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for i := range 8 {
		src, dst := syntheticPair(w, h, int64(i+1), sigma, horizontal, vertical, chroma)
		if err := e.Observe(i, src, dst); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func tableFields(t testing.TB, table, key string) []int {
	t.Helper()
	for line := range strings.SplitSeq(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != key {
			continue
		}
		out := make([]int, len(fields)-1)
		for i, v := range fields[1:] {
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatal(err)
			}
			out[i] = n
		}
		return out
	}
	t.Fatalf("no %s in table: %s", key, table)
	return nil
}

func meanStrength(t testing.TB, table, plane string) float64 {
	t.Helper()
	p := tableFields(t, table, "p")
	points := tableFields(t, table, plane)
	if points[0] == 0 {
		return 0
	}
	var sum float64
	for i := range points[0] {
		sum += float64(points[2+2*i]) * 4 / math.Exp2(float64(p[3]-5))
	}
	return sum / float64(points[0])
}

func TestRecoversNoiseModel(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		sigma, horizontal, vertical float64
		chroma                      bool
	}{
		{"white", 8, 0, 0, false},
		{"correlated", 8, 0.45, 0.25, true},
		// Sub-8-bit residuals would be corrupted by converting both partners
		// to 8-bit before subtraction, as some other estimators do.
		{"fractional-10-bit", 0.8, 0, 0, false},
		{"fractional-correlated-10-bit", 0.8, 0.45, 0.25, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateSynthetic(t, tc.sigma, tc.horizontal, tc.vertical, tc.chroma)
			p := tableFields(t, got.Table, "p")
			if p[0] != 3 || p[1] < 6 || p[1] > 9 || p[3] < 8 || p[3] > 11 {
				t.Fatalf("invalid parameter shifts: %v", p)
			}
			coeffs := tableFields(t, got.Table, "cY")
			horizontal := float64(coeffs[23]) / math.Exp2(float64(p[1]))
			vertical := float64(coeffs[17]) / math.Exp2(float64(p[1]))
			if math.Abs(horizontal-tc.horizontal) > 0.08 || math.Abs(vertical-tc.vertical) > 0.08 {
				t.Errorf("AR fit = horizontal %.3f, vertical %.3f; want %.3f, %.3f", horizontal, vertical, tc.horizontal, tc.vertical)
			}
			strength := meanStrength(t, got.Table, "sY")
			if math.Abs(strength-tc.sigma) > math.Max(0.15*tc.sigma, 0.15) {
				t.Errorf("innovation strength = %.3f, want %.3f", strength, tc.sigma)
			}
			for _, plane := range []string{"sCb", "sCr"} {
				strength := meanStrength(t, got.Table, plane)
				if tc.chroma && math.Abs(strength-tc.sigma/2) > 0.6 {
					t.Errorf("%s strength = %.3f, want %.3f", plane, strength, tc.sigma/2)
				} else if !tc.chroma && strength != 0 {
					t.Errorf("invented chroma grain: %s = %.3f", plane, strength)
				}
			}
			if got.Patches != 128 || got.AcceptedFrames != 8 || len(got.Frames) != 8 || got.Seconds <= 0 {
				t.Errorf("unexpected sampling evidence: %+v", got)
			}
		})
	}
}

func TestPromoted8BitAndIntensityCurve(t *testing.T) {
	for _, mode := range []string{"8-bit", "intensity-curve"} {
		t.Run(mode, func(t *testing.T) {
			const w, h = 512, 256
			e, err := New(w, h)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			for frame := range 8 {
				src, dst := syntheticPair(w, h, int64(frame+1), 8, 0, 0, false)
				for i := range len(src) / 2 {
					s, d := binary.LittleEndian.Uint16(src[2*i:]), binary.LittleEndian.Uint16(dst[2*i:])
					if mode == "8-bit" {
						s, d = s&^3, d&^3
					} else if i < w*h {
						s = uint16(math.Round(float64(d) + float64(int(s)-int(d))*(0.5+float64(i%w)/w)))
					}
					binary.LittleEndian.PutUint16(src[2*i:], s)
					binary.LittleEndian.PutUint16(dst[2*i:], d)
				}
				if err := e.Observe(frame, src, dst); err != nil {
					t.Fatal(err)
				}
			}
			got, err := e.Finish()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "8-bit" {
				if strength := meanStrength(t, got.Table, "sY"); math.Abs(strength-8) > 0.5 {
					t.Errorf("promoted 8-bit strength = %.3f, want 8", strength)
				}
				return
			}
			p, points := tableFields(t, got.Table, "p"), tableFields(t, got.Table, "sY")
			for _, tc := range []struct{ x, want float64 }{{64, 5.2}, {176, 10.8}} {
				found := false
				for i := 0; i < points[0]-1; i++ {
					x0, x1 := float64(points[1+2*i]), float64(points[3+2*i])
					if tc.x < x0 || tc.x > x1 {
						continue
					}
					a := (tc.x - x0) / (x1 - x0)
					strength := ((1-a)*float64(points[2+2*i]) + a*float64(points[4+2*i])) * 4 / math.Exp2(float64(p[3]-5))
					if math.Abs(strength-tc.want) > 0.7 {
						t.Errorf("strength at %.0f = %.3f, want %.3f", tc.x, strength, tc.want)
					}
					found = true
					break
				}
				if !found {
					t.Errorf("curve does not cover %.0f", tc.x)
				}
			}
		})
	}
}

func TestLumaChromaCorrelation(t *testing.T) {
	const w, h = 512, 256
	e, err := New(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for frame := range 8 {
		src, dst := syntheticPair(w, h, int64(frame+1), 8, 0, 0, false)
		rng := rand.New(rand.NewSource(int64(frame + 100)))
		for c := range 2 {
			for y := range h / 2 {
				for x := range w / 2 {
					var luma float64
					for dy := range 2 {
						for dx := range 2 {
							i := (2*y+dy)*w + 2*x + dx
							luma += float64(int(binary.LittleEndian.Uint16(src[2*i:]))-int(binary.LittleEndian.Uint16(dst[2*i:]))) / 4
						}
					}
					i := w*h + c*w*h/4 + y*w/2 + x
					value := uint16(math.Round(512 + 0.6*luma + rng.NormFloat64()*4))
					binary.LittleEndian.PutUint16(src[2*i:], value)
				}
			}
		}
		if err := e.Observe(frame, src, dst); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.Finish()
	if err != nil {
		t.Fatal(err)
	}
	p := tableFields(t, got.Table, "p")
	for _, plane := range []struct{ scaling, coeffs string }{{"sCb", "cCb"}, {"sCr", "cCr"}} {
		strength := meanStrength(t, got.Table, plane.scaling)
		if math.Abs(strength-4) > 0.5 {
			t.Errorf("%s independent strength = %.3f, want 4", plane.scaling, strength)
		}
		coeffs := tableFields(t, got.Table, plane.coeffs)
		// Synthesis operates on unscaled grain, so the signalled cross-plane
		// coefficient includes the ratio of luma/chroma innovation strengths.
		correlation := float64(coeffs[24]) / math.Exp2(float64(p[1]))
		t.Logf("%s independent strength %.3f (want 4), cross-plane coefficient %.3f (want 1.2)", plane.scaling, strength, correlation)
		if math.Abs(correlation-1.2) > 0.2 {
			t.Errorf("%s cross-plane coefficient = %.3f, want 1.2", plane.coeffs, correlation)
		}
	}
}

func TestDeterministicModel(t *testing.T) {
	a := estimateSynthetic(t, 8, 0.45, 0.25, true)
	b := estimateSynthetic(t, 8, 0.45, 0.25, true)
	if a.Table != b.Table {
		t.Fatal("same samples produced different tables")
	}
}

func TestRejectsUnreliableSamples(t *testing.T) {
	for _, kind := range []string{"zero", "clipped", "edge", "textured", "few-frames"} {
		t.Run(kind, func(t *testing.T) {
			const w, h = 512, 256
			e, err := New(w, h)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			for frame := range 4 {
				src, dst := syntheticPair(w, h, int64(frame+1), 8, 0, 0, false)
				if kind == "zero" || (kind == "few-frames" && frame > 0) {
					copy(src, dst)
				}
				for i := range w * h {
					switch kind {
					case "clipped":
						binary.LittleEndian.PutUint16(src[2*i:], 0)
						binary.LittleEndian.PutUint16(dst[2*i:], 0)
					case "edge":
						value := uint16(256 + 512*((i%w)/16%2))
						binary.LittleEndian.PutUint16(src[2*i:], value)
						binary.LittleEndian.PutUint16(dst[2*i:], 512)
					case "textured":
						value := uint16(256 + 512*((i%w+i/w)%2))
						binary.LittleEndian.PutUint16(src[2*i:], value)
						binary.LittleEndian.PutUint16(dst[2*i:], value-4)
					}
				}
				if err := e.Observe(frame, src, dst); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Finish(); err == nil {
				t.Fatal("unreliable samples must fail, not generate a replacement table")
			}
		})
	}
}

func TestObserveDoesNotRetainFrameBuffers(t *testing.T) {
	want := estimateSynthetic(t, 8, 0.45, 0.25, true)
	e, err := New(512, 256)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for i := range 8 {
		src, dst := syntheticPair(512, 256, int64(i+1), 8, 0.45, 0.25, true)
		if err := e.Observe(i, src, dst); err != nil {
			t.Fatal(err)
		}
		clear(src)
		clear(dst)
	}
	got, err := e.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if got.Table != want.Table {
		t.Fatal("recycled frame buffers changed the saved patches")
	}
}

func TestEstimatorInputContract(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {31, 32}, {33, 32}, {8194, 32}} {
		if e, err := New(size[0], size[1]); err == nil {
			e.Close()
			t.Errorf("accepted dimensions %v", size)
		}
	}
	e, err := New(64, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	src, dst := syntheticPair(64, 64, 1, 8, 0, 0, false)
	if err := e.Observe(0, src[:len(src)-1], dst); err == nil {
		t.Error("accepted truncated buffer")
	}
	if err := e.Observe(0, src, dst); err != nil {
		t.Fatal(err)
	}
	if err := e.Observe(0, src, dst); err == nil {
		t.Error("accepted duplicate frame")
	}
	for i := 1; i < 48; i++ {
		if err := e.Observe(i, src, dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Observe(48, src, dst); err == nil {
		t.Error("exceeded frame budget")
	}
	e.Close()
	if err := e.Observe(49, src, dst); err == nil {
		t.Error("accepted frame after Close")
	}
	if _, err := e.Finish(); err == nil {
		t.Error("finished after Close")
	}
}

func TestSampleFrame(t *testing.T) {
	for _, frames := range []int{100, 101, 240, 1000} {
		var samples []int
		for i := range frames {
			if SampleFrame(i, frames) {
				samples = append(samples, i)
			}
		}
		if len(samples) != FramesPerChunk || samples[0] == 0 || samples[3] == frames-1 {
			t.Errorf("%d frames: %v", frames, samples)
		}
	}
	if SampleFrame(-1, 100) || SampleFrame(100, 100) || SampleFrame(0, 0) {
		t.Error("sampled an out-of-range frame")
	}
}

func BenchmarkEstimateTitle(b *testing.B) {
	for _, size := range [][2]int{{1920, 1080}, {3840, 2160}} {
		b.Run(fmt.Sprintf("%dx%d-48frames", size[0], size[1]), func(b *testing.B) {
			// Buffer generation is outside the measurement: production reuses
			// the existing ceiling pass's frame pairs rather than decoding again.
			src, dst := syntheticPair(size[0], size[1], 1, 8, 0.45, 0.25, true)
			b.ResetTimer()
			for b.Loop() {
				e, err := New(size[0], size[1])
				if err != nil {
					b.Fatal(err)
				}
				for i := range 48 {
					if err := e.Observe(i, src, dst); err != nil {
						e.Close()
						b.Fatal(err)
					}
				}
				_, err = e.Finish()
				e.Close()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
