package scenario

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jagadishg/arbor/internal/model"
	httpruntime "github.com/jagadishg/arbor/internal/runtime"
	"github.com/jagadishg/arbor/internal/variables"
)

func TestScenarioExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"token":"abc"}`))
		case "/me":
			if r.Header.Get("Authorization") != "Bearer abc" {
				t.Errorf("token was not extracted")
			}
			_, _ = w.Write([]byte(`{"id":"user_1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ws := &model.Workspace{Variables: map[string]string{"base": server.URL}, Requests: []model.Request{
		{ID: "login", Name: "Login", Method: "POST", URL: "{{base}}/login", Extract: map[string]string{"token": "body.token"}, Assert: []string{"status == 200"}},
		{ID: "me", Name: "Me", Method: "GET", URL: "{{base}}/me", Headers: map[string]string{"Authorization": "Bearer {{token}}"}, Assert: []string{`body.id == "user_1"`}},
	}}
	definition := model.Scenario{Name: "Auth flow", Steps: []model.ScenarioStep{{Request: "login"}, {Request: "me"}}}
	vars, _ := variables.New(ws, nil, nil, nil)
	executor := &httpruntime.Executor{Client: server.Client()}
	report := (&Runner{Executor: executor}).Run(context.Background(), ws, definition, vars)
	if !report.Passed() {
		for i, step := range report.Steps {
			t.Logf("step %d: %v %#v", i, step.Error, step.Assertions)
		}
		t.Fatal(fmt.Sprintf("scenario failed: %#v", report))
	}
}
