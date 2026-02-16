// Package drapto integrates the Drapto Go library so the encoding stage can
// launch AV1 transcodes and observe structured progress updates.
//
// It exposes a Client interface, a Library implementation that calls Drapto
// directly, and a reporter adapter that translates Drapto's Reporter callbacks
// into typed ProgressUpdate values. Tests can swap in fakes to avoid executing
// the real encoder while still exercising workflow behaviour.
package drapto
