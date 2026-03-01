package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
)

func newQueueHealthCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check queue database health (schema, integrity, columns)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withClient(func(client *ipc.Client) error {
				resp, err := client.DatabaseHealth()
				if err != nil {
					return err
				}
				if ctx.JSONMode() {
					return writeJSON(cmd, resp)
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Database path: %s\n", resp.DBPath)
				fmt.Fprintf(out, "Database exists: %s\n", yesNo(resp.DatabaseExists))
				fmt.Fprintf(out, "Readable: %s\n", yesNo(resp.DatabaseReadable))
				fmt.Fprintf(out, "Schema version: %s\n", resp.SchemaVersion)
				fmt.Fprintf(out, "queue_items table present: %s\n", yesNo(resp.TableExists))
				if len(resp.ColumnsPresent) > 0 {
					cols := append([]string(nil), resp.ColumnsPresent...)
					sort.Strings(cols)
					fmt.Fprintf(out, "Columns: %s\n", strings.Join(cols, ", "))
				}
				if len(resp.MissingColumns) > 0 {
					missing := append([]string(nil), resp.MissingColumns...)
					sort.Strings(missing)
					fmt.Fprintf(out, "Missing columns: %s\n", strings.Join(missing, ", "))
				} else {
					fmt.Fprintln(out, "Missing columns: none")
				}
				fmt.Fprintf(out, "Integrity check: %s\n", yesNo(resp.IntegrityCheck))
				fmt.Fprintf(out, "Total items: %d\n", resp.TotalItems)
				if resp.Error != "" {
					fmt.Fprintf(out, "Error: %s\n", resp.Error)
				}
				return nil
			})
		},
	}
}
