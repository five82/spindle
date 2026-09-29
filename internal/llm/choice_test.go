package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
)

func TestChoiceRequestAndClientReuse(t *testing.T) {
	var choiceCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer key" || r.Header.Get("HTTP-Referer") != "https://spindle.test" || r.Header.Get("X-Title") != "Spindle test" {
			t.Errorf("headers/method: %s %v", r.Method, r.Header)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/api/v1/systemone":
			choiceCalls.Add(1)
			if len(body) != 3 || string(body["model"]) != `"typesafe/jev-1.13"` || string(body["state"]) != `"a ticket"` {
				t.Errorf("native request: %s", body)
			}
			var questions map[string]struct {
				Type, Instructions string
				Criteria           map[string]string
			}
			if err := json.Unmarshal(body["questions"], &questions); err != nil {
				t.Error(err)
				return
			}
			q := questions["decision"]
			if len(questions) != 1 || q.Type != "choice" || q.Instructions != "Route this ticket" || q.Criteria["review"] != "Needs a person" {
				t.Errorf("question: %+v", questions)
			}
			_, _ = io.WriteString(w, `{"answers":{"decision":{"type":"choice","choice":"review","confidence":0.3,"probabilities":{"accept":0.2,"review":0.7,"reject":0.1}}}}`)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var logs bytes.Buffer
	client := New(config.LLMConfig{APIKey: "key", BaseURL: server.URL + "/api/v1/", Referer: "https://spindle.test", Title: "Spindle test", TimeoutSeconds: 7}, slog.New(slog.NewTextHandler(&logs, nil)))
	if client.client.Timeout != 7*time.Second {
		t.Fatal("timeout not shared")
	}
	for range 2 {
		p, err := client.Choice(context.Background(), "a ticket", "Route this ticket", map[string]string{"accept": "Can proceed", "review": "Needs a person", "reject": "Cannot proceed"})
		if err != nil || p["review"] != 0.7 || len(p) != 3 {
			t.Fatalf("choice=%v err=%v", p, err)
		}
	}
	if choiceCalls.Load() != 2 {
		t.Fatal("incorrect request count")
	}
	if !strings.Contains(logs.String(), "model=typesafe/jev-1.13") {
		t.Errorf("missing model logging: %s", logs.String())
	}
	if New(config.LLMConfig{}, nil) != nil {
		t.Fatal("missing API key must disable client")
	}
}

func TestChoiceRejectsMalformedDistributions(t *testing.T) {
	valid := `{"type":"choice","choice":"yes","confidence":0.7,"probabilities":{"yes":0.85,"no":0.15}}`
	cases := []string{
		"null", `{}`, `{"type":"noul","noul":0.9}`,
		strings.Replace(valid, `"confidence":0.7`, `"confidence":null`, 1),
		strings.Replace(valid, `"confidence":0.7`, `"confidence":1.7`, 1),
		strings.Replace(valid, `"confidence":0.7`, `"confidence":-0.1`, 1),
		strings.Replace(valid, `"confidence":0.7,`, ``, 1),
		strings.Replace(valid, `"yes":0.85`, `"yes":NaN`, 1),
		strings.Replace(valid, `"yes":0.85`, `"yes":Infinity`, 1),
		strings.Replace(valid, `"yes":0.85`, `"yes":1e309`, 1),
		strings.Replace(valid, `"yes":0.85`, `"yes":null`, 1),
		strings.Replace(valid, `"yes":0.85`, `"yes":-0.1`, 1),
		strings.Replace(valid, `"yes":0.85`, `"yes":1.1`, 1),
		strings.Replace(valid, `"yes":0.85`, `"maybe":0.85`, 1),
		strings.Replace(valid, `"yes":0.85`, `"yes":0.5`, 1),
		strings.Replace(valid, `"choice":"yes"`, `"choice":"no"`, 1),
		strings.Replace(valid, `"choice":"yes"`, `"choice":"other"`, 1),
		strings.Replace(valid, `"no":0.15`, `"no":0.15,"other":0`, 1),
	}
	response := ""
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = io.WriteString(w, response) }))
	defer server.Close()
	client := New(config.LLMConfig{APIKey: "key", BaseURL: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bodies := []string{"invalid json", `{}`, `{"answers":{"wrong":` + valid + `}}`, `{"answers":{"decision":` + valid + `,"extra":` + valid + `}}`}
	for _, answer := range cases {
		bodies = append(bodies, `{"answers":{"decision":`+answer+`}}`)
	}
	for _, body := range bodies {
		response = body
		before := calls.Load()
		probabilities, err := client.Choice(context.Background(), "state", "instructions", map[string]string{"yes": "Yes", "no": "No"})
		if err == nil || probabilities != nil || calls.Load() != before+1 {
			t.Errorf("must fail once without partial probabilities: %s got=%v err=%v", body, probabilities, err)
		}
	}
}

func TestChoiceRetriesHTTPFailureAndHonorsCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "overloaded", 529)
			return
		}
		_, _ = io.WriteString(w, `{"answers":{"decision":{"type":"choice","choice":"yes","confidence":0.8,"probabilities":{"yes":0.9,"no":0.1}}}}`)
	}))
	defer server.Close()
	client := New(config.LLMConfig{APIKey: "key", BaseURL: server.URL}, nil)
	criteria := map[string]string{"yes": "Yes", "no": "No"}
	if _, err := client.Choice(context.Background(), "state", "instructions", criteria); err != nil || calls.Load() != 2 {
		t.Fatalf("retry: calls=%d err=%v", calls.Load(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Choice(ctx, "state", "instructions", criteria); err == nil {
		t.Fatal("canceled request succeeded")
	}
	if calls.Load() != 2 {
		t.Fatal("canceled request reached server")
	}
	if _, err := ((*Client)(nil)).Choice(ctx, "", "", criteria); err == nil {
		t.Fatal("nil client succeeded")
	}
	if _, err := client.Choice(ctx, "", "", map[string]string{"only": "one"}); err == nil {
		t.Fatal("single option accepted")
	}
}

func TestChoiceDefaultOpenRouterEndpoint(t *testing.T) {
	client := New(config.LLMConfig{APIKey: "key"}, nil)
	client.client = &http.Client{Transport: choiceTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://openrouter.ai/api/v1/systemone" {
			t.Errorf("endpoint=%s", r.URL)
		}
		return nil, fmt.Errorf("test transport failure")
	})}
	if _, err := client.Choice(context.Background(), "state", "question", map[string]string{"a": "A", "b": "B"}); err == nil {
		t.Fatal("transport error ignored")
	}
}

type choiceTransport func(*http.Request) (*http.Response, error)

func (f choiceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
