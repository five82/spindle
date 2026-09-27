package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/five82/flyer/internal/app"
)

func main() {
	os.Exit(run())
}

// flagOrEnv returns the flag value if set, otherwise the environment variable.
func flagOrEnv(flagValue, envKey string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv(envKey)
}

func run() int {
	configPath := flag.String("config", "", "override spindle config path (optional)")
	pollSeconds := flag.Int("poll", 0, "refresh interval in seconds (optional, defaults to 2s)")
	apiEndpoint := flag.String("api", "", "Spindle API endpoint URL (e.g., http://server:7487)")
	apiToken := flag.String("token", "", "API bearer token for authentication")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	opts := app.Options{
		ConfigPath:  *configPath,
		APIEndpoint: flagOrEnv(*apiEndpoint, "FLYER_API_ENDPOINT"),
		APIToken:    flagOrEnv(*apiToken, "FLYER_API_TOKEN"),
	}
	if poll := *pollSeconds; poll > 0 {
		opts.PollEvery = poll
	}

	if err := app.Run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "flyer: %v\n", err)
		return 1
	}
	return 0
}
