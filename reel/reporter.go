// Package reel provides a Go library for AV1 video encoding with SVT-AV1.
//
// This file re-exports the internal Reporter interface and associated types
// to allow callers to receive all encoding events directly.

package reel

import "github.com/five82/reel/internal/reporter"

// Reporter defines the interface for progress reporting during encoding.
// Implement this interface to receive detailed events about encoding progress.
type Reporter = reporter.Reporter

// NullReporter is a no-op reporter that discards all updates.
type NullReporter = reporter.NullReporter

// HardwareSummary contains hardware information.
type HardwareSummary = reporter.HardwareSummary

// InitializationSummary describes the current file before encoding.
type InitializationSummary = reporter.InitializationSummary

// CropSummary contains crop detection results.
type CropSummary = reporter.CropSummary

// EncodingConfigSummary contains encoding configuration.
type EncodingConfigSummary = reporter.EncodingConfigSummary

// ProgressSnapshot contains encoding progress information.
type ProgressSnapshot = reporter.ProgressSnapshot

// ValidationSummary contains validation results.
type ValidationSummary = reporter.ValidationSummary

// ReporterValidationStep represents a single validation check from the reporter.
// Note: This is distinct from the ValidationStep type in events.go which is
// used for JSON serialization. Use reporter.ValidationStep internally.
type ReporterValidationStep = reporter.ValidationStep

// EncodingOutcome contains final encoding results.
type EncodingOutcome = reporter.EncodingOutcome

// ReporterError contains error information.
type ReporterError = reporter.ReporterError

// BatchStartInfo contains batch start metadata.
type BatchStartInfo = reporter.BatchStartInfo

// FileProgressContext contains current file index within a batch.
type FileProgressContext = reporter.FileProgressContext

// BatchSummary contains batch completion information.
type BatchSummary = reporter.BatchSummary

// FileResult contains per-file encoding result.
type FileResult = reporter.FileResult

// StageProgress represents a generic stage update.
type StageProgress = reporter.StageProgress
