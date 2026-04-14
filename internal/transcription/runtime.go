package transcription

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"

	"github.com/five82/spindle/internal/logs"
)

const (
	runtimeBootstrapVersion      = "qwen3-runtime-v3"
	defaultPythonCommand         = "3.13"
	defaultTorchIndexURL         = "https://download.pytorch.org/whl/cu128"
	defaultTorchSpec             = "torch==2.8.0 torchvision==0.23.0 torchaudio==2.8.0"
	defaultQwenSpec              = "qwen-asr"
	defaultFlashAttentionVersion = "2.8.3"
)

type Runtime struct {
	mu      sync.Mutex
	rootDir string
	logger  *slog.Logger
}

type RuntimeStatus struct {
	PythonPath            string
	WorkerScriptPath      string
	TorchVersion          string
	QwenASRVersion        string
	FlashAttentionVersion string
	CUDAVisible           bool
	DeviceCount           int
	Ready                 bool
}

type runtimeHealthPayload struct {
	OK                    bool   `json:"ok"`
	Python                string `json:"python"`
	TorchVersion          string `json:"torch_version"`
	QwenASRVersion        string `json:"qwen_asr_version"`
	FlashAttentionVersion string `json:"flash_attention_version,omitempty"`
	CUDAVisible           bool   `json:"cuda_visible"`
	DeviceCount           int    `json:"device_count"`
	Error                 string `json:"error,omitempty"`
}

type flashAttentionRuntimeInfo struct {
	PythonTag  string `json:"python_tag"`
	TorchMinor string `json:"torch_minor"`
	CXX11ABI   bool   `json:"cxx11abi"`
}

func newRuntime(rootDir string, logger *slog.Logger) *Runtime {
	return &Runtime{rootDir: rootDir, logger: logs.Default(logger)}
}

func (r *Runtime) RootDir() string {
	return r.rootDir
}

func (r *Runtime) VenvDir() string {
	return filepath.Join(r.rootDir, ".venv")
}

func (r *Runtime) PythonPath() string {
	return filepath.Join(r.VenvDir(), "bin", "python")
}

func (r *Runtime) WorkerScriptPath() string {
	return filepath.Join(r.rootDir, "qwen_worker.py")
}

func (r *Runtime) EnsureWorkerScript() (string, error) {
	if err := os.MkdirAll(r.rootDir, 0o755); err != nil {
		return "", fmt.Errorf("create transcription runtime root: %w", err)
	}
	path := r.WorkerScriptPath()
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == qwenWorkerScript {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(qwenWorkerScript), 0o755); err != nil {
		return "", fmt.Errorf("write qwen worker script: %w", err)
	}
	return path, nil
}

func (r *Runtime) stampPath() string {
	return filepath.Join(r.rootDir, "bootstrap.stamp")
}

func (r *Runtime) EnsureReady(ctx context.Context, cfg workerConfig) (pythonPath, scriptPath string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pythonPath = r.PythonPath()
	scriptPath = r.WorkerScriptPath()
	stampPath := r.stampPath()
	if runtimeReady(pythonPath, scriptPath, stampPath) {
		if _, err := r.EnsureWorkerScript(); err != nil {
			return "", "", err
		}
		r.logger.Info("transcription runtime ready",
			"decision_type", "transcription_runtime",
			"decision_result", "reused",
			"decision_reason", fmt.Sprintf("root=%s", r.rootDir),
		)
		return pythonPath, scriptPath, nil
	}

	if err := os.RemoveAll(r.rootDir); err != nil {
		return "", "", fmt.Errorf("reset transcription runtime: %w", err)
	}
	if err := os.MkdirAll(r.rootDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create transcription runtime root: %w", err)
	}
	if _, err := r.EnsureWorkerScript(); err != nil {
		return "", "", err
	}

	r.logger.Info("bootstrapping transcription runtime",
		"decision_type", "transcription_runtime",
		"decision_result", "bootstrapped",
		"decision_reason", fmt.Sprintf("root=%s", r.rootDir),
	)

	pythonCmd := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_PYTHON"))
	if pythonCmd == "" {
		pythonCmd = defaultPythonCommand
	}
	if _, err := exec.LookPath("uv"); err != nil {
		return "", "", fmt.Errorf("bootstrap transcription runtime: uv not found in PATH")
	}
	if output, err := runCommand(ctx, "uv", []string{"venv", "--python", pythonCmd, r.VenvDir()}, nil); err != nil {
		return "", "", fmt.Errorf("create transcription venv: %w: %s", err, strings.TrimSpace(output))
	}
	if output, err := runCommand(ctx, "uv", []string{"pip", "install", "--python", pythonPath, "--upgrade", "pip", "setuptools", "wheel"}, nil); err != nil {
		return "", "", fmt.Errorf("install transcription bootstrap tools: %w: %s", err, strings.TrimSpace(output))
	}

	torchArgs := []string{"pip", "install", "--python", pythonPath}
	if usesCUDA(cfg.Device) {
		indexURL := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_TORCH_INDEX_URL"))
		if indexURL == "" {
			indexURL = defaultTorchIndexURL
		}
		torchArgs = append(torchArgs, "--index-url", indexURL, "--extra-index-url", "https://pypi.org/simple")
	}
	torchSpec := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_TORCH_SPEC"))
	if torchSpec == "" {
		torchSpec = defaultTorchSpec
	}
	torchArgs = append(torchArgs, strings.Fields(torchSpec)...)
	if output, err := runCommand(ctx, "uv", torchArgs, nil); err != nil {
		return "", "", fmt.Errorf("install torch runtime packages: %w: %s", err, strings.TrimSpace(output))
	}

	qwenSpec := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_QWEN_SPEC"))
	if qwenSpec == "" {
		qwenSpec = defaultQwenSpec
	}
	if output, err := runCommand(ctx, "uv", append([]string{"pip", "install", "--python", pythonPath}, strings.Fields(qwenSpec)...), r.runtimeEnv()); err != nil {
		return "", "", fmt.Errorf("install qwen runtime packages: %w: %s", err, strings.TrimSpace(output))
	}

	if cfg.UseFlashAttention {
		flashSpec, err := r.resolveFlashAttentionSpec(ctx, pythonPath)
		if err != nil {
			return "", "", fmt.Errorf("resolve flash attention package: %w", err)
		}
		r.logger.Info("installing flash attention runtime package",
			"decision_type", "transcription_flash_attention",
			"decision_result", "selected",
			"decision_reason", flashSpec,
		)
		if output, err := runCommand(ctx, "uv", append([]string{"pip", "install", "--python", pythonPath}, strings.Fields(flashSpec)...), r.runtimeEnv()); err != nil {
			return "", "", fmt.Errorf("install flash attention runtime package: %w: %s", err, strings.TrimSpace(output))
		}
	}

	verifyImports := "import torch; import qwen_asr"
	if cfg.UseFlashAttention {
		verifyImports += "; import flash_attn"
	}
	if output, err := runCommand(ctx, pythonPath, []string{"-c", verifyImports}, r.runtimeEnv()); err != nil {
		return "", "", fmt.Errorf("verify transcription runtime: %w: %s", err, strings.TrimSpace(output))
	}
	if err := os.WriteFile(stampPath, []byte(runtimeBootstrapVersion+"\n"), 0o644); err != nil {
		return "", "", fmt.Errorf("write transcription runtime stamp: %w", err)
	}
	return pythonPath, scriptPath, nil
}

func (r *Runtime) HealthCheck(ctx context.Context, cfg workerConfig) (*RuntimeStatus, error) {
	pythonPath, scriptPath, err := r.EnsureReady(ctx, cfg)
	if err != nil {
		return nil, err
	}
	flashCheck := ""
	if cfg.UseFlashAttention {
		flashCheck = `
try:
    import flash_attn
    payload["flash_attention_version"] = getattr(flash_attn, "__version__", "")
except Exception as exc:
    payload["ok"] = False
    payload["error"] = f"import flash_attn: {exc}"`
	}
	check := `import json, sys
payload = {"ok": True, "python": sys.executable, "torch_version": "", "qwen_asr_version": "", "flash_attention_version": "", "cuda_visible": False, "device_count": 0}
try:
    import torch
    payload["torch_version"] = getattr(torch, "__version__", "")
    payload["cuda_visible"] = bool(torch.cuda.is_available())
    payload["device_count"] = int(torch.cuda.device_count()) if payload["cuda_visible"] else 0
except Exception as exc:
    payload["ok"] = False
    payload["error"] = f"import torch: {exc}"
try:
    import qwen_asr
    payload["qwen_asr_version"] = getattr(qwen_asr, "__version__", "")
except Exception as exc:
    payload["ok"] = False
    payload["error"] = f"import qwen_asr: {exc}"` + flashCheck + `
print(json.dumps(payload))`
	output, err := runCommand(ctx, pythonPath, []string{"-c", check}, r.runtimeEnv())
	if err != nil {
		return nil, fmt.Errorf("runtime health check: %w: %s", err, strings.TrimSpace(output))
	}
	var payload runtimeHealthPayload
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, fmt.Errorf("runtime health parse: %w", err)
	}
	if !payload.OK {
		return nil, fmt.Errorf("runtime health check failed: %s", payload.Error)
	}
	return &RuntimeStatus{
		PythonPath:            pythonPath,
		WorkerScriptPath:      scriptPath,
		TorchVersion:          payload.TorchVersion,
		QwenASRVersion:        payload.QwenASRVersion,
		FlashAttentionVersion: payload.FlashAttentionVersion,
		CUDAVisible:           payload.CUDAVisible,
		DeviceCount:           payload.DeviceCount,
		Ready:                 true,
	}, nil
}

func (r *Runtime) resolveFlashAttentionSpec(ctx context.Context, pythonPath string) (string, error) {
	if spec := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_FLASH_ATTN_SPEC")); spec != "" {
		return spec, nil
	}
	info, err := r.flashAttentionRuntimeInfo(ctx, pythonPath)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(os.Getenv("SPINDLE_TRANSCRIPTION_FLASH_ATTN_VERSION"))
	if version == "" {
		version = defaultFlashAttentionVersion
	}
	platformTag, err := currentFlashAttentionPlatformTag()
	if err != nil {
		return "", err
	}
	return flashAttentionWheelURL(version, info.TorchMinor, info.CXX11ABI, info.PythonTag, platformTag), nil
}

func (r *Runtime) flashAttentionRuntimeInfo(ctx context.Context, pythonPath string) (*flashAttentionRuntimeInfo, error) {
	script := `import json, sys, torch
version = getattr(torch, "__version__", "").split("+")[0]
parts = version.split(".")
payload = {
    "python_tag": f"cp{sys.version_info.major}{sys.version_info.minor}",
    "torch_minor": ".".join(parts[:2]),
    "cxx11abi": bool(torch.compiled_with_cxx11_abi()),
}
print(json.dumps(payload))`
	output, err := runCommand(ctx, pythonPath, []string{"-c", script}, r.runtimeEnv())
	if err != nil {
		return nil, fmt.Errorf("inspect flash attention runtime info: %w: %s", err, strings.TrimSpace(output))
	}
	var info flashAttentionRuntimeInfo
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		return nil, fmt.Errorf("parse flash attention runtime info: %w", err)
	}
	if info.PythonTag == "" || info.TorchMinor == "" {
		return nil, fmt.Errorf("incomplete flash attention runtime info")
	}
	return &info, nil
}

func flashAttentionWheelURL(version, torchMinor string, cxx11abi bool, pythonTag, platformTag string) string {
	abiTag := "FALSE"
	if cxx11abi {
		abiTag = "TRUE"
	}
	wheelName := fmt.Sprintf("flash_attn-%s+cu12torch%scxx11abi%s-%s-%s-%s.whl", version, torchMinor, abiTag, pythonTag, pythonTag, platformTag)
	return fmt.Sprintf("https://github.com/Dao-AILab/flash-attention/releases/download/v%s/%s", version, wheelName)
}

func currentFlashAttentionPlatformTag() (string, error) {
	return flashAttentionPlatformTag(goruntime.GOOS, goruntime.GOARCH)
}

func flashAttentionPlatformTag(goos, goarch string) (string, error) {
	switch {
	case goos == "linux" && goarch == "amd64":
		return "linux_x86_64", nil
	case goos == "linux" && goarch == "arm64":
		return "linux_aarch64", nil
	default:
		return "", fmt.Errorf("unsupported flash attention platform: %s/%s", goos, goarch)
	}
}

func (r *Runtime) runtimeEnv() []string {
	env := os.Environ()
	cacheRoot := filepath.Dir(r.rootDir)
	env = append(env,
		"HF_HOME="+filepath.Join(cacheRoot, "huggingface"),
		"XDG_CACHE_HOME="+cacheRoot,
	)
	return env
}

func usesCUDA(device string) bool {
	device = strings.ToLower(strings.TrimSpace(device))
	return strings.HasPrefix(device, "cuda")
}

func runtimeReady(pythonPath, scriptPath, stampPath string) bool {
	if _, err := os.Stat(pythonPath); err != nil {
		return false
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return false
	}
	data, err := os.ReadFile(stampPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == runtimeBootstrapVersion
}

func runCommand(ctx context.Context, name string, args []string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}
