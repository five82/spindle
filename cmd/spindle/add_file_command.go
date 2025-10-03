package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
	"spindle/internal/queue"
)

var manualFileExtensions = map[string]struct{}{
	".mkv": {},
	".mp4": {},
	".avi": {},
}

func newAddFileCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "add-file <path>",
		Short: "Add a video file to the processing queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			absPath, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			info, err := os.Stat(absPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("file does not exist: %s", absPath)
				}
				return fmt.Errorf("inspect file: %w", err)
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory", absPath)
			}

			ext := strings.ToLower(filepath.Ext(info.Name()))
			if _, ok := manualFileExtensions[ext]; !ok {
				return fmt.Errorf("unsupported file extension %q", ext)
			}

			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				if client != nil {
					// Use IPC if daemon is running
					resp, err := client.AddFile(absPath)
					if err != nil {
						return err
					}
					if resp == nil {
						return errors.New("empty response from daemon")
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Queued manual file as item #%d (%s)\n", resp.Item.ID, filepath.Base(absPath))
				} else {
					// Use direct store access
					item, err := store.NewFile(cmd.Context(), absPath)
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Queued manual file as item #%d (%s)\n", item.ID, filepath.Base(absPath))
				}
				return nil
			})
		},
	}
}
