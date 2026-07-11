package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jagadishg/arbor/internal/cli"
	"github.com/jagadishg/arbor/internal/tui"
)

func main() {
	command := cli.New(cli.Options{RunTUI: func(ctx context.Context, directory, environment string) error {
		return tui.Run(ctx, directory, environment)
	}})
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "arbor:", err)
		os.Exit(1)
	}
}
