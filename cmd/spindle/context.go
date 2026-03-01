package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/ipc"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/queueaccess"
)

type commandContext struct {
	socketFlag *string
	configFlag *string
	logLevel   *string
	verbose    *bool
	diagnostic *bool
	jsonOutput *bool

	configOnce sync.Once
	config     *config.Config
	configErr  error
}

func newCommandContext(socketFlag, configFlag, logLevel *string, verbose, diagnostic, jsonOutput *bool) *commandContext {
	return &commandContext{
		socketFlag: socketFlag,
		configFlag: configFlag,
		logLevel:   logLevel,
		verbose:    verbose,
		diagnostic: diagnostic,
		jsonOutput: jsonOutput,
	}
}

// JSONMode returns true when the user passed --json.
func (c *commandContext) JSONMode() bool {
	return c != nil && c.jsonOutput != nil && *c.jsonOutput
}

func (c *commandContext) ensureConfig() (*config.Config, error) {
	c.configOnce.Do(func() {
		var path string
		if c.configFlag != nil {
			path = strings.TrimSpace(*c.configFlag)
		}
		cfg, _, _, err := config.Load(path)
		if err != nil {
			c.configErr = err
			return
		}
		if err := cfg.EnsureDirectories(); err != nil {
			c.configErr = err
			return
		}
		c.config = cfg
	})
	return c.config, c.configErr
}

func (c *commandContext) configValue() *config.Config {
	cfg, _ := c.ensureConfig()
	return cfg
}

func (c *commandContext) socketPath() string {
	if c.socketFlag == nil {
		return c.resolveSocketPath()
	}
	if strings.TrimSpace(*c.socketFlag) == "" {
		*c.socketFlag = c.resolveSocketPath()
	}
	return *c.socketFlag
}

func (c *commandContext) resolveSocketPath() string {
	// Use cached config when available to avoid re-parsing.
	if c.config != nil {
		return filepath.Join(c.config.Paths.LogDir, "spindle.sock")
	}
	return defaultSocketPath()
}

func (c *commandContext) resolvedLogLevel(cfg *config.Config) string {
	if c != nil && c.logLevel != nil {
		if trimmed := strings.TrimSpace(*c.logLevel); trimmed != "" {
			return trimmed
		}
	}
	if c != nil && c.verbose != nil && *c.verbose {
		return "debug"
	}
	if cfg != nil {
		if trimmed := strings.TrimSpace(cfg.Logging.Level); trimmed != "" {
			return trimmed
		}
	}
	return "info"
}

func (c *commandContext) logDevelopment(cfg *config.Config) bool {
	level := strings.ToLower(strings.TrimSpace(c.resolvedLogLevel(cfg)))
	return level == "debug"
}

// newCLILogger creates a logger configured for CLI commands. When console is true,
// it uses "console" format (for lightweight helpers). Otherwise it uses the
// config-specified format with stdout output.
func (c *commandContext) newCLILogger(cfg *config.Config, component string, console bool) (*slog.Logger, error) {
	opts := logging.Options{
		Level:       c.resolvedLogLevel(cfg),
		Development: c.logDevelopment(cfg),
	}
	if console {
		opts.Format = "console"
	} else {
		opts.Format = cfg.Logging.Format
		opts.OutputPaths = []string{"stdout"}
	}
	logger, err := logging.New(opts)
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}
	if component != "" {
		logger = logger.With(logging.String("component", component))
	}
	return logger, nil
}

func (c *commandContext) diagnosticMode() bool {
	return c != nil && c.diagnostic != nil && *c.diagnostic
}

func (c *commandContext) withClient(fn func(*ipc.Client) error) error {
	client, err := c.dialClient()
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client)
}

func (c *commandContext) withQueueAPI(fn func(queueaccess.Access) error) error {
	session, err := queueaccess.OpenWithFallback(c.dialClient, func() (*queue.Store, error) {
		cfg, cfgErr := c.ensureConfig()
		if cfgErr != nil {
			return nil, fmt.Errorf("load config for direct store access: %w", cfgErr)
		}
		return queue.Open(cfg)
	})
	if err != nil {
		return err
	}
	defer session.Close()
	return fn(session.Access)
}

func (c *commandContext) dialClient() (*ipc.Client, error) {
	socket := c.socketPath()
	client, err := ipc.Dial(socket)
	if err != nil {
		return nil, wrapDialError(err, socket)
	}
	return client, nil
}

func wrapDialError(err error, socket string) error {
	switch {
	case errors.Is(err, syscall.ENOENT) || os.IsNotExist(err):
		return fmt.Errorf("spindle daemon is not running. Start it with: spindle start")
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("connect to daemon: socket %s refused the connection; verify the daemon is running", socket)
	default:
		return fmt.Errorf("connect to daemon: %w", err)
	}
}

func defaultSocketPath() string {
	cfg, _, _, err := config.Load("")
	if err == nil {
		return filepath.Join(cfg.Paths.LogDir, "spindle.sock")
	}

	logDir, err2 := config.ExpandPath("~/.local/share/spindle/logs")
	if err2 != nil {
		return filepath.Join(os.TempDir(), "spindle.sock")
	}
	return filepath.Join(logDir, "spindle.sock")
}

func shouldSkipConfig(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations != nil && c.Annotations["skipConfigLoad"] == "true" {
			return true
		}
	}
	return false
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
