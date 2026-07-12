package app

import (
	"context"
	"fmt"

	"github.com/jagadishg/arbor/internal/assertions"
	"github.com/jagadishg/arbor/internal/model"
	httpruntime "github.com/jagadishg/arbor/internal/runtime"
	"github.com/jagadishg/arbor/internal/scenario"
	"github.com/jagadishg/arbor/internal/secrets"
	"github.com/jagadishg/arbor/internal/variables"
	"github.com/jagadishg/arbor/internal/workspace"
)

type App struct {
	Workspace *model.Workspace
	Executor  *httpruntime.Executor
	Secrets   secrets.Provider
}

func Load(path string) (*App, error) {
	ws, err := workspace.Load(path)
	if err != nil {
		return nil, err
	}
	executor, err := httpruntime.New(ws.HTTP)
	if err != nil {
		return nil, err
	}
	return &App{Workspace: ws, Executor: executor, Secrets: secrets.SystemProvider{}}, nil
}

func (a *App) Variables(environmentName string, runtime map[string]string) (*variables.Set, error) {
	if environmentName == "" {
		environmentName = a.Workspace.DefaultEnv
	}
	var environment *model.Environment
	if environmentName != "" {
		found, ok := a.Workspace.EnvironmentByName(environmentName)
		if !ok {
			return nil, fmt.Errorf("environment %q not found", environmentName)
		}
		environment = &found
	}
	return variables.New(a.Workspace, environment, runtime, a.Secrets)
}

func (a *App) RunRequest(ctx context.Context, ref, environment string, runtime map[string]string) model.RequestResult {
	request, ok := a.Workspace.RequestByRef(ref)
	if !ok {
		return model.RequestResult{Error: fmt.Errorf("request %q not found", ref)}
	}
	result := model.RequestResult{Request: request, Extracted: map[string]string{}}
	vars, err := a.Variables(environment, runtime)
	if err != nil {
		result.Error = err
		return result
	}
	result.Response, result.Sent, result.Error = a.Executor.Execute(ctx, request, vars)
	if result.Error == nil {
		result.Assertions = assertions.EvaluateAll(request.Assert, result.Response, vars)
	}
	return result
}

func (a *App) RunScenario(ctx context.Context, ref, environment string, runtime map[string]string) (model.ScenarioReport, error) {
	definition, ok := a.Workspace.ScenarioByRef(ref)
	if !ok {
		return model.ScenarioReport{}, fmt.Errorf("scenario %q not found", ref)
	}
	vars, err := a.Variables(environment, runtime)
	if err != nil {
		return model.ScenarioReport{}, err
	}
	return (&scenario.Runner{Executor: a.Executor}).Run(ctx, a.Workspace, definition, vars), nil
}
