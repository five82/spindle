package deps

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Requirement describes an external dependency that Spindle needs at runtime.
type Requirement struct {
	Name        string
	Command     string
	Description string
	Optional    bool
	Library     bool
}

// Status is the result of checking whether a single Requirement is satisfied.
type Status struct {
	Requirement
	Available bool
	Detail    string
}

// CheckRequirements probes the system PATH for command requirements and the dynamic
// linker cache for library requirements. Results preserve input order.
func CheckRequirements(requirements []Requirement) []Status {
	results := make([]Status, len(requirements))
	for i, req := range requirements {
		path, err := findRequirement(req)
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

func findRequirement(req Requirement) (string, error) {
	if !req.Library {
		return exec.LookPath(req.Command)
	}
	if req.Command == "" {
		return "", errors.New("empty library name")
	}
	if path := libraryFromLDConfig(req.Command); path != "" {
		return path, nil
	}
	if path := libraryFromSearchPath(req.Command); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("library %s", req.Command)
}

func libraryFromLDConfig(name string) string {
	for _, candidate := range []string{"/sbin/ldconfig", "/usr/sbin/ldconfig", "ldconfig"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}

		out, err := exec.Command(path, "-p").Output()
		if err == nil {
			return parseLDConfig(name, string(out))
		}
	}
	return ""
}

func parseLDConfig(name, output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, name) {
			continue
		}
		if _, rest, ok := strings.Cut(line, "=>"); ok {
			return strings.TrimSpace(rest)
		}
		return line
	}
	return ""
}

func libraryFromSearchPath(name string) string {
	dirs := []string{}
	if ldPath := os.Getenv("LD_LIBRARY_PATH"); ldPath != "" {
		dirs = append(dirs, filepath.SplitList(ldPath)...)
	}
	dirs = append(dirs,
		"/lib", "/usr/lib", "/usr/local/lib",
		"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/usr/local/lib/x86_64-linux-gnu",
		"/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/usr/local/lib/aarch64-linux-gnu",
	)
	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, name+"*"))
		if err == nil && len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}
