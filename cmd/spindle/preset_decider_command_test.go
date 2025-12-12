package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPresetDeciderTestCommandPrintsJSON(t *testing.T) {
	env := setupCLITestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": `{"profile":"grain","confidence":0.82,"reason":"demo"}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	if err := appendLine(env.configPath, `preset_decider_api_key = "test"`); err != nil {
		t.Fatalf("append config: %v", err)
	}
	if err := appendLine(env.configPath, `preset_decider_base_url = "`+server.URL+`"`); err != nil {
		t.Fatalf("append config: %v", err)
	}
	if err := appendLine(env.configPath, `preset_decider_model = "demo-model"`); err != nil {
		t.Fatalf("append config: %v", err)
	}

	stdout, stderr, err := runCLI(t, []string{"preset-decider", "test"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("expected command to succeed, err=%v stderr=%q", err, stderr)
	}
	requireContains(t, stderr, "System Prompt")
	requireContains(t, stderr, "User Description")
	requireContains(t, stderr, "Response")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("expected stdout to be JSON, got %q: %v", stdout, err)
	}
	if parsed["profile"] != "grain" {
		t.Fatalf("profile=%v, want grain", parsed["profile"])
	}
}
