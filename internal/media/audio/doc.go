// Package audio provides audio track selection and ranking for media files.
//
// This package depends only on internal/media/ffprobe and could be extracted
// as a standalone library alongside ffprobe.
//
// The selection algorithm prioritizes:
//  1. English language tracks (falls back to first available if none found)
//  2. Channel count (8ch > 6ch > 2ch)
//  3. Lossless codecs over lossy (TrueHD, DTS-HD MA, FLAC, PCM)
//
// Spatial audio metadata (Atmos, DTS:X) is detected but not prioritized
// since it's typically stripped during transcoding to Opus.
//
// Key types:
//   - Selection: describes which audio streams to keep/remove
//
// Primary entry point:
//   - Select: analyzes streams and returns optimal audio layout
package audio
