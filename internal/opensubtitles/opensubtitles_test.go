package opensubtitles

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNew_EmptyAPIKey_ReturnsNil(t *testing.T) {
	c := New("", "agent", "token", "")
	if c != nil {
		t.Fatal("expected nil client when apiKey is empty")
	}
}

func TestNew_Defaults(t *testing.T) {
	c := New("key", "", "", "")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.baseURL != "https://api.opensubtitles.com/api/v1" {
		t.Errorf("unexpected baseURL: %s", c.baseURL)
	}
	if c.userAgent != "Spindle/dev" {
		t.Errorf("unexpected userAgent: %s", c.userAgent)
	}
}

func TestCleanSRT_RemovesHTMLTags(t *testing.T) {
	input := "1\n00:00:01,000 --> 00:00:02,000\n<i>Hello</i> <b>world</b>\n"
	got := CleanSRT(input)
	want := "1\n00:00:01,000 --> 00:00:02,000\nHello world\n"
	if got != want {
		t.Errorf("CleanSRT HTML removal:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestCleanSRT_NormalizesLineEndings(t *testing.T) {
	input := "1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n"
	got := CleanSRT(input)
	want := "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	if got != want {
		t.Errorf("CleanSRT line endings:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestCleanSRT_TrimsEmptyCues(t *testing.T) {
	input := "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n\n\n2\n00:00:03,000 --> 00:00:04,000\nWorld\n"
	got := CleanSRT(input)
	want := "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n2\n00:00:03,000 --> 00:00:04,000\nWorld\n"
	if got != want {
		t.Errorf("CleanSRT empty cues:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestSearch_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/subtitles" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Api-Key") != "test-key" {
			t.Errorf("missing Api-Key header")
		}

		resp := searchResponse{
			Data: []SubtitleResult{
				{
					ID: "123",
					Attributes: SubtitleAttributes{
						Language:         "en",
						DownloadCount:    100,
						ForeignPartsOnly: true,
						Files: []SubtitleFile{
							{FileID: 456, FileName: "test.srt"},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("test-key", "TestAgent", "", srv.URL)
	c.rateDelay = 0 // disable rate limiting for tests

	results, err := c.Search(context.Background(), 550, 0, 0, []string{"en"})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "123" {
		t.Errorf("unexpected result ID: %s", results[0].ID)
	}
	if !results[0].Attributes.ForeignPartsOnly {
		t.Error("expected ForeignPartsOnly to be true")
	}
	if len(results[0].Attributes.Files) != 1 || results[0].Attributes.Files[0].FileID != 456 {
		t.Error("unexpected file data")
	}
}

func TestDownload_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/download" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var reqBody struct {
			FileID    int    `json:"file_id"`
			SubFormat string `json:"sub_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if reqBody.FileID != 456 {
			t.Errorf("unexpected file_id: %d", reqBody.FileID)
		}
		if reqBody.SubFormat != "srt" {
			t.Errorf("unexpected sub_format: %s", reqBody.SubFormat)
		}

		resp := DownloadResponse{
			Link:      "https://example.com/download/abc.srt",
			Remaining: 99,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("test-key", "TestAgent", "user-token", srv.URL)
	c.rateDelay = 0

	dlResp, err := c.Download(context.Background(), 456)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if dlResp.Link != "https://example.com/download/abc.srt" {
		t.Errorf("unexpected link: %s", dlResp.Link)
	}
	if dlResp.Remaining != 99 {
		t.Errorf("unexpected remaining: %d", dlResp.Remaining)
	}
}

func TestCheckHealth_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/infos/formats" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"output_formats":["srt","vtt"]}}`))
	}))
	defer srv.Close()

	c := New("test-key", "TestAgent", "", srv.URL)
	c.rateDelay = 0

	if err := c.CheckHealth(context.Background()); err != nil {
		t.Fatalf("CheckHealth failed: %v", err)
	}
}

func TestCheckHealth_NilClient(t *testing.T) {
	var c *Client
	err := c.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestCheckHealth_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New("test-key", "TestAgent", "", srv.URL)
	c.rateDelay = 0

	if err := c.CheckHealth(context.Background()); err == nil {
		t.Fatal("expected error for 500 response")
	}
}
