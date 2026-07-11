package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWorkspace(t *testing.T) {
	root := t.TempDir()
	write(t, root, "arbor.yaml", "version: 1\nname: Demo\ndefaultEnvironment: local\n")
	write(t, root, "collections/users/get.yaml", "version: 1\nkind: request\nid: users.get\nname: Get user\nmethod: GET\nurl: '{{base_url}}/users/1'\n")
	write(t, root, "environments/local.yaml", "version: 1\nkind: environment\nname: local\nvariables:\n  base_url: http://localhost:8080\n")

	ws, err := Load(filepath.Join(root, "collections", "users"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ws.Name != "Demo" || len(ws.Requests) != 1 || len(ws.Environments) != 1 {
		t.Fatalf("unexpected workspace: %#v", ws)
	}
	if request, ok := ws.RequestByRef("users.get"); !ok || request.Method != "GET" {
		t.Fatalf("request lookup failed: %#v, %v", request, ok)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	write(t, root, "arbor.yaml", "version: 1\nname: Demo\nmystery: true\n")
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func write(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
