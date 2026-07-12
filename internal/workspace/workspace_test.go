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

func TestLoadDerivesCollectionsAndMetadata(t *testing.T) {
	root := t.TempDir()
	write(t, root, "arbor.yaml", "version: 1\nname: Demo\n")
	write(t, root, "collections/users/get.yaml", "version: 1\nkind: request\nid: users.get\nname: Get user\ndescription: Fetch a user.\nmethod: GET\nurl: 'https://x/users/1'\n")
	write(t, root, "collections/users/collection.yaml", "version: 1\nkind: collection\nname: users\ndescription: User endpoints.\n")
	write(t, root, "collections/health.yaml", "version: 1\nkind: request\nid: health\nname: Health\nmethod: GET\nurl: 'https://x/health'\n")

	ws, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(ws.Collections) != 2 {
		t.Fatalf("expected 2 collections, got %d: %#v", len(ws.Collections), ws.Collections)
	}
	users, ok := ws.CollectionByName("users")
	if !ok || users.Description != "User endpoints." {
		t.Fatalf("users collection metadata missing: %#v (%v)", users, ok)
	}
	if got := ws.RequestsInCollection("users"); len(got) != 1 || got[0].Ref() != "users.get" {
		t.Fatalf("unexpected requests in users collection: %#v", got)
	}
	if request, _ := ws.RequestByRef("users.get"); request.Description != "Fetch a user." {
		t.Fatalf("description not loaded: %#v", request)
	}
	if _, ok := ws.CollectionByName("default"); !ok {
		t.Fatalf("top-level request should belong to the default collection: %#v", ws.Collections)
	}
}

func TestLoadRejectsDuplicateCollections(t *testing.T) {
	root := t.TempDir()
	write(t, root, "arbor.yaml", "version: 1\nname: Demo\n")
	write(t, root, "collections/users/collection.yaml", "version: 1\nkind: collection\nname: users\n")
	write(t, root, "collections/users/nested/collection.yaml", "version: 1\nkind: collection\nname: users\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "duplicate collection") {
		t.Fatalf("expected duplicate collection error, got %v", err)
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

func TestValidateRejectsBodyWithFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "arbor.yaml", "version: 1\nname: Demo\n")
	write(t, root, "collections/up/post.yaml", "version: 1\nkind: request\nid: up.post\nname: Up\nmethod: POST\nurl: 'https://x'\nbody:\n  a: 1\nfiles:\n  doc: ./x.txt\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "body cannot be combined with form or files") {
		t.Fatalf("expected body/files conflict error, got %v", err)
	}
}
