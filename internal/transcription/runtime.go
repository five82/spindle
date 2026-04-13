package transcription

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	runtimeBootstrapVersion = "parakeet-runtime-v2"
	defaultPythonCommand    = "python3"
)

type runtimeEnv struct {
	mu      sync.Mutex
	rootDir string
}

func newRuntimeEnv(cacheDir string) *runtimeEnv {
	return &runtimeEnv{rootDir: filepath.Join(cacheDir, "runtime", transcriptionBackendName)}
}

func (r *runtimeEnv) venvDir() string {
	return filepath.Join(r.rootDir, ".venv")
}

func (r *runtimeEnv) pythonPath() string {
	return filepath.Join(r.venvDir(), "bin", "python")
}

func (r *runtimeEnv) helperPath() string {
	return filepath.Join(r.rootDir, parakeetHelperFileName)
}

func (r *runtimeEnv) stampPath() string {
	return filepath.Join(r.rootDir, "bootstrap.stamp")
}

func (s *Service) ensureRuntime(ctx context.Context) (pythonPath, helperPath string, err error) {
	r := s.runtime
	r.mu.Lock()
	defer r.mu.Unlock()

	pythonPath = r.pythonPath()
	helperPath = r.helperPath()
	stampPath := r.stampPath()

	if runtimeReady(pythonPath, helperPath, stampPath) {
		s.logger.Info("transcription runtime ready",
			"decision_type", "transcription_runtime",
			"decision_result", "reused",
			"decision_reason", fmt.Sprintf("backend=%s root=%s", transcriptionBackendName, r.rootDir),
		)
		return pythonPath, helperPath, nil
	}

	if err := os.RemoveAll(r.rootDir); err != nil {
		return "", "", fmt.Errorf("reset transcription runtime: %w", err)
	}
	if err := os.MkdirAll(r.rootDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create transcription runtime root: %w", err)
	}
	if err := os.WriteFile(helperPath, []byte(parakeetTranscribeScript), 0o755); err != nil {
		return "", "", fmt.Errorf("write transcription helper: %w", err)
	}

	s.logger.Info("bootstrapping transcription runtime",
		"decision_type", "transcription_runtime",
		"decision_result", "bootstrapped",
		"decision_reason", fmt.Sprintf("backend=%s root=%s", transcriptionBackendName, r.rootDir),
	)

	pythonCmd := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_PYTHON"))
	if pythonCmd == "" {
		pythonCmd = defaultPythonCommand
	}

	if output, err := runCommand(ctx, "uv", []string{"venv", "--python", pythonCmd, r.venvDir()}, nil); err != nil {
		return "", "", fmt.Errorf("create transcription venv: %w: %s", err, strings.TrimSpace(output))
	}

	if output, err := runCommand(ctx, "uv", []string{"pip", "install", "--python", pythonPath, "--upgrade", "pip", "setuptools", "wheel"}, nil); err != nil {
		return "", "", fmt.Errorf("install transcription bootstrap tools: %w: %s", err, strings.TrimSpace(output))
	}

	installSpec := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_NEMO_SPEC"))
	if installSpec == "" {
		installSpec = s.runtimeInstallSpec()
	}
	if output, err := runCommand(ctx, "uv", []string{"pip", "install", "--python", pythonPath, installSpec}, nil); err != nil {
		return "", "", fmt.Errorf("install transcription runtime packages: %w: %s", err, strings.TrimSpace(output))
	}

	verifyEnv := s.runtimeEnv()
	if output, err := runCommand(ctx, pythonPath, []string{"-c", "import torch; import nemo.collections.asr"}, verifyEnv); err != nil {
		return "", "", fmt.Errorf("verify transcription runtime: %w: %s", err, strings.TrimSpace(output))
	}
	if err := os.WriteFile(stampPath, []byte(runtimeBootstrapVersion+"\n"), 0o644); err != nil {
		return "", "", fmt.Errorf("write transcription runtime stamp: %w", err)
	}
	return pythonPath, helperPath, nil
}

func runtimeReady(pythonPath, helperPath, stampPath string) bool {
	if _, err := os.Stat(pythonPath); err != nil {
		return false
	}
	if _, err := os.Stat(helperPath); err != nil {
		return false
	}
	data, err := os.ReadFile(stampPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == runtimeBootstrapVersion
}

func (s *Service) runtimeInstallSpec() string {
	if s.device == "cpu" {
		return "nemo-toolkit[asr]>=2.4.0,<3"
	}
	return "nemo-toolkit[asr,cu13]>=2.4.0,<3"
}

func (s *Service) runtimeEnv() []string {
	env := os.Environ()
	env = append(env, "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	cacheRoot := filepath.Dir(s.cacheDir)
	env = append(env,
		"HF_HOME="+filepath.Join(cacheRoot, "huggingface"),
		"XDG_CACHE_HOME="+cacheRoot,
	)
	return env
}

func runCommand(ctx context.Context, name string, args []string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}
