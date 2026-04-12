package subtitle

import _ "embed"

const (
	stableTSCommand = "uvx"
	stableTSPackage = "stable-ts-whisperless"
)

//go:embed stable_ts_formatter.py
var stableTSFormatterScript string
