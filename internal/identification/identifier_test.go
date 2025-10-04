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
}

func TestIdentifierFallsBackToQueueFingerprint(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	item, err := store.NewDisc(context.Background(), "Fallback Disc", "fp-fallback")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

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
	}
	if err := json.Unmarshal([]byte(item.RipSpecData), &spec); err != nil {
		t.Fatalf("Unmarshal rip spec: %v", err)
	}
	if spec.Fingerprint != item.DiscFingerprint {
		t.Fatalf("expected fallback fingerprint %q, got %q", item.DiscFingerprint, spec.Fingerprint)
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
	cfg.TMDBAPIKey = ""
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
	resp *tmdb.Response
	err  error
}

func (s *stubSearcher) SearchMovie(ctx context.Context, query string) (*tmdb.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func (s *stubSearcher) SearchMovieWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	return s.SearchMovie(ctx, query)
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
