package transcription

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeEnsureWorkerScript(t *testing.T) {
	runtime := newRuntime(t.TempDir(), nil)
	path, err := runtime.EnsureWorkerScript()
	if err != nil {
		t.Fatalf("EnsureWorkerScript() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read worker script: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty worker script")
	}
	if filepath.Base(path) != "qwen_worker.py" {
		t.Fatalf("unexpected worker script path %q", path)
	}
}

func TestFlashAttentionWheelURL(t *testing.T) {
	got := flashAttentionWheelURL("2.8.3", "2.8", true, "cp313", "linux_x86_64")
	want := "https://github.com/Dao-AILab/flash-attention/releases/download/v2.8.3/flash_attn-2.8.3+cu12torch2.8cxx11abiTRUE-cp313-cp313-linux_x86_64.whl"
	if got != want {
		t.Fatalf("flashAttentionWheelURL() = %q, want %q", got, want)
	}
}

func TestFlashAttentionPlatformTag(t *testing.T) {
	got, err := flashAttentionPlatformTag("linux", "amd64")
	if err != nil {
		t.Fatalf("flashAttentionPlatformTag() error = %v", err)
	}
	if got != "linux_x86_64" {
		t.Fatalf("flashAttentionPlatformTag() = %q, want linux_x86_64", got)
	}
}
