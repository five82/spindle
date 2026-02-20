package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// writeJSON encodes v as indented JSON to the command's stdout.
func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
