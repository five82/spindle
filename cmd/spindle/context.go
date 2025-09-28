package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/ipc"
)

type commandContext struct {
	socketFlag *string
	configFlag *string

	configOnce sync.Once
	config     *config.Config
	configErr  error
}

func newCommandContext(socketFlag, configFlag *string) *commandContext {
	return &commandContext{
		socketFlag: socketFlag,
		configFlag: configFlag,
	}
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

func (c *commandContext) withClient(fn func(*ipc.Client) error) error {
	client, err := ipc.Dial(c.socketPath())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer client.Close()
	return fn(client)
}

func defaultSocketPath() string {
	cfg, _, _, err := config.Load("")
	if err == nil {
		return filepath.Join(cfg.LogDir, "spindle.sock")
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
