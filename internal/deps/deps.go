package deps

import (
	"fmt"
	"os"
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

// ResolveFFmpegPath returns the path to ffmpeg by checking, in order:
//
//  1. SPINDLE_FFMPEG_PATH env var
//  2. FFMPEG_PATH env var
//  3. PATH lookup for "ffmpeg"
//  4. Literal "ffmpeg" as a last resort
func ResolveFFmpegPath() string {
	return resolveToolPath([]string{"SPINDLE_FFMPEG_PATH", "FFMPEG_PATH"}, "ffmpeg")
}

// ResolveFFprobePath returns the path to ffprobe by checking, in order:
//
//  1. SPINDLE_FFPROBE_PATH env var
//  2. FFPROBE_PATH env var
//  3. PATH lookup for defaultName
//  4. Literal "ffprobe" as a last resort
func ResolveFFprobePath(defaultName string) string {
	return resolveToolPath([]string{"SPINDLE_FFPROBE_PATH", "FFPROBE_PATH"}, defaultName)
}

// resolveToolPath checks env vars in order, verifies the resolved path is an
// executable file, then falls back to a PATH lookup of fallbackName, and
// finally returns fallbackName as a literal.
func resolveToolPath(envVars []string, fallbackName string) string {
	for _, env := range envVars {
		if p := os.Getenv(env); p != "" {
			if isExecutableFile(p) {
				return p
			}
		}
	}

	if p, err := exec.LookPath(fallbackName); err == nil {
		return p
	}

	return fallbackName
}

// isExecutableFile returns true when path refers to a regular file (not a
// directory) that has at least one execute permission bit set.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0111 != 0
}
