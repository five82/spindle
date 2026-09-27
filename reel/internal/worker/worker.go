// Package worker provides types and utilities for parallel chunk encoding.
package worker

// EncodeResult contains the result of encoding a single chunk.
type EncodeResult struct {
	ChunkIdx int
	Frames   int
	Size     uint64
	Error    error
}

// Progress represents encoding progress information.
type Progress struct {
	ChunksComplete int
	ChunksTotal    int
	FramesComplete int
	FramesTotal    int
	BytesComplete  uint64
	ActiveWorkers  int
	TargetWorkers  int
	MaxWorkers     int

	// InFlight is the number of chunks currently being worked on. In
	// target-quality mode a chunk stays in flight while it probes and scores,
	// even when its encode slot is released, so InFlight exceeds ActiveWorkers.
	// In fixed-CRF mode work is slot-bound, so InFlight equals ActiveWorkers.
	InFlight int

	// EncodeSlotWaitSeconds is the cumulative wall time workers have spent
	// blocked waiting to acquire an encode slot. It is monotonic across the
	// encode and aggregated over all workers, so it is a measure of slot
	// starvation, not a per-worker latency.
	EncodeSlotWaitSeconds float64
}

// Percent returns the completion percentage.
func (p Progress) Percent() float64 {
	if p.FramesTotal == 0 {
		return 0
	}
	return float64(p.FramesComplete) / float64(p.FramesTotal) * 100
}
