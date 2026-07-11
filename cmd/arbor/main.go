package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jagadishg/arbor/internal/cli"
)

func main() {
	command := cli.New(cli.Options{RunTUI: func(context.Context, string, string) error {
		return fmt.Errorf("interactive mode is not available in this build yet; use arbor --help")
	}})
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "arbor:", err)
		os.Exit(1)
	}
}
