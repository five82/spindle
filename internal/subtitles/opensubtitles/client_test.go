package opensubtitles

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSearchBuildsQueryAndParsesResponse(t *testing.T) {
	var captured *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		if r.URL.Path == "/subtitles" {
			resp := map[string]any{
				"data": []map[string]any{
					{
						"id": "1",
						"attributes": map[string]any{
							"language":           "en",
							"release":            "WEBRip",
							"download_count":     120,
							"hearing_impaired":   false,
							"hd":                 true,
							"ai_translated":      false,
							"machine_translated": false,
							"feature_details": map[string]any{
								"feature_type": "movie",
								"title":        "Example Movie",
								"year":         2024,
							},
							"files": []map[string]any{
								{"file_id": 555},
							},
						},
					},
					{
						"id": "2",
						"attributes": map[string]any{
							"language":       "es",
							"download_count": 80,
							"feature_details": map[string]any{
								"feature_type": "movie",
								"title":        "Película",
								"year":         2024,
							},
							"files": []map[string]any{
								{"file_id": 777},
							},
						},
					},
				},
				"meta": map[string]any{"total_count": 2},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(Config{
		APIKey:    "abc",
		UserAgent: "Spindle/test",
		BaseURL:   server.URL,
	})
	if err != nil {
		t.Fatalf("New client failed: %v", err)
	}

	resp, err := client.Search(context.Background(), SearchRequest{
		TMDBID:    12345,
		IMDBID:    "tt7654321",
		Languages: []string{"en", "es"},
		Season:    1,
		Episode:   2,
		Year:      "2024",
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(resp.Subtitles) != 2 {
		t.Fatalf("expected 2 subtitles, got %d", len(resp.Subtitles))
	}
	if resp.Subtitles[0].FileID != 555 || resp.Subtitles[0].Language != "en" {
		t.Fatalf("unexpected first subtitle: %+v", resp.Subtitles[0])
	}
	if resp.Total != 2 {
		t.Fatalf("expected total 2, got %d", resp.Total)
	}

	if captured == nil {
		t.Fatal("expected request to be captured")
	}
	if got := captured.Header.Get("Api-Key"); got != "abc" {
		t.Fatalf("expected api key header, got %q", got)
	}
	if got := captured.Header.Get("User-Agent"); got != "Spindle/test" {
		t.Fatalf("expected user agent header, got %q", got)
	}

	values, _ := url.ParseQuery(captured.URL.RawQuery)
	expect := map[string]string{
		"tmdb_id":        "12345",
		"imdb_id":        "7654321",
		"languages":      "en,es",
		"season_number":  "1",
		"episode_number": "2",
		"year":           "2024",
	}
	for key, want := range expect {
		if got := values.Get(key); got != want {
			t.Fatalf("expected query param %s=%s, got %s", key, want, got)
		}
	}
	if values.Get("type") != "episode" {
		t.Fatalf("expected type to be 'episode', got %q", values.Get("type"))
	}
	if values.Get("order_by") != "download_count" || values.Get("order_direction") != "desc" {
		t.Fatalf("expected ordering params to be set, got %v", values)
	}
}

func TestDownloadFetchesSubtitleData(t *testing.T) {
	var negotiationBody string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			body, _ := io.ReadAll(r.Body)
			negotiationBody = string(body)
			resp := map[string]any{
				"link":      server.URL + "/payload",
				"file_name": "movie.en.srt",
				"language":  "en",
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/payload":
			w.Write([]byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(Config{
		APIKey:    "abc",
		UserAgent: "Spindle/test",
		BaseURL:   server.URL,
	})
	if err != nil {
		t.Fatalf("New client failed: %v", err)
	}

	result, err := client.Download(context.Background(), 42, DownloadOptions{Format: "srt"})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if !strings.Contains(negotiationBody, `"file_id":42`) {
		t.Fatalf("expected negotiation body to contain file_id, got %q", negotiationBody)
	}
	if string(result.Data) == "" {
		t.Fatal("expected subtitle data")
	}
	if result.FileName != "movie.en.srt" {
		t.Fatalf("unexpected filename %q", result.FileName)
	}
	if result.Language != "en" {
		t.Fatalf("unexpected language %q", result.Language)
	}
}
