package scenario

import (
	"context"
	"fmt"
	"time"

	"github.com/jagadishg/arbor/internal/assertions"
	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/responsevalue"
	httpruntime "github.com/jagadishg/arbor/internal/runtime"
	"github.com/jagadishg/arbor/internal/variables"
)

type Runner struct {
	Executor *httpruntime.Executor
}

func (r *Runner) Run(ctx context.Context, ws *model.Workspace, definition model.Scenario, vars *variables.Set) model.ScenarioReport {
	report := model.ScenarioReport{Scenario: definition, Started: time.Now()}
	current := vars.With(definition.Variables)
	for _, step := range definition.Steps {
		request, ok := ws.RequestByRef(step.Request)
		if !ok {
			report.Steps = append(report.Steps, model.RequestResult{Error: fmt.Errorf("request %q not found", step.Request)})
			if !definition.ContinueOnFailure {
				break
			}
			continue
		}
		result := model.RequestResult{Request: request, Extracted: map[string]string{}}
		response, sent, err := r.Executor.Execute(ctx, request, current)
		result.Response, result.Sent, result.Error = response, sent, err
		if err == nil {
			expressions := append(append([]string{}, request.Assert...), step.Assert...)
			result.Assertions = assertions.EvaluateAll(expressions, response, current)
			extractors := make(map[string]string, len(request.Extract)+len(step.Extract))
			for key, selector := range request.Extract {
				extractors[key] = selector
			}
			for key, selector := range step.Extract {
				extractors[key] = selector
			}
			for key, selector := range extractors {
				value, extractErr := responsevalue.Select(response, selector)
				if extractErr != nil {
					result.Error = fmt.Errorf("extract %q using %q: %w", key, selector, extractErr)
					break
				}
				result.Extracted[key] = responsevalue.String(value)
			}
			current = current.With(result.Extracted)
		}
		report.Steps = append(report.Steps, result)
		if !result.Passed() && !definition.ContinueOnFailure {
			break
		}
	}
	report.Duration = time.Since(report.Started)
	return report
}
