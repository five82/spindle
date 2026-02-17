// Package audio provides audio track selection and ranking for media files.
//
// This package depends only on internal/media/ffprobe and could be extracted
// as a standalone library alongside ffprobe.
//
// The selection algorithm filters to English-language tracks (falling back to
// first available if none found), then ranks candidates by:
//  1. Channel count (8ch > 6ch > 4ch > 2ch)
//  2. Lossless codecs over lossy (TrueHD, DTS-HD MA, FLAC, PCM)
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
