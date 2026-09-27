// aligncheck reproduces and locates the scoring frame-misalignment bug.
//
// It decodes a source file sequentially as ground truth (frame N -> luma
// checksum), then for a set of target frames calls ReadFrame(N) on a FRESH
// video.Source -- the seek-based access pattern reel's CVVDP scoring and
// fullvalidate use -- and reports any frame whose ReadFrame checksum does not
// match the ground truth,
// including how many frames off it landed. Repeats each probe to expose
// run-to-run nondeterminism.
//
// Usage: go run ./scripts/aligncheck <video.mkv> [reps]
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"strconv"

	"github.com/five82/reel/internal/video"
)

// cropRect matches the sullyhv autocrop (3840:1600:0:280) to exercise the
// crop/sws decode path the real scoring uses. Set to nil via -nocrop.
var cropRect = &video.CropRect{X: 0, Y: 280, Width: 3840, Height: 1600}

func frameSize(inf *video.Info) int {
	if cropRect != nil {
		return int(cropRect.Width) * int(cropRect.Height) * 3
	}
	return int(inf.Width) * int(inf.Height) * 3
}

// sumGroundTruth decodes frames [0,max) sequentially and returns a checksum per index.
func groundTruth(path string, inf *video.Info, max int) ([]uint32, error) {
	src, err := video.Open(path, 1)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	buf := make([]byte, frameSize(inf))
	sums := make([]uint32, max)
	for i := 0; i < max; i++ {
		if err := src.ReadFrame(i, buf, inf, cropRect); err != nil {
			return nil, fmt.Errorf("ground-truth frame %d: %w", i, err)
		}
		sums[i] = crc32.ChecksumIEEE(buf)
	}
	return sums, nil
}

// readFresh opens a NEW Source and ReadFrame(target) -- the scoring access pattern.
func readFresh(path string, inf *video.Info, target int) (uint32, error) {
	src, err := video.Open(path, 1)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	buf := make([]byte, frameSize(inf))
	if err := src.ReadFrame(target, buf, inf, cropRect); err != nil {
		return 0, err
	}
	return crc32.ChecksumIEEE(buf), nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aligncheck <video> [reps]")
		os.Exit(2)
	}
	path := os.Args[1]
	reps := 3
	if len(os.Args) > 2 {
		reps, _ = strconv.Atoi(os.Args[2])
	}

	inf, err := video.Probe(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %dx%d, %d frames\n", path, inf.Width, inf.Height, inf.Frames)

	// Ground truth over the first ~6 splice intervals (60s * 23.976 ~ 1438 frames).
	maxGT := 9000
	if inf.Frames > 0 && inf.Frames < maxGT {
		maxGT = inf.Frames
	}
	fmt.Printf("decoding ground truth [0,%d)...\n", maxGT)
	gt, err := groundTruth(path, inf, maxGT)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ground truth:", err)
		os.Exit(1)
	}
	// Build a reverse map checksum->index to report how far off a wrong read landed.
	rev := make(map[uint32]int, len(gt))
	for i, s := range gt {
		rev[s] = i // last-wins on dup checksums; fine for diagnostics
	}

	// Probe targets: dense around splice points (~1438,2876,...) plus a spread.
	targets := []int{}
	for k := 1; k <= 5; k++ {
		c := k * 1438
		for d := -3; d <= 3; d++ {
			if t := c + d; t >= 0 && t < maxGT {
				targets = append(targets, t)
			}
		}
	}
	for t := 200; t < maxGT; t += 977 {
		targets = append(targets, t)
	}

	// --- Sequential pass ---
	seqMismatch := 0
	for _, t := range targets {
		s, err := readFresh(path, inf, t)
		if err != nil {
			fmt.Printf("  seq frame %d: ERROR %v\n", t, err)
			continue
		}
		if s != gt[t] {
			seqMismatch++
			landed := "unknown"
			if idx, ok := rev[s]; ok {
				landed = fmt.Sprintf("frame %d (off by %+d)", idx, idx-t)
			}
			fmt.Printf("  SEQ MISMATCH frame %d -> %s\n", t, landed)
		}
	}
	fmt.Printf("sequential: %d/%d mismatched\n", seqMismatch, len(targets))

	// --- Concurrent stress (the scoring access pattern: many parallel fresh Sources) ---
	workers := 8
	itersPer := reps * 20
	fmt.Printf("concurrent stress: %d workers x %d reads each...\n", workers, itersPer)
	type res struct {
		target, landed int
		bad            bool
	}
	out := make(chan res, workers*itersPer)
	done := make(chan struct{}, workers)
	for w := 0; w < workers; w++ {
		go func(seed int) {
			for k := 0; k < itersPer; k++ {
				t := targets[(seed*7+k*13)%len(targets)]
				s, err := readFresh(path, inf, t)
				if err != nil {
					out <- res{t, -2, true}
					continue
				}
				if s != gt[t] {
					idx := -1
					if i, ok := rev[s]; ok {
						idx = i
					}
					out <- res{t, idx, true}
				} else {
					out <- res{t, t, false}
				}
			}
			done <- struct{}{}
		}(w)
	}
	go func() {
		for i := 0; i < workers; i++ {
			<-done
		}
		close(out)
	}()
	conMismatch, conTotal := 0, 0
	offsets := map[int]int{}
	for r := range out {
		conTotal++
		if r.bad {
			conMismatch++
			if conMismatch <= 15 {
				fmt.Printf("  CONCURRENT MISMATCH frame %d -> landed %d (off %+d)\n", r.target, r.landed, r.landed-r.target)
			}
			offsets[r.landed-r.target]++
		}
	}
	fmt.Printf("\nconcurrent: %d/%d reads wrong\n", conMismatch, conTotal)
	if len(offsets) > 0 {
		fmt.Printf("offset histogram (landed-requested): %v\n", offsets)
	}
	if seqMismatch == 0 && conMismatch == 0 {
		fmt.Println("RESULT: ReadFrame is aligned and deterministic (sequential AND concurrent)")
	} else if seqMismatch == 0 {
		fmt.Println("RESULT: ReadFrame misalignment appears ONLY under concurrency -> decode race")
	} else {
		fmt.Println("RESULT: ReadFrame misalignment reproduced (sequential)")
	}
}
