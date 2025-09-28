package deps

import (
	"fmt"
	"os/exec"
	"strings"
)

// Requirement defines an external dependency Spindle relies on.
type Requirement struct {
	Name        string
	Command     string
	Description string
	Optional    bool
}

// Status reports the availability of a dependency.
type Status struct {
	Name        string
	Command     string
	Description string
	Optional    bool
	Available   bool
	Detail      string
}

// CheckBinaries evaluates the provided requirements and reports availability.
func CheckBinaries(requirements []Requirement) []Status {
	results := make([]Status, 0, len(requirements))
	for _, req := range requirements {
		cmd := strings.TrimSpace(req.Command)
		status := Status{
			Name:        req.Name,
			Command:     cmd,
			Description: strings.TrimSpace(req.Description),
			Optional:    req.Optional,
		}
		if cmd == "" {
			status.Available = false
			status.Detail = "command not configured"
			results = append(results, status)
			continue
		}
		if _, err := exec.LookPath(cmd); err != nil {
			status.Available = false
			status.Detail = fmt.Sprintf("binary %q not found", cmd)
			results = append(results, status)
			continue
		}
		status.Available = true
		results = append(results, status)
	}
	return results
}
