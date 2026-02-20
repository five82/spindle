package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/ipc"
	"spindle/internal/queue"
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
		return defaultSocketPath()
	}
	if strings.TrimSpace(*c.socketFlag) == "" {
		*c.socketFlag = defaultSocketPath()
	}
	return *c.socketFlag
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

// withStore attempts to use IPC if available, falls back to direct store access
func (c *commandContext) withStore(fn func(*ipc.Client, *queue.Store) error) error {
	// Try IPC first
	client, err := c.dialClient()
	if err == nil {
		defer client.Close()
		return fn(client, nil)
	}

	// If daemon is not running, use direct store access
	cfg, err := c.ensureConfig()
	if err != nil {
		return fmt.Errorf("load config for direct store access: %w", err)
	}

	store, err := queue.Open(cfg)
	if err != nil {
		return fmt.Errorf("open queue store: %w", err)
	}
	defer store.Close()

	return fn(nil, store)
}

func (c *commandContext) withQueueAPI(fn func(queueAPI) error) error {
	return c.withStore(func(client *ipc.Client, store *queue.Store) error {
		var api queueAPI
		if client != nil {
			api = &queueIPCFacade{client: client}
		} else {
			api = &queueStoreFacade{store: store}
		}
		return fn(api)
	})
}

// withQueueStore provides direct store access (bypassing IPC).
// Use this for operations that require direct database manipulation.
func (c *commandContext) withQueueStore(fn func(queueStoreAPI) error) error {
	cfg, err := c.ensureConfig()
	if err != nil {
		return fmt.Errorf("load config for direct store access: %w", err)
	}

	store, err := queue.Open(cfg)
	if err != nil {
		return fmt.Errorf("open queue store: %w", err)
	}
	defer store.Close()

	return fn(&queueStoreFacade{store: store})
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
