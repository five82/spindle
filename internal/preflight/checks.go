package preflight

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/services/llm"
)

// CheckLLM verifies that the LLM API is reachable and the key is valid.
// It uses a 30-second timeout and a single attempt (no retries).
func CheckLLM(ctx context.Context, name string, cfg config.LLMConfig) Result {
	if cfg.APIKey == "" {
		return Result{Name: name, Detail: "API key missing"}
	}

	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := llm.NewClient(llm.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		Referer: cfg.Referer,
		Title:   cfg.Title,
	}, llm.WithRetryMaxAttempts(1))

	if err := client.HealthCheck(checkCtx); err != nil {
		return Result{Name: name, Detail: summarizeLLMError(err)}
	}
	return Result{Name: name, Passed: true, Detail: "API reachable"}
}

// CheckJellyfin verifies Jellyfin connectivity and authentication.
func CheckJellyfin(ctx context.Context, baseURL, apiKey string) Result {
	const name = "Jellyfin"

	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return Result{Name: name, Detail: "missing url"}
	}
	if strings.TrimSpace(apiKey) == "" {
		return Result{Name: name, Detail: "missing api key"}
	}

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, base+"/Users", nil)
	if err != nil {
		return Result{Name: name, Detail: fmt.Sprintf("auth check failed (%v)", err)}
	}
	req.Header.Set("X-Emby-Token", strings.TrimSpace(apiKey))

	resp, err := client.Do(req)
	if err != nil {
		return Result{Name: name, Detail: fmt.Sprintf("auth check failed (%v)", err)}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return Result{Name: name, Passed: true, Detail: "Reachable"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return Result{Name: name, Detail: "auth failed (invalid api key)"}
	default:
		return Result{Name: name, Detail: fmt.Sprintf("auth check failed (%d)", resp.StatusCode)}
	}
}

// CheckDirectoryAccess verifies that the directory exists and is readable/writable.
func CheckDirectoryAccess(name, path string) Result {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Name: name, Detail: fmt.Sprintf("%s (error: does not exist)", path)}
		}
		return Result{Name: name, Detail: fmt.Sprintf("%s (error: stat: %v)", path, err)}
	}
	if !info.IsDir() {
		return Result{Name: name, Detail: fmt.Sprintf("%s (error: is not a directory)", path)}
	}
	if err := unix.Access(path, unix.R_OK|unix.W_OK|unix.X_OK); err != nil {
		return Result{Name: name, Detail: fmt.Sprintf("%s (error: insufficient permissions: %v)", path, err)}
	}
	return Result{Name: name, Passed: true, Detail: fmt.Sprintf("%s (read/write ok)", path)}
}

// CheckSystemDeps evaluates all system-level dependencies for the given config.
// Both the daemon and the CLI status command use this to avoid duplicating
// the requirements list. LLM checks are not included here because only the
// CLI status path uses them.
func CheckSystemDeps(ctx context.Context, cfg *config.Config) []deps.Status {
	requirements := []deps.Requirement{
		{
			Name:        "MakeMKV",
			Command:     cfg.MakemkvBinary(),
			Description: "Required for disc ripping",
		},
		{
			Name:        "FFmpeg",
			Command:     deps.ResolveFFmpegPath(),
			Description: "Required for encoding",
		},
		{
			Name:        "FFprobe",
			Command:     deps.ResolveFFprobePath(cfg.FFprobeBinary()),
			Description: "Required for media inspection",
		},
		{
			Name:        "MediaInfo",
			Command:     "mediainfo",
			Description: "Required for metadata inspection",
		},
		{
			Name:        "bd_info",
			Command:     "bd_info",
			Description: "Enhances disc metadata when MakeMKV titles are generic",
			Optional:    true,
		},
	}
	if cfg.Subtitles.Enabled {
		requirements = append(requirements, deps.Requirement{
			Name:        "uvx",
			Command:     "uvx",
			Description: "Required for WhisperX-driven transcription",
		})
		if cfg.Subtitles.MuxIntoMKV {
			requirements = append(requirements, deps.Requirement{
				Name:        "mkvmerge",
				Command:     "mkvmerge",
				Description: "Required for muxing subtitles into MKV containers",
			})
		}
	}
	return deps.CheckBinaries(requirements)
}

// summarizeLLMError produces a human-readable summary for LLM health check failures.
func summarizeLLMError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "health check timed out (LLM API unresponsive)"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "health check timed out (LLM API unreachable)"
	}
	return err.Error()
}
