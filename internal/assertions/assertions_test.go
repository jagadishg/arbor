package assertions

import (
	"testing"
	"time"

	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/variables"
)

func TestEvaluate(t *testing.T) {
	response := &model.Response{StatusCode: 201, Body: []byte(`{"user":{"id":"usr_1","roles":["admin"]}}`), Headers: map[string][]string{"Content-Type": {"application/json"}}, Duration: 20 * time.Millisecond}
	vars, _ := variables.New(&model.Workspace{Variables: map[string]string{"expected": "usr_1"}}, nil, nil, nil)
	for _, expression := range []string{"status == 201", `body.user.id == "{{expected}}"`, `body.user.roles[0] == "admin"`, `headers.Content-Type contains "json"`, "durationMs < 100"} {
		if result := Evaluate(expression, response, vars); !result.Passed {
			t.Errorf("%q failed: %s", expression, result.Message)
		}
	}
	if result := Evaluate("status == 200", response, vars); result.Passed || result.Message == "" {
		t.Fatalf("expected useful failure, got %#v", result)
	}
}
