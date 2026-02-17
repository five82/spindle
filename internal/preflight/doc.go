// Package preflight provides readiness checks for external services
// and filesystem paths that Spindle depends on.
//
// These checks run in two contexts:
//   - The workflow manager calls RunAll before processing each queue item.
//     If any check fails, the lane halts to avoid wasting hours on a doomed run.
//   - The CLI "spindle status" command uses individual check functions
//     (CheckJellyfin, CheckDirectoryAccess) to display service health.
//
// Each check is gated by its config toggle -- disabled features are skipped.
package preflight
