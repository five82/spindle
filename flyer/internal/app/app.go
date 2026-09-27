package app

import (
	"context"
	"fmt"
	"time"

	"github.com/five82/flyer/internal/config"
	"github.com/five82/flyer/internal/prefs"
	"github.com/five82/flyer/internal/spindle"
	"github.com/five82/flyer/internal/state"
	"github.com/five82/flyer/internal/ui"
)

// Options configure the Flyer application.
type Options struct {
	ConfigPath  string
	PrefsPath   string // empty uses default ~/.config/flyer/prefs.toml
	PollEvery   int    // seconds; zero uses default
	APIEndpoint string // override Spindle API endpoint (e.g., http://server:7487)
	APIToken    string // bearer token for API authentication
}

// Run boots the Flyer TUI until the context is cancelled.
func Run(ctx context.Context, opts Options) error {
	return run(ctx, opts, ui.Run)
}

func run(ctx context.Context, opts Options, startUI func(ui.Options) error) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("load spindle config: %w", err)
	}

	userPrefs := prefs.Load(opts.PrefsPath)

	// Explicit CLI/environment values win over local Spindle config.
	apiEndpoint := opts.APIEndpoint
	if apiEndpoint == "" {
		apiEndpoint = cfg.APIBind
	}
	apiToken := opts.APIToken
	if apiToken == "" {
		apiToken = cfg.APIToken
	}

	var clientOpts []spindle.ClientOption
	if apiToken != "" {
		clientOpts = append(clientOpts, spindle.WithToken(apiToken))
	}

	client, err := spindle.NewClient(apiEndpoint, clientOpts...)
	if err != nil {
		return fmt.Errorf("init spindle client: %w", err)
	}

	store := &state.Store{}

	interval := defaultPollInterval
	if opts.PollEvery > 0 {
		interval = time.Duration(opts.PollEvery) * time.Second
	}

	// Start background poller
	StartPoller(ctx, store, client, interval)

	// Do initial refresh to populate store before UI starts
	_ = refresh(ctx, store, client)

	uiOpts := ui.Options{
		Context:   ctx,
		Client:    client,
		Store:     store,
		Config:    &cfg,
		PollTick:  interval,
		ThemeName: userPrefs.Theme,
		PrefsPath: opts.PrefsPath,
		Refresh:   func() error { return refresh(ctx, store, client) },
	}
	return startUI(uiOpts)
}
