package stage

import "fmt"

// ErrDegraded indicates a stage completed with degraded behavior. Workflow can
// treat this as a successful stage while logging the degradation.
type ErrDegraded struct {
	Msg   string
	Cause error
}

func (e *ErrDegraded) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Cause)
	}
	return e.Msg
}

func (e *ErrDegraded) Unwrap() error { return e.Cause }
