package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"spindle/internal/config"
)

// NewConfiguredService returns a Plex service that moves files into the library
// and triggers Plex scans when credentials are available.
func NewConfiguredService(cfg *config.Config) Service {
	simple := NewSimpleService(cfg.LibraryDir, cfg.MoviesDir, cfg.TVDir, cfg.MoviesLibrary, cfg.TVLibrary)

	plexURL := strings.TrimRight(strings.TrimSpace(cfg.PlexURL), "/")
	plexToken := strings.TrimSpace(cfg.PlexToken)
	if plexURL == "" || plexToken == "" {
		return simple
	}

	client := &http.Client{Timeout: 10 * time.Second}
	return &httpService{
		simple:        simple,
		plexURL:       plexURL,
		plexToken:     plexToken,
		moviesLibrary: cfg.MoviesLibrary,
		tvLibrary:     cfg.TVLibrary,
		client:        client,
	}
}

type httpService struct {
	simple        *SimpleService
	plexURL       string
	plexToken     string
	moviesLibrary string
	tvLibrary     string
	client        *http.Client

	mu       sync.Mutex
	sections map[string]string
}

func (s *httpService) Organize(ctx context.Context, sourcePath string, meta MediaMetadata) (string, error) {
	return s.simple.Organize(ctx, sourcePath, meta)
}

func (s *httpService) Refresh(ctx context.Context, meta MediaMetadata) error {
	if err := s.simple.Refresh(ctx, meta); err != nil {
		return err
	}

	if s.client == nil {
		return nil
	}

	sections, err := s.ensureSections(ctx)
	if err != nil {
		return err
	}

	var libraryName string
	if meta.IsMovie() {
		libraryName = s.moviesLibrary
	} else {
		libraryName = s.tvLibrary
	}
	key, ok := sections[strings.ToLower(libraryName)]
	if !ok {
		return fmt.Errorf("plex library %q not found", libraryName)
	}

	refreshURL := fmt.Sprintf("%s/library/sections/%s/refresh", s.plexURL, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, refreshURL, nil)
	if err != nil {
		return fmt.Errorf("build plex refresh request: %w", err)
	}
	req.Header.Set("X-Plex-Token", s.plexToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh plex library: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("plex refresh returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *httpService) ensureSections(ctx context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sections != nil {
		return s.sections, nil
	}

	sectionsURL := fmt.Sprintf("%s/library/sections", s.plexURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sectionsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build plex sections request: %w", err)
	}
	req.Header.Set("X-Plex-Token", s.plexToken)
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch plex sections: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("plex sections returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	type directory struct {
		Key   string `xml:"key,attr"`
		Title string `xml:"title,attr"`
	}
	type mediaContainer struct {
		Directories []directory `xml:"Directory"`
	}

	var container mediaContainer
	if err := xml.NewDecoder(resp.Body).Decode(&container); err != nil {
		return nil, fmt.Errorf("decode plex sections: %w", err)
	}

	sections := make(map[string]string, len(container.Directories))
	for _, dir := range container.Directories {
		if dir.Key == "" || dir.Title == "" {
			continue
		}
		sections[strings.ToLower(dir.Title)] = dir.Key
	}
	s.sections = sections
	return sections, nil
}

const userAgent = "Spindle-Go/0.1.0"
