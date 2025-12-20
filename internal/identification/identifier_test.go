package identification_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"spindle/internal/disc"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/testsupport"
)

func TestIdentifierTransitionsToIdentified(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	item, err := store.NewDisc(context.Background(), "Demo Disc", "fp-demo")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	stubTMDB := &stubSearcher{resp: &tmdb.Response{Results: []tmdb.Result{{ID: 1, Title: "Demo Disc", VoteAverage: 8.5, VoteCount: 200, ReleaseDate: "2001-05-20"}}, TotalResults: 1}}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-demo", Titles: []disc.Title{{ID: 1, Name: "Demo Disc", Duration: 7000}}}}
	notifier := &recordingNotifier{}
	handler := identification.NewIdentifierWithDependencies(cfg, store, logging.NewNop(), stubTMDB, stubScanner, notifier)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = queue.StatusIdentified
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != queue.StatusIdentified {
		t.Fatalf("expected status identified, got %s", updated.Status)
	}
	if updated.MetadataJSON == "" {
		t.Fatal("expected metadata to be stored")
	}
	if len(notifier.detected) != 1 {
		t.Fatalf("expected disc detected notification, got %d", len(notifier.detected))
	}
	if len(notifier.identified) != 1 {
		t.Fatalf("expected identification notification, got %d", len(notifier.identified))
	}
	if notifier.identified[0].title != "Demo Disc" {
		t.Fatalf("unexpected identification title %q", notifier.identified[0].title)
	}
	if notifier.identified[0].year != "2001" {
		t.Fatalf("unexpected identification year %q", notifier.identified[0].year)
	}

	var spec struct {
		ContentKey string `json:"content_key"`
		Titles     []struct {
			ContentFingerprint string `json:"content_fingerprint"`
		} `json:"titles"`
	}
	if err := json.Unmarshal([]byte(updated.RipSpecData), &spec); err != nil {
		t.Fatalf("decode rip spec: %v", err)
	}
	if spec.ContentKey != "tmdb:movie:1" {
		t.Fatalf("expected content key tmdb:movie:1, got %q", spec.ContentKey)
	}
	if len(spec.Titles) == 0 || spec.Titles[0].ContentFingerprint == "" {
		t.Fatal("expected per-title content fingerprint")
	}
}

func TestIdentifierFallsBackToQueueFingerprint(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	item, err := store.NewDisc(context.Background(), "Fallback Disc", "fp-fallback")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	originalFingerprint := item.DiscFingerprint

	stubTMDB := &stubSearcher{resp: &tmdb.Response{Results: []tmdb.Result{{ID: 42, Title: "Fallback Disc", VoteAverage: 7.0, VoteCount: 100, ReleaseDate: "2010-01-01"}}, TotalResults: 1}}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "", Titles: []disc.Title{{ID: 1, Name: "Fallback Disc", Duration: 7200}}}}
	handler := identification.NewIdentifierWithDependencies(cfg, store, logging.NewNop(), stubTMDB, stubScanner, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.TrimSpace(item.RipSpecData) == "" {
		t.Fatal("expected rip spec data to be populated")
	}
	var spec struct {
		Fingerprint string `json:"fingerprint"`
		ContentKey  string `json:"content_key"`
	}
	if err := json.Unmarshal([]byte(item.RipSpecData), &spec); err != nil {
		t.Fatalf("Unmarshal rip spec: %v", err)
	}
	if spec.Fingerprint != item.DiscFingerprint {
		t.Fatalf("expected fallback fingerprint %q, got %q", item.DiscFingerprint, spec.Fingerprint)
	}
	if spec.ContentKey != "tmdb:movie:42" {
		t.Fatalf("expected tmdb content key, got %q", spec.ContentKey)
	}
	if item.DiscFingerprint != originalFingerprint {
		t.Fatalf("expected disc fingerprint to remain %q, got %q", originalFingerprint, item.DiscFingerprint)
	}
}

func TestIdentifierMarksDuplicateForReview(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	ctx := context.Background()
	first, err := store.NewDisc(ctx, "Existing", "fp-existing")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	first.Status = queue.StatusCompleted
	if err := store.Update(ctx, first); err != nil {
		t.Fatalf("Update: %v", err)
	}

	second, err := store.NewDisc(ctx, "Duplicate", "fp-dup")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	stubTMDB := &stubSearcher{resp: &tmdb.Response{Results: []tmdb.Result{{ID: 2, Title: "Duplicate", VoteAverage: 9.0, VoteCount: 500}}, TotalResults: 1}}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: first.DiscFingerprint}}
	notifier := &recordingNotifier{}
	handler := identification.NewIdentifierWithDependencies(cfg, store, logging.NewNop(), stubTMDB, stubScanner, notifier)
	if err := handler.Prepare(ctx, second); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(ctx, second); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if second.Status != queue.StatusReview {
		t.Fatalf("expected review status, got %s", second.Status)
	}
	if !second.NeedsReview {
		t.Fatal("expected duplicate to require review")
	}
	if len(notifier.unidentified) != 1 {
		t.Fatalf("expected unidentified media notification, got %d", len(notifier.unidentified))
	}
}

func TestIdentifierSkipsDuplicateCheckWithoutStore(t *testing.T) {
	cfg := testsupport.NewConfig(t)

	stubTMDB := &stubSearcher{resp: &tmdb.Response{Results: []tmdb.Result{{ID: 3, Title: "CLI Disc", VoteAverage: 7.5, VoteCount: 50, ReleaseDate: "2005-02-01"}}, TotalResults: 1}}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-cli", Titles: []disc.Title{{ID: 1, Name: "", Duration: 7200}}}}
	handler := identification.NewIdentifierWithDependencies(cfg, nil, logging.NewNop(), stubTMDB, stubScanner, nil)
	item := &queue.Item{DiscTitle: "CLI Disc", Status: queue.StatusPending}

	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if item.NeedsReview {
		t.Fatalf("expected identification without store to skip duplicate review, got %q", item.ReviewReason)
	}
	if strings.TrimSpace(item.MetadataJSON) == "" {
		t.Fatal("expected metadata to be populated")
	}
}

func TestIdentifierMarksReviewWhenNoResults(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	item, err := store.NewDisc(context.Background(), "Unknown", "fp-unknown")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	stubTMDB := &stubSearcher{resp: &tmdb.Response{Results: []tmdb.Result{}, TotalResults: 0}}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-unknown"}}
	notifier := &recordingNotifier{}
	handler := identification.NewIdentifierWithDependencies(cfg, store, logging.NewNop(), stubTMDB, stubScanner, notifier)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !item.NeedsReview {
		t.Fatal("expected item to require review")
	}
	if item.Status != queue.StatusIdentified {
		t.Fatalf("expected status identified, got %s", item.Status)
	}
	if item.ReviewReason == "" {
		t.Fatal("expected review reason to be recorded")
	}
	if len(notifier.unidentified) != 0 {
		t.Fatalf("expected no immediate notification, got %d", len(notifier.unidentified))
	}
	if strings.TrimSpace(item.RipSpecData) == "" {
		t.Fatal("expected rip spec data for unidentified content")
	}
}

func TestIdentifierAnnotatesTVEpisodes(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	item, err := store.NewDisc(context.Background(), "South Park", "fp-southpark")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	seasonDetails := &tmdb.SeasonDetails{
		SeasonNumber: 5,
		Episodes: []tmdb.Episode{
			{ID: 1, Name: "Scott Tenorman Must Die", SeasonNumber: 5, EpisodeNumber: 1, Runtime: 22, AirDate: "2001-07-11"},
			{ID: 2, Name: "It Hits the Fan", SeasonNumber: 5, EpisodeNumber: 2, Runtime: 22, AirDate: "2001-06-20"},
			{ID: 3, Name: "Cripple Fight", SeasonNumber: 5, EpisodeNumber: 3, Runtime: 22, AirDate: "2001-06-27"},
			{ID: 4, Name: "Super Best Friends", SeasonNumber: 5, EpisodeNumber: 4, Runtime: 22, AirDate: "2001-07-04"},
		},
	}
	resp := &tmdb.Response{Results: []tmdb.Result{{ID: 123, Title: "South Park Season 5 - Disc 1", Name: "South Park", MediaType: "tv", FirstAirDate: "1997-08-13", VoteAverage: 8.4, VoteCount: 200}}}
	stubTMDB := &stubSearcher{
		resp:   resp,
		tvResp: resp,
		season: seasonDetails,
	}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{
		Fingerprint: "fp-southpark",
		Titles: []disc.Title{
			{ID: 0, Name: "", Duration: 1320},
			{ID: 1, Name: "", Duration: 1320},
			{ID: 2, Name: "", Duration: 1320},
			{ID: 3, Name: "", Duration: 1320},
		},
		BDInfo: &disc.BDInfoResult{DiscID: "ABC123", DiscName: "South Park Season 5 Disc 1", VolumeIdentifier: "SOUTHPARK5_DISC1"},
	}}
	handler := identification.NewIdentifierWithDependencies(cfg, store, logging.NewNop(), stubTMDB, stubScanner, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("tmdb calls movie=%d tv=%d multi=%d season=%d", stubTMDB.movieCalls, stubTMDB.tvCalls, stubTMDB.multiCalls, stubTMDB.seasonCalls)
	if !strings.Contains(item.MetadataJSON, "\"media_type\":\"tv\"") {
		t.Fatalf("expected tv media type in metadata, status=%s review=%v reason=%s json=%s", item.Status, item.NeedsReview, item.ReviewReason, item.MetadataJSON)
	}
	if !strings.Contains(item.MetadataJSON, "season_number") {
		t.Fatalf("expected season metadata, got %s", item.MetadataJSON)
	}
	if !strings.Contains(item.RipSpecData, "\"season\":5") {
		t.Fatalf("expected rip spec to include season annotations, got %s", item.RipSpecData)
	}
	if !strings.Contains(item.RipSpecData, "\"episode\":1") {
		t.Fatalf("expected rip spec to include episode mapping, got %s", item.RipSpecData)
	}
}

func TestIdentifierHealthReady(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	handler := identification.NewIdentifierWithDependencies(cfg, store, logging.NewNop(), &stubSearcher{}, &stubDiscScanner{}, nil)
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected health ready, got %+v", health)
	}
	if health.Detail != "" {
		t.Fatalf("expected empty detail, got %q", health.Detail)
	}
}

func TestIdentifierHealthMissingAPIKey(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.TMDB.APIKey = ""
	store := testsupport.MustOpenStore(t, cfg)

	handler := identification.NewIdentifierWithDependencies(cfg, store, logging.NewNop(), &stubSearcher{}, &stubDiscScanner{}, nil)
	health := handler.HealthCheck(context.Background())
	if health.Ready {
		t.Fatalf("expected health not ready, got %+v", health)
	}
	if !strings.Contains(strings.ToLower(health.Detail), "api key") {
		t.Fatalf("expected detail to mention api key, got %q", health.Detail)
	}
}

type stubSearcher struct {
	resp        *tmdb.Response
	tvResp      *tmdb.Response
	multiResp   *tmdb.Response
	season      *tmdb.SeasonDetails
	err         error
	movieCalls  int
	tvCalls     int
	multiCalls  int
	seasonCalls int
}

func (s *stubSearcher) SearchMovieWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	s.movieCalls++
	if s.err != nil {
		return nil, s.err
	}
	if s.resp != nil {
		return s.resp, nil
	}
	return &tmdb.Response{}, nil
}

func (s *stubSearcher) SearchTVWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	s.tvCalls++
	if s.err != nil {
		return nil, s.err
	}
	if s.tvResp != nil {
		return s.tvResp, nil
	}
	return &tmdb.Response{}, nil
}

func (s *stubSearcher) SearchMultiWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	s.multiCalls++
	if s.err != nil {
		return nil, s.err
	}
	if s.multiResp != nil {
		return s.multiResp, nil
	}
	return &tmdb.Response{}, nil
}

func (s *stubSearcher) GetSeasonDetails(ctx context.Context, showID int64, seasonNumber int) (*tmdb.SeasonDetails, error) {
	s.seasonCalls++
	if s.err != nil {
		return nil, s.err
	}
	if s.season != nil {
		return s.season, nil
	}
	return &tmdb.SeasonDetails{}, nil
}

type stubDiscScanner struct {
	result *disc.ScanResult
	err    error
}

func (s *stubDiscScanner) Scan(ctx context.Context, device string) (*disc.ScanResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

type recordingNotifier struct {
	detected     []struct{ title, discType string }
	identified   []struct{ title, mediaType, year string }
	unidentified []string
}

func (r *recordingNotifier) Publish(ctx context.Context, event notifications.Event, payload notifications.Payload) error {
	switch event {
	case notifications.EventDiscDetected:
		title := ""
		discType := ""
		if payload != nil {
			if v, ok := payload["discTitle"].(string); ok {
				title = v
			}
			if v, ok := payload["discType"].(string); ok {
				discType = v
			}
		}
		r.detected = append(r.detected, struct{ title, discType string }{strings.TrimSpace(title), strings.TrimSpace(discType)})
	case notifications.EventIdentificationCompleted:
		title := ""
		mediaType := ""
		year := ""
		if payload != nil {
			if v, ok := payload["title"].(string); ok {
				title = v
			}
			if v, ok := payload["mediaType"].(string); ok {
				mediaType = v
			}
			if v, ok := payload["year"].(string); ok {
				year = v
			}
		}
		r.identified = append(r.identified, struct{ title, mediaType, year string }{strings.TrimSpace(title), strings.TrimSpace(mediaType), strings.TrimSpace(year)})
	case notifications.EventUnidentifiedMedia:
		label := ""
		if payload != nil {
			if v, ok := payload["filename"].(string); ok {
				label = v
			} else if v, ok := payload["label"].(string); ok {
				label = v
			}
		}
		r.unidentified = append(r.unidentified, strings.TrimSpace(label))
	}
	return nil
}
