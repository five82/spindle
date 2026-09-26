package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandRegistrationAndArgumentGuards(t *testing.T) {
	for _, tc := range []struct {
		name     string
		build    func() *cobra.Command
		children []string
	}{
		{"queue", newQueueCmd, []string{"list", "show", "clear", "retry", "cancel", "audit"}},
		{"daemon", newDaemonCmd, nil},
		{"cache", newCacheCmd, []string{"rip", "list", "process", "remove", "clear"}},
		{"identify", newIdentifyCmd, nil},
		{"discid", newDiscIDCmd, nil},
		{"start", newStartCmd, nil},
		{"restart", newRestartCmd, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.build()
			if cmd.Name() != tc.name {
				t.Fatalf("command name = %q", cmd.Name())
			}
			for _, child := range tc.children {
				if c, _, err := cmd.Find([]string{child}); err != nil || c.Name() != child {
					t.Fatalf("missing %s: %v", child, err)
				}
			}
			if cmd.RunE != nil && tc.name == "identify" {
				if err := cmd.Args(cmd, []string{"one", "two"}); err == nil {
					t.Fatal("identify accepted two devices")
				}
			}
		})
	}
}
