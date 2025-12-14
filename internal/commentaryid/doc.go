// Package commentaryid detects and retains audio commentary tracks.
//
// The default ripping workflow keeps only the primary English track plus any
// commentaries that can be inferred from MakeMKV/ffprobe metadata. Some discs
// omit reliable commentary labels, so commentaryid optionally performs a
// pre-encoding analysis pass:
//
//   - Extract short audio snippets from the primary track and each English stereo track
//   - Transcribe snippets with WhisperX (cached)
//   - Filter obvious duplicates ("same as primary") with text similarity scoring
//   - Use an OpenRouter-backed LLM to classify remaining candidates (commentary vs audio description vs music-only)
//
// The output is a keep-list of stream indices that upstream stages can remux
// into a smaller container before encoding.
package commentaryid
