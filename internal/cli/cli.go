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
	"github.com/jagadishg/arbor/internal/config"
	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/secrets"
	"github.com/jagadishg/arbor/internal/variables"
	"github.com/jagadishg/arbor/internal/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Out     io.Writer
	ErrOut  io.Writer
	In      io.Reader
	Dir     string
	RunTUI  func(context.Context, string, string) error
	Resolve func() (string, error)
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
	var workspaceName string
	options.Resolve = func() (string, error) { return resolveDir(options, workspaceName) }
	root := &cobra.Command{
		Use: "arbor", Short: "A terminal-native, local-first API workspace",
		SilenceUsage: true, SilenceErrors: true,
		Version: fmt.Sprintf("%s (%s, %s)", buildinfo.Version, buildinfo.Commit, buildinfo.Date),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.RunTUI == nil {
				return cmd.Help()
			}
			dir, err := resolveTUITarget(options, workspaceName)
			if err != nil {
				return err
			}
			rememberWorkspace(dir)
			return options.RunTUI(cmd.Context(), dir, environment)
		},
	}
	root.SetOut(options.Out)
	root.SetErr(options.ErrOut)
	root.PersistentFlags().StringVarP(&environment, "env", "e", "", "environment to use")
	root.PersistentFlags().StringVarP(&workspaceName, "workspace", "w", "", "registered workspace to target")
	root.AddCommand(initCommand(options), newCommand(options), registerCommand(options), workspacesCommand(options), unregisterCommand(options), configCommand(options), validateCommand(options), listCommand(options), describeCommand(options, &environment), runCommand(options, &environment), scenarioCommand(options, &environment), secretCommand(options, &environment))
	return root
}

func configCommand(options Options) *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Show Arbor's central configuration"}
	command.AddCommand(&cobra.Command{Use: "path", Short: "Print the central config file path", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		path, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Fprintln(options.Out, path)
		return nil
	}})
	return command
}

// resolveDir maps the --workspace flag to a directory: a registered workspace's
// path, or the working directory when no name is given.
func resolveDir(options Options, workspaceName string) (string, error) {
	if workspaceName == "" {
		return options.Dir, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	entry, ok := cfg.Find(workspaceName)
	if !ok {
		return "", fmt.Errorf("workspace %q is not registered (see 'arbor workspaces')", workspaceName)
	}
	return entry.Path, nil
}

// resolveTUITarget decides which workspace the TUI opens: an explicit
// --workspace, else one in/above the working directory, else the last-used one.
func resolveTUITarget(options Options, workspaceName string) (string, error) {
	if workspaceName != "" {
		return resolveDir(options, workspaceName)
	}
	if root, err := workspace.FindRoot(options.Dir); err == nil {
		return root, nil
	}
	if cfg, err := config.Load(); err == nil {
		if cfg.LastWorkspace != "" {
			if _, err := os.Stat(filepath.Join(cfg.LastWorkspace, workspace.ConfigName)); err == nil {
				return cfg.LastWorkspace, nil
			}
		}
		// Fall back to any still-valid registered workspace, so an upgrade that
		// left lastWorkspace unset — or a stale last-used path — still opens the
		// registry instead of erroring out.
		for _, entry := range cfg.Workspaces {
			if _, err := os.Stat(filepath.Join(entry.Path, workspace.ConfigName)); err == nil {
				return entry.Path, nil
			}
		}
	}
	return "", errors.New("no workspace here — run 'arbor init', 'arbor register <dir>', or open one you've used before")
}

// rememberWorkspace registers the opened workspace and records it as last-used.
// It is best-effort: registry problems must not stop the app from opening.
func rememberWorkspace(root string) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	name := ""
	if ws, err := workspace.Load(root); err == nil {
		name = ws.Name
	}
	cfg.Register(root, name)
	cfg.Touch(root)
	_ = cfg.Save()
}

func registerCommand(options Options) *cobra.Command {
	var name string
	command := &cobra.Command{Use: "register [directory]", Short: "Register a workspace in the central registry", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		dir := options.Dir
		if len(args) == 1 {
			dir = args[0]
		}
		root, err := workspace.FindRoot(dir)
		if err != nil {
			return err
		}
		if name == "" {
			if ws, err := workspace.Load(root); err == nil {
				name = ws.Name
			}
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		entry := cfg.Register(root, name)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Registered workspace %q at %s\n", entry.Name, entry.Path)
		return nil
	}}
	command.Flags().StringVarP(&name, "name", "n", "", "workspace name")
	return command
}

func workspacesCommand(options Options) *cobra.Command {
	return &cobra.Command{Use: "workspaces", Aliases: []string{"ws"}, Short: "List registered workspaces", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if len(cfg.Workspaces) == 0 {
			fmt.Fprintln(options.Out, "No workspaces registered. Open one with 'arbor' inside it, or 'arbor register <dir>'.")
			return nil
		}
		for _, entry := range cfg.Workspaces {
			marker := "  "
			if entry.Path == cfg.LastWorkspace {
				marker = "* "
			}
			fmt.Fprintf(options.Out, "%s%-20s  %s\n", marker, entry.Name, entry.Path)
		}
		return nil
	}}
}

func unregisterCommand(options Options) *cobra.Command {
	return &cobra.Command{Use: "unregister <name>", Short: "Remove a workspace from the registry", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if !cfg.Remove(args[0]) {
			return fmt.Errorf("workspace %q is not registered", args[0])
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Unregistered %s\n", args[0])
		return nil
	}}
}

func newCommand(options Options) *cobra.Command {
	command := &cobra.Command{Use: "new", Short: "Create a workspace resource"}
	command.AddCommand(newRequestCommand(options), newCollectionCommand(options), newEnvironmentCommand(options), newScenarioCommand(options))
	return command
}

func newCollectionCommand(options Options) *cobra.Command {
	var description string
	command := &cobra.Command{Use: "collection <name>", Aliases: []string{"col"}, Short: "Create a collection", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		root, err := workspace.FindRoot(options.Dir)
		if err != nil {
			return err
		}
		name := safeName(args[0])
		value := model.Collection{Version: model.SchemaVersion, Kind: "collection", Name: name, Description: description}
		path := filepath.Join(root, "collections", name, "collection.yaml")
		if err := writeResource(path, value); err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "Created collection %s at %s\n", name, path)
		return nil
	}}
	command.Flags().StringVarP(&description, "description", "d", "", "collection description")
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
	collectionDir := filepath.Join(root, "collections", "example")
	if err := os.MkdirAll(collectionDir, 0o755); err != nil {
		return err
	}
	collection := "version: 1\nkind: collection\nname: example\ndescription: Group related requests under collections/<name>/. Delete this once you add your own.\n"
	return os.WriteFile(filepath.Join(collectionDir, "collection.yaml"), []byte(collection), 0o644)
}

func validateCommand(options Options) *cobra.Command {
	return &cobra.Command{Use: "validate", Short: "Validate the current workspace", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		dir, err := options.Resolve()
		if err != nil {
			return err
		}
		loaded, err := app.Load(dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(options.Out, "✓ %s: %d requests, %d environments, %d scenarios\n", loaded.Workspace.Name, len(loaded.Workspace.Requests), len(loaded.Workspace.Environments), len(loaded.Workspace.Scenarios))
		return nil
	}}
}

func listCommand(options Options) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{Use: "list", Short: "List workspace resources"}
	command.PersistentFlags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	command.AddCommand(
		listResourceCommand(options, "requests", &asJSON),
		listResourceCommand(options, "collections", &asJSON),
		listResourceCommand(options, "environments", &asJSON),
		listResourceCommand(options, "scenarios", &asJSON),
	)
	return command
}

func listResourceCommand(options Options, resource string, asJSON *bool) *cobra.Command {
	return &cobra.Command{Use: resource, Short: "List " + resource, Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		dir, err := options.Resolve()
		if err != nil {
			return err
		}
		loaded, err := app.Load(dir)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(options.Out).Encode(listPayload(loaded.Workspace, resource))
		}
		var rows []string
		switch resource {
		case "requests":
			for _, item := range loaded.Workspace.Requests {
				rows = append(rows, fmt.Sprintf("%-8s  %-28s  %s", strings.ToUpper(item.Method), item.Ref(), item.Name))
			}
		case "collections":
			for _, item := range loaded.Workspace.Collections {
				rows = append(rows, fmt.Sprintf("%-20s  %2d requests  %s", item.Name, len(loaded.Workspace.RequestsInCollection(item.Name)), item.Description))
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

func listPayload(ws *model.Workspace, resource string) any {
	switch resource {
	case "requests":
		out := make([]map[string]any, 0, len(ws.Requests))
		for _, item := range ws.Requests {
			out = append(out, map[string]any{"ref": item.Ref(), "name": item.Name, "method": strings.ToUpper(item.Method), "url": item.URL, "collection": item.Collection, "description": item.Description})
		}
		return out
	case "collections":
		out := make([]map[string]any, 0, len(ws.Collections))
		for _, item := range ws.Collections {
			out = append(out, map[string]any{"name": item.Name, "requests": len(ws.RequestsInCollection(item.Name)), "description": item.Description})
		}
		return out
	case "environments":
		out := make([]map[string]any, 0, len(ws.Environments))
		for _, item := range ws.Environments {
			out = append(out, map[string]any{"name": item.Name, "variables": len(item.Variables), "secrets": len(item.Secrets), "description": item.Description})
		}
		return out
	case "scenarios":
		out := make([]map[string]any, 0, len(ws.Scenarios))
		for _, item := range ws.Scenarios {
			out = append(out, map[string]any{"ref": item.Ref(), "name": item.Name, "steps": len(item.Steps), "description": item.Description})
		}
		return out
	}
	return nil
}

func describeCommand(options Options, environment *string) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{Use: "describe <reference>", Short: "Describe a request, collection, scenario, or environment", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		dir, err := options.Resolve()
		if err != nil {
			return err
		}
		loaded, err := app.Load(dir)
		if err != nil {
			return err
		}
		described, err := describeResource(loaded, *environment, args[0])
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(options.Out).Encode(described)
		}
		printDescription(options.Out, described)
		return nil
	}}
	command.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return command
}

// description is the resolved, redacted view of one resource. It is the surface
// an agent can read to understand the workspace without opening every file.
type description struct {
	Kind        string            `json:"kind"`
	Ref         string            `json:"ref,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Collection  string            `json:"collection,omitempty"`
	Method      string            `json:"method,omitempty"`
	URL         string            `json:"url,omitempty"`
	ResolvedURL string            `json:"resolvedUrl,omitempty"`
	File        string            `json:"file,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Query       map[string]string `json:"query,omitempty"`
	Form        map[string]string `json:"form,omitempty"`
	Files       map[string]string `json:"files,omitempty"`
	Assert      []string          `json:"assert,omitempty"`
	Extract     map[string]string `json:"extract,omitempty"`
	Steps       []string          `json:"steps,omitempty"`
	Requests    []string          `json:"requests,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
	Secrets     []string          `json:"secrets,omitempty"`
	Environment string            `json:"environment,omitempty"`
}

func describeResource(loaded *app.App, environment, ref string) (description, error) {
	ws := loaded.Workspace
	if request, ok := ws.RequestByRef(ref); ok {
		out := description{Kind: "request", Ref: request.Ref(), Name: request.Name, Description: request.Description, Collection: firstOr(request.Collection, "default"), Method: strings.ToUpper(request.Method), URL: request.URL, File: relPath(ws.Root, request.Path), Headers: request.Headers, Query: request.Query, Form: request.Form, Files: request.Files, Assert: request.Assert, Extract: request.Extract, Environment: firstOr(environment, ws.DefaultEnv)}
		if vars, err := loaded.Variables(environment, nil); err == nil {
			if resolved, err := vars.Resolve(request.URL); err == nil {
				out.ResolvedURL = vars.Redact(resolved)
			}
			out.Variables = redactedValues(vars)
		}
		return out, nil
	}
	if scenario, ok := ws.ScenarioByRef(ref); ok {
		out := description{Kind: "scenario", Ref: scenario.Ref(), Name: scenario.Name, Description: scenario.Description, File: relPath(ws.Root, scenario.Path)}
		for _, step := range scenario.Steps {
			out.Steps = append(out.Steps, step.Request)
		}
		return out, nil
	}
	if environmentValue, ok := ws.EnvironmentByName(ref); ok {
		out := description{Kind: "environment", Name: environmentValue.Name, Description: environmentValue.Description, File: relPath(ws.Root, environmentValue.Path), Variables: environmentValue.Variables}
		for _, name := range sortedStrings(environmentValue.Secrets) {
			out.Secrets = append(out.Secrets, name)
		}
		return out, nil
	}
	if collection, ok := ws.CollectionByName(ref); ok {
		out := description{Kind: "collection", Name: collection.Name, Description: collection.Description}
		if collection.Path != "" {
			out.File = relPath(ws.Root, collection.Path)
		}
		requests := ws.RequestsInCollection(collection.Name)
		sort.Slice(requests, func(i, j int) bool { return requests[i].Ref() < requests[j].Ref() })
		for _, request := range requests {
			out.Requests = append(out.Requests, request.Ref())
		}
		return out, nil
	}
	return description{}, fmt.Errorf("resource %q not found", ref)
}

func printDescription(out io.Writer, d description) {
	fmt.Fprintf(out, "%s: %s\n", strings.ToUpper(d.Kind[:1])+d.Kind[1:], firstOr(d.Ref, d.Name))
	if d.Ref != "" && d.Name != "" && d.Name != d.Ref {
		fmt.Fprintf(out, "Name: %s\n", d.Name)
	}
	if d.Description != "" {
		fmt.Fprintf(out, "Description: %s\n", d.Description)
	}
	if d.Collection != "" {
		fmt.Fprintf(out, "Collection: %s\n", d.Collection)
	}
	if d.Method != "" {
		fmt.Fprintf(out, "Method: %s\n", d.Method)
	}
	if d.URL != "" {
		fmt.Fprintf(out, "URL: %s\n", d.URL)
	}
	if d.ResolvedURL != "" && d.ResolvedURL != d.URL {
		fmt.Fprintf(out, "Resolved: %s\n", d.ResolvedURL)
	}
	if d.File != "" {
		fmt.Fprintf(out, "File: %s\n", d.File)
	}
	printSection(out, "Headers", mapLines(d.Headers))
	printSection(out, "Query", mapLines(d.Query))
	printSection(out, "Form", mapLines(d.Form))
	printSection(out, "Files", mapLines(d.Files))
	printSection(out, "Assertions", d.Assert)
	printSection(out, "Extract", mapLines(d.Extract))
	printSection(out, "Steps", d.Steps)
	printSection(out, "Requests", d.Requests)
	printSection(out, "Variables", mapLines(d.Variables))
	printSection(out, "Secrets", d.Secrets)
}

func printSection(out io.Writer, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n", title)
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Fprintf(out, "  %s\n", line)
	}
}

func mapLines(values map[string]string) []string {
	lines := make([]string, 0, len(values))
	for key, value := range values {
		lines = append(lines, key+": "+value)
	}
	return lines
}

func redactedValues(vars *variables.Set) map[string]string {
	values := vars.Values()
	for key, value := range values {
		values[key] = vars.Redact(value)
	}
	return values
}

func sortedStrings(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func relPath(root, path string) string {
	if path == "" {
		return ""
	}
	if value, err := filepath.Rel(root, path); err == nil {
		return value
	}
	return path
}

func firstOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func runCommand(options Options, environment *string) *cobra.Command {
	var values []string
	var outputJSON bool
	command := &cobra.Command{Use: "run <request>", Short: "Run a request", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := options.Resolve()
		if err != nil {
			return err
		}
		loaded, err := app.Load(dir)
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
		dir, err := options.Resolve()
		if err != nil {
			return err
		}
		loaded, err := app.Load(dir)
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
