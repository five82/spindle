package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestChoiceRoundingBoundaries(t *testing.T) {
	many := make(map[string]float64)
	for i := range 25 {
		many[fmt.Sprint(i)] = 0.0388 // 0.97 total: option count must not widen tolerance.
	}
	for _, tt := range []struct {
		name, choice  string
		probabilities map[string]float64
		wantError     bool
	}{
		{"mass 0.99", "a", map[string]float64{"a": 0.70, "b": 0.20, "c": 0.09}, false},
		{"mass 1.01", "a", map[string]float64{"a": 0.71, "b": 0.20, "c": 0.10}, false},
		{"binary lower boundary", "a", map[string]float64{"a": 0.90, "b": 0.09}, false},
		{"binary upper boundary", "a", map[string]float64{"a": 0.91, "b": 0.10}, false},
		{"never normalize across acceptance threshold", "a", map[string]float64{"a": 0.899, "b": 0.091}, false},
		{"rounding winner inversion", "a", map[string]float64{"a": 0.38, "b": 0.39, "c": 0.22}, false},
		{"tie", "a", map[string]float64{"a": 0.5, "b": 0.5}, false},
		{"outside lower boundary", "a", map[string]float64{"a": 0.90, "b": 0.089999}, true},
		{"outside upper boundary", "a", map[string]float64{"a": 0.91, "b": 0.100001}, true},
		{"mass 0.98", "a", map[string]float64{"a": 0.89, "b": 0.09}, true},
		{"mass 1.02", "a", map[string]float64{"a": 0.92, "b": 0.10}, true},
		{"outside winner boundary", "a", map[string]float64{"a": 0.38, "b": 0.390001, "c": 0.229999}, true},
		{"winner contradicts probabilities", "a", map[string]float64{"a": 0.38, "b": 0.40, "c": 0.22}, true},
		{"large catalog does not increase allowance", "0", many, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"answers": map[string]any{"decision": map[string]any{
				"type": "choice", "choice": tt.choice, "confidence": 0.3, "probabilities": tt.probabilities,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
			defer server.Close()
			client := New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			criteria := make(map[string]string)
			for k := range tt.probabilities {
				criteria[k] = k
			}
			got, err := client.Choice(context.Background(), "state", "instructions", criteria)
			if tt.wantError {
				if err == nil || got != nil {
					t.Fatalf("malformed distribution accepted: %v, %v", got, err)
				}
			} else if err != nil || !reflect.DeepEqual(got, tt.probabilities) {
				t.Fatalf("probabilities must be returned unchanged: got %v, %v; want %v", got, err, tt.probabilities)
			}
		})
	}
}

func TestChoiceRecordedMulticlassResponses(t *testing.T) {
	// Minimal raw answers from the 2026-09-28 episode evaluation, returned by
	// typesafe/jev-1.13-20260917. No transcript text or credentials are retained.
	data, err := os.ReadFile("testdata/jev-choice-rounding.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string
		Criteria []string
		Response json.RawMessage
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("empty fixture")
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(f.Response) }))
			defer server.Close()
			client := New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			criteria := make(map[string]string)
			for _, k := range f.Criteria {
				criteria[k] = k
			}
			var raw struct {
				Answers map[string]struct{ Probabilities map[string]float64 }
			}
			if err := json.Unmarshal(f.Response, &raw); err != nil {
				t.Fatal(err)
			}
			got, err := client.Choice(context.Background(), "state", "instructions", criteria)
			if err != nil || !reflect.DeepEqual(got, raw.Answers["decision"].Probabilities) {
				t.Fatalf("recorded response rejected or altered: %v, %v", got, err)
			}
		})
	}
}
