package reporter

// CompositeReporter fans out events to multiple reporters.
type CompositeReporter struct {
	reporters []Reporter
}

// NewCompositeReporter creates a composite reporter.
func NewCompositeReporter(reporters ...Reporter) *CompositeReporter {
	return &CompositeReporter{reporters: reporters}
}

func (c *CompositeReporter) Hardware(summary HardwareSummary) {
	for _, r := range c.reporters {
		r.Hardware(summary)
	}
}

func (c *CompositeReporter) Initialization(summary InitializationSummary) {
	for _, r := range c.reporters {
		r.Initialization(summary)
	}
}

func (c *CompositeReporter) StageProgress(update StageProgress) {
	for _, r := range c.reporters {
		r.StageProgress(update)
	}
}

func (c *CompositeReporter) CropResult(summary CropSummary) {
	for _, r := range c.reporters {
		r.CropResult(summary)
	}
}

func (c *CompositeReporter) EncodingConfig(summary EncodingConfigSummary) {
	for _, r := range c.reporters {
		r.EncodingConfig(summary)
	}
}

func (c *CompositeReporter) EncodingStarted(totalFrames uint64) {
	for _, r := range c.reporters {
		r.EncodingStarted(totalFrames)
	}
}

func (c *CompositeReporter) EncodingProgress(progress ProgressSnapshot) {
	for _, r := range c.reporters {
		r.EncodingProgress(progress)
	}
}

func (c *CompositeReporter) ValidationComplete(summary ValidationSummary) {
	for _, r := range c.reporters {
		r.ValidationComplete(summary)
	}
}

func (c *CompositeReporter) EncodingComplete(summary EncodingOutcome) {
	for _, r := range c.reporters {
		r.EncodingComplete(summary)
	}
}

func (c *CompositeReporter) Warning(message string) {
	for _, r := range c.reporters {
		r.Warning(message)
	}
}

func (c *CompositeReporter) Error(err ReporterError) {
	for _, r := range c.reporters {
		r.Error(err)
	}
}

func (c *CompositeReporter) OperationComplete(message string) {
	for _, r := range c.reporters {
		r.OperationComplete(message)
	}
}

func (c *CompositeReporter) BatchStarted(info BatchStartInfo) {
	for _, r := range c.reporters {
		r.BatchStarted(info)
	}
}

func (c *CompositeReporter) FileProgress(context FileProgressContext) {
	for _, r := range c.reporters {
		r.FileProgress(context)
	}
}

func (c *CompositeReporter) BatchComplete(summary BatchSummary) {
	for _, r := range c.reporters {
		r.BatchComplete(summary)
	}
}

func (c *CompositeReporter) Verbose(message string) {
	for _, r := range c.reporters {
		r.Verbose(message)
	}
}
