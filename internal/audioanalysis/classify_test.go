package audioanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/media/ffprobe"
)

func TestClassifyUsesFrozenEvaluatedRubricAndProbability(t *testing.T) {
	type question struct {
		Type, Instructions string
		Criteria           map[string]string
	}
	var frozen struct {
		Threshold float64
		Selected  string
		Questions map[string]question
	}
	data, err := os.ReadFile("testdata/jev-commentary.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &frozen); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, title, text string
		p                 float64
	}{
		{"no title", "", "1\n00:00:01,000 --> 00:00:02,000\nMovie dialogue", 0},
		{"trimmed title", "  Stereo  ", "Mixed commentary", 0.64},
		{"boundary", "Stereo", "Mixed commentary", 0.65},
		{"immediately below", "Stereo", "Mixed commentary", math.Nextafter(0.65, 0)},
		{"exact cap", "", strings.Repeat("a", 4000), 1},
		{"truncated", "", strings.Repeat("a", 4500), 0.87},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/systemone" {
					t.Errorf("wrong endpoint %s", r.URL.Path)
				}
				var body struct {
					Model, State string
					Questions    map[string]question
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.Model != "typesafe/jev-1.13" || len(body.Questions) != 1 || !reflect.DeepEqual(body.Questions["decision"], frozen.Questions[frozen.Selected]) {
					t.Errorf("evaluated rubric changed: %+v", body)
				}
				text := tc.text
				if len(text) > 4000 {
					text = text[:4000] + "\n[truncated]"
				}
				want := ""
				if title := strings.TrimSpace(tc.title); title != "" {
					want = "Title: " + title + "\n\n"
				}
				want += "Transcript sample:\n" + text
				if body.State != want {
					t.Errorf("state mismatch: %q", body.State)
				}
				choice := "not_commentary"
				if tc.p >= 0.5 {
					choice = "commentary"
				}
				// Confidence deliberately differs: only the probability drives policy.
				_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"decision": map[string]any{"type": "choice", "choice": choice, "confidence": 0.1, "probabilities": map[string]float64{"commentary": tc.p, "not_commentary": 1 - tc.p}}}})
			}))
			defer server.Close()
			client := llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL + "/api/v1/chat/completions", Model: "must-not-use-chat"}, nil)
			got, err := Classify(context.Background(), client, tc.title, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			want := "not_commentary"
			if tc.p >= frozen.Threshold {
				want = "commentary"
			}
			if got.Decision != want || got.Probability != tc.p || !strings.Contains(got.Reason, "Jev commentary probability") || !strings.Contains(got.Reason, "0.65") {
				t.Fatalf("result=%+v want=%s", got, want)
			}
		})
	}
}

func TestClassifyTrackPreservesFailuresAndLogsProbability(t *testing.T) {
	response := `{}`
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := llm.New(config.LLMConfig{APIKey: "key", BaseURL: server.URL}, logger)
	handler := New(&config.Config{}, client, nil)
	for _, tc := range []struct {
		name, text, response string
		status               int
	}{
		{"invalid answer", "speech", `{}`, 200},
		{"HTTP error", "speech", `{"error":"unauthorized"}`, 401},
		{"blank transcript", "  \n", `{}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, status = tc.response, tc.status
			ref := handler.classifyTrack(context.Background(), logger, 3, ffprobe.Stream{}, "main", tc.text, true)
			if ref == nil || ref.Index != 3 || ref.Confidence != 0 || !strings.Contains(ref.Reason, "classification failed") {
				t.Fatalf("not preserved: %+v", ref)
			}
			if reason := classifySimilarityExclusion(2, 1, 0.92, ref); reason != "" {
				t.Fatalf("failed classification excluded audio: %s", reason)
			}
		})
	}
	response = `{"answers":{"decision":{"type":"choice","choice":"commentary","confidence":0.3,"probabilities":{"commentary":0.65,"not_commentary":0.35}}}}`
	status = 200
	buf.Reset()
	ref := handler.classifyTrack(context.Background(), logger, 3, ffprobe.Stream{}, "main", "a production remark over dialogue", true)
	if ref == nil || ref.Confidence != 0.65 {
		t.Fatalf("threshold not shared: %+v", ref)
	}
	if reason := classifySimilarityExclusion(2, 1, 0.92, ref); reason != "" {
		t.Fatalf("mixed commentary excluded: %s", reason)
	}
	for _, want := range []string{`"decision_type":"commentary_classification"`, `"decision_result":"commentary"`, `"decision_reason":"Jev commentary probability 0.65 >= 0.65"`, `"commentary_probability":0.65`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %s: %s", want, buf.String())
		}
	}
}

func TestClassifyRejectsMissingEvidenceOrClient(t *testing.T) {
	for _, text := range []string{"", " \n\t", "transcript"} {
		if _, err := Classify(context.Background(), nil, "", text); err == nil {
			t.Fatalf("unexpected success for %q", text)
		}
	}
}
