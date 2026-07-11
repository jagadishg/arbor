package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jagadishg/arbor/internal/app"
	"github.com/jagadishg/arbor/internal/buildinfo"
	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/workspace"
	"github.com/spf13/cobra"
)

type Options struct {
	Out    io.Writer
	ErrOut io.Writer
	Dir    string
	RunTUI func(context.Context, string, string) error
}

func New(options Options) *cobra.Command {
	if options.Out == nil {
		options.Out = os.Stdout
	}
	if options.ErrOut == nil {
		options.ErrOut = os.Stderr
	}
	if options.Dir == "" {
		options.Dir = "."
	}
	var environment string
	root := &cobra.Command{
		Use: "arbor", Short: "A terminal-native, local-first API workspace",
		SilenceUsage: true, SilenceErrors: true,
		Version: fmt.Sprintf("%s (%s, %s)", buildinfo.Version, buildinfo.Commit, buildinfo.Date),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.RunTUI == nil {
				return cmd.Help()
			}
			return options.RunTUI(cmd.Context(), options.Dir, environment)
		},
	}
	root.SetOut(options.Out)
	root.SetErr(options.ErrOut)
	root.PersistentFlags().StringVarP(&environment, "env", "e", "", "environment to use")
	root.AddCommand(initCommand(options), validateCommand(options), listCommand(options), runCommand(options, &environment), scenarioCommand(options, &environment))
	return root
}

func initCommand(options Options) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use: "init [directory]", Short: "Create a new Arbor workspace", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			directory := options.Dir
			if len(args) == 1 {
				directory = args[0]
			}
			if name == "" {
				name = filepath.Base(directory)
			}
			if err := initialize(directory, name); err != nil {
				return err
			}
			fmt.Fprintf(options.Out, "Created Arbor workspace %q in %s\n", name, directory)
			return nil
		},
	}
	command.Flags().StringVarP(&name, "name", "n", "", "workspace name")
	return command
}

func initialize(directory, name string) error {
	root, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	config := filepath.Join(root, workspace.ConfigName)
	if _, err := os.Stat(config); err == nil {
		return fmt.Errorf("%s already exists", config)
	}
	for _, dir := range []string{"collections", "environments", "scenarios"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return err
		}
	}
	contents := fmt.Sprintf("version: 1\nname: %s\ndefaultEnvironment: local\n\nhttp:\n  timeout: 30s\n", name)
	if err := os.WriteFile(config, []byte(contents), 0o644); err != nil {
		return err
	}
	environment := "version: 1\nkind: environment\nname: local\n\nvariables:\n  base_url: http://localhost:8080\n"
	if err := os.WriteFile(filepath.Join(root, "environments", "local.yaml"), []byte(environment), 0o644); err != nil {
		return err
	}
	return nil
}

func validateCommand(options Options) *cobra.Command {
	return &cobra.Command{Use: "validate", Short: "Validate the current workspace", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		loaded, err := app.Load(options.Dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "✓ %s: %d requests, %d environments, %d scenarios\n", loaded.Workspace.Name, len(loaded.Workspace.Requests), len(loaded.Workspace.Environments), len(loaded.Workspace.Scenarios))
		return nil
	}}
}

func listCommand(options Options) *cobra.Command {
	command := &cobra.Command{Use: "list", Short: "List workspace resources"}
	command.AddCommand(listResourceCommand(options, "requests"), listResourceCommand(options, "environments"), listResourceCommand(options, "scenarios"))
	return command
}

func listResourceCommand(options Options, resource string) *cobra.Command {
	return &cobra.Command{Use: resource, Short: "List " + resource, Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		loaded, err := app.Load(options.Dir)
		if err != nil {
			return err
		}
		var rows []string
		switch resource {
		case "requests":
			for _, item := range loaded.Workspace.Requests {
				rows = append(rows, fmt.Sprintf("%-8s  %-28s  %s", strings.ToUpper(item.Method), item.Ref(), item.Name))
			}
		case "environments":
			for _, item := range loaded.Workspace.Environments {
				rows = append(rows, item.Name)
			}
		case "scenarios":
			for _, item := range loaded.Workspace.Scenarios {
				rows = append(rows, fmt.Sprintf("%-28s  %s (%d steps)", item.Ref(), item.Name, len(item.Steps)))
			}
		}
		sort.Strings(rows)
		for _, row := range rows {
			fmt.Fprintln(options.Out, row)
		}
		return nil
	}}
}

func runCommand(options Options, environment *string) *cobra.Command {
	var values []string
	var outputJSON bool
	command := &cobra.Command{Use: "run <request>", Short: "Run a request", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		loaded, err := app.Load(options.Dir)
		if err != nil {
			return err
		}
		result := loaded.RunRequest(cmd.Context(), args[0], *environment, parseVariables(values))
		if err := printRequestResult(options.Out, result, outputJSON); err != nil {
			return err
		}
		if !result.Passed() {
			return errors.New("request failed")
		}
		return nil
	}}
	command.Flags().StringArrayVarP(&values, "var", "v", nil, "runtime variable (key=value)")
	command.Flags().BoolVar(&outputJSON, "json", false, "print a machine-readable result")
	return command
}

func scenarioCommand(options Options, environment *string) *cobra.Command {
	var values []string
	command := &cobra.Command{Use: "scenario <scenario>", Short: "Run a scenario", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		loaded, err := app.Load(options.Dir)
		if err != nil {
			return err
		}
		report, err := loaded.RunScenario(cmd.Context(), args[0], *environment, parseVariables(values))
		if err != nil {
			return err
		}
		for index, step := range report.Steps {
			status := "FAIL"
			if step.Passed() {
				status = "PASS"
			}
			code := 0
			if step.Response != nil {
				code = step.Response.StatusCode
			}
			fmt.Fprintf(options.Out, "%s  %d. %-28s  %d\n", status, index+1, step.Request.Ref(), code)
			if step.Error != nil {
				fmt.Fprintf(options.Out, "      %s\n", step.Error)
			}
			for _, assertion := range step.Assertions {
				if !assertion.Passed {
					fmt.Fprintf(options.Out, "      %s: %s\n", assertion.Expression, assertion.Message)
				}
			}
		}
		fmt.Fprintf(options.Out, "\n%d steps in %s\n", len(report.Steps), report.Duration.Round(1e6))
		if !report.Passed() {
			return errors.New("scenario failed")
		}
		return nil
	}}
	command.Flags().StringArrayVarP(&values, "var", "v", nil, "runtime variable (key=value)")
	return command
}

func parseVariables(values []string) map[string]string {
	result := map[string]string{}
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if ok && key != "" {
			result[key] = item
		}
	}
	return result
}

func printRequestResult(out io.Writer, result model.RequestResult, asJSON bool) error {
	if asJSON {
		payload := map[string]any{"passed": result.Passed(), "request": result.Request.Ref(), "assertions": result.Assertions}
		if result.Response != nil {
			payload["status"] = result.Response.StatusCode
			payload["durationMs"] = float64(result.Response.Duration.Microseconds()) / 1000
			payload["body"] = json.RawMessage(result.Response.Body)
		}
		if result.Error != nil {
			payload["error"] = result.Error.Error()
		}
		return json.NewEncoder(out).Encode(payload)
	}
	if result.Error != nil {
		fmt.Fprintf(out, "ERROR  %s\n", result.Error)
		return nil
	}
	fmt.Fprintf(out, "%d %s  %s  %d B\n", result.Response.StatusCode, result.Response.Status, result.Response.Duration.Round(1e6), result.Response.Size)
	for _, assertion := range result.Assertions {
		mark := "✓"
		if !assertion.Passed {
			mark = "✗"
		}
		fmt.Fprintf(out, "%s %s", mark, assertion.Expression)
		if assertion.Message != "" {
			fmt.Fprintf(out, ": %s", assertion.Message)
		}
		fmt.Fprintln(out)
	}
	if len(result.Response.Body) > 0 {
		var pretty bytes.Buffer
		if json.Indent(&pretty, result.Response.Body, "", "  ") == nil {
			_, _ = pretty.WriteTo(out)
			fmt.Fprintln(out)
		} else {
			fmt.Fprintln(out, string(result.Response.Body))
		}
	}
	return nil
}
