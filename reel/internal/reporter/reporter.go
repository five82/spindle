package reporter

// Reporter defines the interface for progress reporting.
type Reporter interface {
	Hardware(summary HardwareSummary)
	Initialization(summary InitializationSummary)
	StageProgress(update StageProgress)
	CropResult(summary CropSummary)
	EncodingConfig(summary EncodingConfigSummary)
	EncodingStarted(totalFrames uint64)
	EncodingProgress(progress ProgressSnapshot)
	ValidationComplete(summary ValidationSummary)
	EncodingComplete(summary EncodingOutcome)
	Warning(message string)
	Error(err ReporterError)
	OperationComplete(message string)
	BatchStarted(info BatchStartInfo)
	FileProgress(context FileProgressContext)
	BatchComplete(summary BatchSummary)
	Verbose(message string)
}

// NullReporter is a no-op reporter that discards all updates.
type NullReporter struct{}

func (NullReporter) Hardware(HardwareSummary)             {}
func (NullReporter) Initialization(InitializationSummary) {}
func (NullReporter) StageProgress(StageProgress)          {}
func (NullReporter) CropResult(CropSummary)               {}
func (NullReporter) EncodingConfig(EncodingConfigSummary) {}
func (NullReporter) EncodingStarted(uint64)               {}
func (NullReporter) EncodingProgress(ProgressSnapshot)    {}
func (NullReporter) ValidationComplete(ValidationSummary) {}
func (NullReporter) EncodingComplete(EncodingOutcome)     {}
func (NullReporter) Warning(string)                       {}
func (NullReporter) Error(ReporterError)                  {}
func (NullReporter) OperationComplete(string)             {}
func (NullReporter) BatchStarted(BatchStartInfo)          {}
func (NullReporter) FileProgress(FileProgressContext)     {}
func (NullReporter) BatchComplete(BatchSummary)           {}
func (NullReporter) Verbose(string)                       {}
