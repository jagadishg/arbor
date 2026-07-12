package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestMultipartUpload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("file body"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotType, gotCaption, gotFile string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		_ = r.ParseMultipartForm(1 << 20)
		gotCaption = r.FormValue("caption")
		file, _, err := r.FormFile("document")
		if err == nil {
			data, _ := io.ReadAll(file)
			gotFile = string(data)
			file.Close()
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	ws := &model.Workspace{Variables: map[string]string{"base_url": server.URL, "greeting": "hello"}}
	vars, _ := variables.New(ws, nil, nil, nil)
	def := model.Request{Method: "POST", URL: "{{base_url}}/post", Path: filepath.Join(dir, "req.yaml"),
		Form:  map[string]string{"caption": "{{greeting}} there"},
		Files: map[string]string{"document": "./hello.txt"}}
	if _, err := (&Executor{Client: server.Client()}).Execute(context.Background(), def, vars); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotType, "multipart/form-data") {
		t.Fatalf("content-type = %q", gotType)
	}
	if gotCaption != "hello there" || gotFile != "file body" {
		t.Fatalf("caption=%q file=%q", gotCaption, gotFile)
	}
}

func TestFormURLEncoded(t *testing.T) {
	var gotType, gotValue string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotValue = r.FormValue("q")
		w.WriteHeader(200)
	}))
	defer server.Close()

	ws := &model.Workspace{Variables: map[string]string{"base_url": server.URL}}
	vars, _ := variables.New(ws, nil, nil, nil)
	def := model.Request{Method: "POST", URL: "{{base_url}}/f", Form: map[string]string{"q": "hi"}}
	if _, err := (&Executor{Client: server.Client()}).Execute(context.Background(), def, vars); err != nil {
		t.Fatal(err)
	}
	if gotType != "application/x-www-form-urlencoded" || gotValue != "hi" {
		t.Fatalf("type=%q value=%q", gotType, gotValue)
	}
}

func TestMissingFileErrors(t *testing.T) {
	ws := &model.Workspace{Variables: map[string]string{}}
	vars, _ := variables.New(ws, nil, nil, nil)
	def := model.Request{Method: "POST", URL: "https://example.com", Path: filepath.Join(t.TempDir(), "req.yaml"),
		Files: map[string]string{"x": "./nope.txt"}}
	if _, err := BuildRequest(context.Background(), def, vars); err == nil {
		t.Fatal("expected error for missing file")
	}
}
