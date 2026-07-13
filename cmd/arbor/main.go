package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jagadishg/arbor/internal/cli"
	"github.com/jagadishg/arbor/internal/tui"
	"golang.org/x/term"
)

func main() {
	command := cli.New(cli.Options{UpdateCheck: interactive(), RunTUI: func(ctx context.Context, directory, environment string) error {
		return tui.Run(ctx, directory, environment)
	}})
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "arbor:", err)
		os.Exit(1)
	}
}

func interactive() bool {
	if os.Getenv("CI") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
