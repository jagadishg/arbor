package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jagadishg/arbor/internal/model"
	"gopkg.in/yaml.v3"
)

const ConfigName = "arbor.yaml"

type ValidationError struct {
	Path    string
	Message string
}

func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

func FindRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ConfigName)); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return "", fmt.Errorf("no %s found from %s", ConfigName, start)
}

func Load(start string) (*model.Workspace, error) {
	root, err := FindRoot(start)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, ConfigName)
	var ws model.Workspace
	if err := decodeStrict(path, &ws); err != nil {
		return nil, err
	}
	ws.Path, ws.Root = path, root

	if err := loadKind(filepath.Join(root, "collections"), "request", func(path string) error {
		var value model.Request
		if err := decodeStrict(path, &value); err != nil {
			return err
		}
		value.Path = path
		ws.Requests = append(ws.Requests, value)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := loadKind(filepath.Join(root, "environments"), "environment", func(path string) error {
		var value model.Environment
		if err := decodeStrict(path, &value); err != nil {
			return err
		}
		value.Path = path
		ws.Environments = append(ws.Environments, value)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := loadKind(filepath.Join(root, "scenarios"), "scenario", func(path string) error {
		var value model.Scenario
		if err := decodeStrict(path, &value); err != nil {
			return err
		}
		value.Path = path
		ws.Scenarios = append(ws.Scenarios, value)
		return nil
	}); err != nil {
		return nil, err
	}

	if errs := Validate(&ws); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &ws, nil
}

func decodeStrict(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return ValidationError{Path: path, Message: err.Error()}
	}
	return nil
}

func loadKind(root, expected string, load func(string) error) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		var header struct {
			Kind string `yaml:"kind"`
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(data, &header); err != nil {
			return ValidationError{Path: path, Message: err.Error()}
		}
		if header.Kind != expected {
			return ValidationError{Path: path, Message: fmt.Sprintf("expected kind %q, got %q", expected, header.Kind)}
		}
		return load(path)
	})
}

func Validate(ws *model.Workspace) []error {
	var errs []error
	if ws.Version != model.SchemaVersion {
		errs = append(errs, ValidationError{Path: ws.Path, Message: fmt.Sprintf("unsupported version %d; expected %d", ws.Version, model.SchemaVersion)})
	}
	if strings.TrimSpace(ws.Name) == "" {
		errs = append(errs, ValidationError{Path: ws.Path, Message: "name is required"})
	}
	seenRequests := map[string]string{}
	for _, request := range ws.Requests {
		if request.Version != model.SchemaVersion {
			errs = append(errs, ValidationError{Path: request.Path, Message: "unsupported request version"})
		}
		if request.Name == "" || request.Method == "" || request.URL == "" {
			errs = append(errs, ValidationError{Path: request.Path, Message: "name, method, and url are required"})
		}
		ref := request.Ref()
		if previous, ok := seenRequests[ref]; ok {
			errs = append(errs, ValidationError{Path: request.Path, Message: fmt.Sprintf("duplicate request reference %q (also in %s)", ref, previous)})
		}
		seenRequests[ref] = request.Path
	}
	seenEnvs := map[string]string{}
	for _, env := range ws.Environments {
		if env.Version != model.SchemaVersion || env.Name == "" {
			errs = append(errs, ValidationError{Path: env.Path, Message: "version 1 and name are required"})
		}
		if previous, ok := seenEnvs[env.Name]; ok {
			errs = append(errs, ValidationError{Path: env.Path, Message: fmt.Sprintf("duplicate environment %q (also in %s)", env.Name, previous)})
		}
		seenEnvs[env.Name] = env.Path
		for name, reference := range env.Secrets {
			if !strings.HasPrefix(reference, "env://") && !strings.HasPrefix(reference, "keychain://") {
				errs = append(errs, ValidationError{Path: env.Path, Message: fmt.Sprintf("secret %q must use env:// or keychain://", name)})
			}
		}
	}
	if ws.DefaultEnv != "" {
		if _, ok := seenEnvs[ws.DefaultEnv]; !ok {
			errs = append(errs, ValidationError{Path: ws.Path, Message: fmt.Sprintf("default environment %q does not exist", ws.DefaultEnv)})
		}
	}
	seenScenarios := map[string]string{}
	for _, scenario := range ws.Scenarios {
		if scenario.Version != model.SchemaVersion || scenario.Name == "" || len(scenario.Steps) == 0 {
			errs = append(errs, ValidationError{Path: scenario.Path, Message: "version 1, name, and at least one step are required"})
		}
		ref := scenario.Ref()
		if previous, ok := seenScenarios[ref]; ok {
			errs = append(errs, ValidationError{Path: scenario.Path, Message: fmt.Sprintf("duplicate scenario reference %q (also in %s)", ref, previous)})
		}
		seenScenarios[ref] = scenario.Path
		for index, step := range scenario.Steps {
			if _, ok := seenRequests[step.Request]; !ok {
				errs = append(errs, ValidationError{Path: scenario.Path, Message: fmt.Sprintf("step %d references unknown request %q", index+1, step.Request)})
			}
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}
