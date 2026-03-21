// Package auditgather collects comprehensive audit artifacts for queue items.
// Its sole consumer is the /itemaudit agent skill. The structured JSON output
// (pre-computed analysis, anomaly flags, aggregated decisions) enables the LLM
// skill to audit a queue item in a single context window without running
// multiple sequential shell commands.
package auditgather
