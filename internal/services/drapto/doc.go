// Package drapto wraps the Drapto command-line encoder so the encoding stage can
// launch AV1 transcodes and observe structured progress updates.
//
// It exposes a small client interface, a CLI implementation with configurable
// binary paths, and typed progress callbacks that surface percent complete,
// stage names, and human messages. Tests can swap in fakes to avoid executing
// the real encoder while still exercising workflow behaviour.
package drapto
