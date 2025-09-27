package identification_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/queue"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default()
	cfg.TMDBAPIKey = "test"
	cfg.StagingDir = filepath.Join(base, "staging")
	cfg.LibraryDir = filepath.Join(base, "library")
	cfg.LogDir = filepath.Join(base, "logs")
	cfg.ReviewDir = filepath.Join(base, "review")
	return &cfg
}

func TestIdentifierTransitionsToIdentified(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	item, err := store.NewDisc(context.Background(), "Demo Disc", "fp-demo")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	stubTMDB := &stubSearcher{resp: &tmdb.Response{Results: []tmdb.Result{{ID: 1, Title: "Demo Disc", VoteAverage: 8.5, VoteCount: 200}}, TotalResults: 1}}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-demo", Titles: []disc.Title{{ID: 1, Name: "Demo Disc", Duration: 7000}}}}
	notifier := &recordingNotifier{}
	handler := identification.NewIdentifierWithDependencies(cfg, store, zap.NewNop(), stubTMDB, stubScanner, notifier)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = handler.NextStatus()
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
}

func TestIdentifierMarksDuplicateForReview(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

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
	handler := identification.NewIdentifierWithDependencies(cfg, store, zap.NewNop(), stubTMDB, stubScanner, notifier)
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
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	item, err := store.NewDisc(context.Background(), "Unknown", "fp-unknown")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	stubTMDB := &stubSearcher{resp: &tmdb.Response{Results: []tmdb.Result{}, TotalResults: 0}}
	stubScanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-unknown"}}
	notifier := &recordingNotifier{}
	handler := identification.NewIdentifierWithDependencies(cfg, store, zap.NewNop(), stubTMDB, stubScanner, notifier)
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
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	handler := identification.NewIdentifierWithDependencies(cfg, store, zap.NewNop(), &stubSearcher{}, &stubDiscScanner{}, nil)
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected health ready, got %+v", health)
	}
	if health.Detail != "" {
		t.Fatalf("expected empty detail, got %q", health.Detail)
	}
}

func TestIdentifierHealthMissingAPIKey(t *testing.T) {
	cfg := testConfig(t)
	cfg.TMDBAPIKey = ""
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	handler := identification.NewIdentifierWithDependencies(cfg, store, zap.NewNop(), &stubSearcher{}, &stubDiscScanner{}, nil)
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
	identified   []struct{ title, mediaType string }
	unidentified []string
}

func (r *recordingNotifier) NotifyDiscDetected(ctx context.Context, discTitle, discType string) error {
	r.detected = append(r.detected, struct{ title, discType string }{strings.TrimSpace(discTitle), strings.TrimSpace(discType)})
	return nil
}

func (r *recordingNotifier) NotifyIdentificationComplete(ctx context.Context, title, mediaType string) error {
	r.identified = append(r.identified, struct{ title, mediaType string }{strings.TrimSpace(title), strings.TrimSpace(mediaType)})
	return nil
}

func (r *recordingNotifier) NotifyRipStarted(context.Context, string) error          { return nil }
func (r *recordingNotifier) NotifyRipCompleted(context.Context, string) error        { return nil }
func (r *recordingNotifier) NotifyEncodingCompleted(context.Context, string) error   { return nil }
func (r *recordingNotifier) NotifyProcessingCompleted(context.Context, string) error { return nil }
func (r *recordingNotifier) NotifyOrganizationCompleted(context.Context, string, string) error {
	return nil
}
func (r *recordingNotifier) NotifyQueueStarted(context.Context, int) error { return nil }
func (r *recordingNotifier) NotifyQueueCompleted(context.Context, int, int, time.Duration) error {
	return nil
}
func (r *recordingNotifier) NotifyError(context.Context, error, string) error { return nil }

func (r *recordingNotifier) NotifyUnidentifiedMedia(ctx context.Context, filename string) error {
	r.unidentified = append(r.unidentified, strings.TrimSpace(filename))
	return nil
}

func (r *recordingNotifier) TestNotification(context.Context) error { return nil }
