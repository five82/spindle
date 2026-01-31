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
		TMDBID:       67890,
		ParentTMDBID: 12345,
		IMDBID:       "tt7654321",
		Languages:    []string{"en", "es"},
		Season:       1,
		Episode:      2,
		Year:         "2024",
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
		"tmdb_id":        "67890",
		"parent_tmdb_id": "12345",
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

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "missing api key",
			cfg:     Config{},
			wantErr: true,
		},
		{
			name:    "empty api key",
			cfg:     Config{APIKey: "   "},
			wantErr: true,
		},
		{
			name:    "valid minimal config",
			cfg:     Config{APIKey: "test-key"},
			wantErr: false,
		},
		{
			name: "valid full config",
			cfg: Config{
				APIKey:    "test-key",
				UserAgent: "TestAgent/1.0",
				UserToken: "bearer-token",
				BaseURL:   "https://custom.api.example.com",
			},
			wantErr: false,
		},
		{
			name:    "invalid base url",
			cfg:     Config{APIKey: "test-key", BaseURL: "://invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Error("expected client, got nil")
			}
		})
	}
}

func TestNewDefaultsUserAgent(t *testing.T) {
	client, err := New(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.userAgent != defaultUserAgent {
		t.Errorf("userAgent = %q, want %q", client.userAgent, defaultUserAgent)
	}
}

func TestNewDefaultsBaseURL(t *testing.T) {
	client, err := New(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.baseURL.String() != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", client.baseURL.String(), defaultBaseURL)
	}
}

func TestSanitizeIMDBID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"tt0123456", "0123456"},
		{"0123456", "0123456"},
		{"  tt0123456  ", "0123456"},
		{"invalid", ""},
		{"tt", ""},
		{"ttabc", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeIMDBID(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeIMDBID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSearchForeignPartsOnlyParameter(t *testing.T) {
	tests := []struct {
		name    string
		foreign *bool
		wantVal string
		wantSet bool
	}{
		{
			name:    "nil - no filter",
			foreign: nil,
			wantSet: false,
		},
		{
			name:    "true - only forced",
			foreign: boolPtr(true),
			wantVal: "only",
			wantSet: true,
		},
		{
			name:    "false - exclude forced",
			foreign: boolPtr(false),
			wantVal: "exclude",
			wantSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedURL = r.URL.String()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(searchResponse{})
			}))
			defer server.Close()

			client, err := New(Config{APIKey: "test-key", BaseURL: server.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			_, err = client.Search(context.Background(), SearchRequest{
				TMDBID:           123,
				ForeignPartsOnly: tt.foreign,
			})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}

			if tt.wantSet {
				if !strings.Contains(capturedURL, "foreign_parts_only="+tt.wantVal) {
					t.Errorf("URL %q missing foreign_parts_only=%s", capturedURL, tt.wantVal)
				}
			} else {
				if strings.Contains(capturedURL, "foreign_parts_only") {
					t.Errorf("URL %q should not contain foreign_parts_only", capturedURL)
				}
			}
		})
	}
}

func TestSearchNilClient(t *testing.T) {
	var client *Client
	_, err := client.Search(context.Background(), SearchRequest{})
	if err == nil {
		t.Error("expected error for nil client")
	}
}

func TestSearchHandlesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid api key"))
	}))
	defer server.Close()

	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Search(context.Background(), SearchRequest{TMDBID: 123})
	if err == nil {
		t.Error("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestSearchSkipsEntriesWithoutLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := searchResponse{
			Data: []struct {
				ID         string           `json:"id"`
				Attributes searchAttributes `json:"attributes"`
			}{
				{
					ID: "no-lang",
					Attributes: searchAttributes{
						Language: "",
						Files:    []searchFile{{FileID: 100}},
					},
				},
				{
					ID: "with-lang",
					Attributes: searchAttributes{
						Language: "en",
						Files:    []searchFile{{FileID: 200}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := client.Search(context.Background(), SearchRequest{TMDBID: 123})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(result.Subtitles) != 1 {
		t.Fatalf("expected 1 subtitle (entry without language should be skipped), got %d", len(result.Subtitles))
	}
	if result.Subtitles[0].ID != "with-lang" {
		t.Errorf("expected subtitle with-lang, got %s", result.Subtitles[0].ID)
	}
}

func TestSearchSkipsEntriesWithoutFileID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := searchResponse{
			Data: []struct {
				ID         string           `json:"id"`
				Attributes searchAttributes `json:"attributes"`
			}{
				{
					ID: "no-file",
					Attributes: searchAttributes{
						Language: "en",
						Files:    []searchFile{},
					},
				},
				{
					ID: "with-file",
					Attributes: searchAttributes{
						Language: "en",
						Files:    []searchFile{{FileID: 200}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := client.Search(context.Background(), SearchRequest{TMDBID: 123})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(result.Subtitles) != 1 {
		t.Fatalf("expected 1 subtitle (entry without file_id should be skipped), got %d", len(result.Subtitles))
	}
	if result.Subtitles[0].ID != "with-file" {
		t.Errorf("expected subtitle with-file, got %s", result.Subtitles[0].ID)
	}
}

func TestSearchDetectsAITranslated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := searchResponse{
			Data: []struct {
				ID         string           `json:"id"`
				Attributes searchAttributes `json:"attributes"`
			}{
				{
					ID: "ai-translated",
					Attributes: searchAttributes{
						Language:     "en",
						AITranslated: true,
						Files:        []searchFile{{FileID: 1}},
					},
				},
				{
					ID: "machine-translated",
					Attributes: searchAttributes{
						Language:          "en",
						MachineTranslated: true,
						Files:             []searchFile{{FileID: 2}},
					},
				},
				{
					ID: "human-translated",
					Attributes: searchAttributes{
						Language: "en",
						Files:    []searchFile{{FileID: 3}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := New(Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := client.Search(context.Background(), SearchRequest{TMDBID: 123})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(result.Subtitles) != 3 {
		t.Fatalf("expected 3 subtitles, got %d", len(result.Subtitles))
	}

	// First should be AI translated
	if !result.Subtitles[0].AITranslated {
		t.Error("expected first subtitle to be AI translated")
	}
	// Second should also be flagged (machine translated)
	if !result.Subtitles[1].AITranslated {
		t.Error("expected second subtitle to be flagged as AI translated (machine)")
	}
	// Third should not be flagged
	if result.Subtitles[2].AITranslated {
		t.Error("expected third subtitle NOT to be AI translated")
	}
}

func TestSearchAttributesPrimaryFileID(t *testing.T) {
	tests := []struct {
		name  string
		files []searchFile
		want  int64
	}{
		{
			name:  "empty files",
			files: []searchFile{},
			want:  0,
		},
		{
			name:  "single file",
			files: []searchFile{{FileID: 123}},
			want:  123,
		},
		{
			name:  "multiple files returns first",
			files: []searchFile{{FileID: 100}, {FileID: 200}},
			want:  100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attr := searchAttributes{Files: tt.files}
			got := attr.PrimaryFileID()
			if got != tt.want {
				t.Errorf("PrimaryFileID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDownloadNilClient(t *testing.T) {
	var client *Client
	_, err := client.Download(context.Background(), 123, DownloadOptions{})
	if err == nil {
		t.Error("expected error for nil client")
	}
}

func TestDownloadInvalidFileID(t *testing.T) {
	client, err := New(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Download(context.Background(), 0, DownloadOptions{})
	if err == nil {
		t.Error("expected error for zero file ID")
	}

	_, err = client.Download(context.Background(), -1, DownloadOptions{})
	if err == nil {
		t.Error("expected error for negative file ID")
	}
}

func boolPtr(b bool) *bool {
	return &b
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
