package logging

import "time"

const logTimestampLayout = "2006-01-02 15:04:05"

func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.In(time.Local).Format(logTimestampLayout)
}
