package cli

import (
	"bufio"
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
	"github.com/jagadishg/arbor/internal/secrets"
	"github.com/jagadishg/arbor/internal/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Out    io.Writer
	ErrOut io.Writer
	In     io.Reader
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
	if options.In == nil {
		options.In = os.Stdin
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
	root.AddCommand(initCommand(options), newCommand(options), validateCommand(options), listCommand(options), runCommand(options, &environment), scenarioCommand(options, &environment), secretCommand(options, &environment))
	return root
}

func newCommand(options Options) *cobra.Command {
	command := &cobra.Command{Use: "new", Short: "Create a workspace resource"}
	command.AddCommand(newRequestCommand(options), newEnvironmentCommand(options), newScenarioCommand(options))
	return command
}

func newRequestCommand(options Options) *cobra.Command {
	var method, rawURL, name string
	command := &cobra.Command{Use: "request <reference>", Short: "Create a request", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		root, err := workspace.FindRoot(options.Dir)
		if err != nil {
			return err
		}
		if name == "" {
			name = displayName(args[0])
		}
		value := model.Request{Version: model.SchemaVersion, Kind: "request", ID: args[0], Name: name, Method: strings.ToUpper(method), URL: rawURL}
		path := filepath.Join(root, "collections", refPath(args[0])+".yaml")
		if err := writeResource(path, value); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Created request %s at %s\n", args[0], path)
		return nil
	}}
	command.Flags().StringVarP(&method, "method", "m", "GET", "HTTP method")
	command.Flags().StringVarP(&rawURL, "url", "u", "{{base_url}}/", "request URL")
	command.Flags().StringVarP(&name, "name", "n", "", "display name")
	return command
}

func newEnvironmentCommand(options Options) *cobra.Command {
	return &cobra.Command{Use: "environment <name>", Aliases: []string{"env"}, Short: "Create an environment", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		root, err := workspace.FindRoot(options.Dir)
		if err != nil {
			return err
		}
		value := model.Environment{Version: model.SchemaVersion, Kind: "environment", Name: args[0], Variables: map[string]string{"base_url": "http://localhost:8080"}}
		path := filepath.Join(root, "environments", safeName(args[0])+".yaml")
		if err := writeResource(path, value); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Created environment %s at %s\n", args[0], path)
		return nil
	}}
}

func newScenarioCommand(options Options) *cobra.Command {
	var request string
	command := &cobra.Command{Use: "scenario <reference>", Short: "Create a scenario", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		root, err := workspace.FindRoot(options.Dir)
		if err != nil {
			return err
		}
		if request == "" {
			return errors.New("--request is required for the first step")
		}
		value := model.Scenario{Version: model.SchemaVersion, Kind: "scenario", ID: args[0], Name: displayName(args[0]), Steps: []model.ScenarioStep{{Request: request}}}
		path := filepath.Join(root, "scenarios", refPath(args[0])+".yaml")
		if err := writeResource(path, value); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Created scenario %s at %s\n", args[0], path)
		return nil
	}}
	command.Flags().StringVarP(&request, "request", "r", "", "request reference for the first step")
	return command
}

func writeResource(path string, value any) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func refPath(ref string) string {
	parts := strings.FieldsFunc(ref, func(r rune) bool { return r == '.' || r == '/' || r == '\\' })
	for index, part := range parts {
		parts[index] = safeName(part)
	}
	return filepath.Join(parts...)
}

func safeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}

func displayName(ref string) string {
	parts := strings.FieldsFunc(ref, func(r rune) bool { return r == '.' || r == '/' || r == '-' || r == '_' })
	if len(parts) == 0 {
		return ref
	}
	last := parts[len(parts)-1]
	if last == "" {
		return ref
	}
	return strings.ToUpper(last[:1]) + last[1:]
}

func secretCommand(options Options, environment *string) *cobra.Command {
	command := &cobra.Command{Use: "secret", Short: "Manage keychain-backed secrets"}
	command.AddCommand(secretSetCommand(options, environment), secretDeleteCommand(options, environment))
	return command
}

func secretSetCommand(options Options, environment *string) *cobra.Command {
	return &cobra.Command{Use: "set <name>", Short: "Store an environment secret in the OS keychain", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		reference, err := secretReference(options.Dir, *environment, args[0])
		if err != nil {
			return err
		}
		value, err := readSecret(options)
		if err != nil {
			return err
		}
		if value == "" {
			return errors.New("secret value cannot be empty")
		}
		if err := (secrets.SystemProvider{}).Store(reference, value); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Stored %s in the OS keychain\n", args[0])
		return nil
	}}
}

func secretDeleteCommand(options Options, environment *string) *cobra.Command {
	return &cobra.Command{Use: "delete <name>", Short: "Delete an environment secret from the OS keychain", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		reference, err := secretReference(options.Dir, *environment, args[0])
		if err != nil {
			return err
		}
		if err := (secrets.SystemProvider{}).Delete(reference); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Deleted %s from the OS keychain\n", args[0])
		return nil
	}}
}

func secretReference(directory, environmentName, name string) (string, error) {
	loaded, err := app.Load(directory)
	if err != nil {
		return "", err
	}
	if environmentName == "" {
		environmentName = loaded.Workspace.DefaultEnv
	}
	environment, ok := loaded.Workspace.EnvironmentByName(environmentName)
	if !ok {
		return "", fmt.Errorf("environment %q not found", environmentName)
	}
	reference, ok := environment.Secrets[name]
	if !ok {
		return "", fmt.Errorf("secret %q is not declared in environment %q", name, environmentName)
	}
	if !strings.HasPrefix(reference, "keychain://") {
		return "", fmt.Errorf("secret %q uses %s; only keychain:// secrets can be managed", name, reference)
	}
	return reference, nil
}

func readSecret(options Options) (string, error) {
	if file, ok := options.In.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		fmt.Fprint(options.ErrOut, "Secret value: ")
		value, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(options.ErrOut)
		return string(value), err
	}
	value, err := bufio.NewReader(options.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(value, "\r\n"), nil
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
		runtimeValues, err := parseVariables(values)
		if err != nil {
			return err
		}
		result := loaded.RunRequest(cmd.Context(), args[0], *environment, runtimeValues)
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
		runtimeValues, err := parseVariables(values)
		if err != nil {
			return err
		}
		report, err := loaded.RunScenario(cmd.Context(), args[0], *environment, runtimeValues)
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

func parseVariables(values []string) (map[string]string, error) {
	result := map[string]string{}
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid variable %q; expected key=value", value)
		}
		result[key] = item
	}
	return result, nil
}

func printRequestResult(out io.Writer, result model.RequestResult, asJSON bool) error {
	if asJSON {
		payload := map[string]any{"passed": result.Passed(), "request": result.Request.Ref(), "assertions": result.Assertions}
		if result.Response != nil {
			payload["status"] = result.Response.StatusCode
			payload["durationMs"] = float64(result.Response.Duration.Microseconds()) / 1000
			var body any
			if json.Unmarshal(result.Response.Body, &body) != nil {
				body = string(result.Response.Body)
			}
			payload["body"] = body
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
