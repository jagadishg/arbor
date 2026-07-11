package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/variables"
)

func TestExecute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Query().Get("expand") != "profile" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %s %#v", r.Method, r.URL, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "Ada" {
			t.Fatalf("unexpected body %#v, %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"usr_1"}`))
	}))
	defer server.Close()

	ws := &model.Workspace{Variables: map[string]string{"base_url": server.URL, "token": "secret", "name": "Ada"}}
	vars, _ := variables.New(ws, nil, nil, nil)
	definition := model.Request{Method: "POST", URL: "{{base_url}}/users", Query: map[string]string{"expand": "profile"}, Headers: map[string]string{"Authorization": "Bearer {{token}}"}, Body: map[string]any{"name": "{{name}}"}}
	response, err := (&Executor{Client: server.Client()}).Execute(context.Background(), definition, vars)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || string(response.Body) != `{"id":"usr_1"}` {
		t.Fatalf("unexpected response: %#v", response)
	}
}
