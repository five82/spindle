package deps

import (
	"fmt"
	"os/exec"
)

// Requirement describes a binary dependency that Spindle needs at runtime.
type Requirement struct {
	Name        string
	Command     string
	Description string
	Optional    bool
}

// Status is the result of checking whether a single Requirement is satisfied.
type Status struct {
	Requirement
	Available bool
	Detail    string
}

// CheckBinaries probes the system PATH for each requirement and returns a
// Status slice in the same order. Available is true when exec.LookPath finds
// the command; Detail holds the resolved path or the error message.
func CheckBinaries(requirements []Requirement) []Status {
	results := make([]Status, len(requirements))
	for i, req := range requirements {
		path, err := exec.LookPath(req.Command)
		if err != nil {
			results[i] = Status{
				Requirement: req,
				Available:   false,
				Detail:      fmt.Sprintf("not found: %v", err),
			}
		} else {
			results[i] = Status{
				Requirement: req,
				Available:   true,
				Detail:      path,
			}
		}
	}
	return results
}
