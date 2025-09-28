package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

func main() {
	cmd := newRootCommand()
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
