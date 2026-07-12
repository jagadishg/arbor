package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jagadishg/arbor/internal/config"
)

func TestInitAndValidate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo")
	var output bytes.Buffer
	command := New(Options{Out: &output, ErrOut: &output, Dir: root})
	command.SetArgs([]string{"init", "--name", "Demo"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "arbor.yaml")); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	command = New(Options{Out: &output, ErrOut: &output, Dir: root})
	command.SetArgs([]string{"validate"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "✓ Demo") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestNewRequestAndScenario(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo")
	var output bytes.Buffer
	run := func(args ...string) {
		t.Helper()
		command := New(Options{Out: &output, ErrOut: &output, Dir: root})
		command.SetArgs(args)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	run("init", "--name", "Demo")
	run("new", "request", "users.get", "--url", "{{base_url}}/users/1")
	run("new", "scenario", "users.smoke", "--request", "users.get")
	run("validate")
	for _, path := range []string{"collections/users/get.yaml", "scenarios/users/smoke.yaml"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestParseVariablesRejectsMalformedValue(t *testing.T) {
	if _, err := parseVariables([]string{"missing-separator"}); err == nil {
		t.Fatal("expected malformed variable error")
	}
}

func TestNewCollectionListAndDescribe(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo")
	var output bytes.Buffer
	run := func(args ...string) {
		t.Helper()
		output.Reset()
		command := New(Options{Out: &output, ErrOut: &output, Dir: root})
		command.SetArgs(args)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	run("init", "--name", "Demo")
	run("new", "collection", "users", "--description", "User endpoints")
	if _, err := os.Stat(filepath.Join(root, "collections", "users", "collection.yaml")); err != nil {
		t.Fatalf("collection.yaml not written: %v", err)
	}
	run("new", "request", "users.get", "--url", "https://example.com/users/1")

	run("list", "collections")
	if !strings.Contains(output.String(), "users") || !strings.Contains(output.String(), "User endpoints") {
		t.Fatalf("list collections output = %q", output.String())
	}

	run("describe", "users.get")
	if !strings.Contains(output.String(), "Collection: users") {
		t.Fatalf("describe output = %q", output.String())
	}

	run("describe", "users.get", "--json")
	if !strings.Contains(output.String(), "\"collection\":\"users\"") {
		t.Fatalf("describe --json output = %q", output.String())
	}

	run("describe", "users")
	if !strings.Contains(output.String(), "users.get") {
		t.Fatalf("collection describe should list member requests: %q", output.String())
	}
}

func TestDescribeUnknownRefErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo")
	var output bytes.Buffer
	command := New(Options{Out: &output, ErrOut: &output, Dir: root})
	command.SetArgs([]string{"init", "--name", "Demo"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	command = New(Options{Out: &output, ErrOut: &output, Dir: root})
	command.SetArgs([]string{"describe", "nope.missing"})
	if err := command.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error for unknown ref")
	}
}

func TestRegisterAndWorkspaces(t *testing.T) {
	t.Setenv("ARBOR_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	root := filepath.Join(t.TempDir(), "demo")
	var output bytes.Buffer
	run := func(dir string, args ...string) {
		t.Helper()
		output.Reset()
		command := New(Options{Out: &output, ErrOut: &output, Dir: dir})
		command.SetArgs(args)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	run(root, "init", "--name", "Demo")
	run(root, "register")
	run(root, "workspaces")
	if !strings.Contains(output.String(), "Demo") || !strings.Contains(output.String(), root) {
		t.Fatalf("workspaces output = %q", output.String())
	}
}

func TestResolveDirMapsWorkspaceName(t *testing.T) {
	t.Setenv("ARBOR_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	root := t.TempDir()
	cfg, _ := config.Load()
	cfg.Register(root, "acme")
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if dir, err := resolveDir(Options{Dir: "."}, "acme"); err != nil || dir != root {
		t.Fatalf("resolveDir(acme) = %q, %v", dir, err)
	}
	if _, err := resolveDir(Options{Dir: "."}, "missing"); err == nil {
		t.Fatal("expected error for unknown workspace")
	}
	if dir, _ := resolveDir(Options{Dir: "here"}, ""); dir != "here" {
		t.Fatalf("resolveDir(\"\") should return cwd, got %q", dir)
	}
}

func TestDescribeIncludesFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo")
	var output bytes.Buffer
	run := func(args ...string) {
		t.Helper()
		output.Reset()
		command := New(Options{Out: &output, ErrOut: &output, Dir: root})
		command.SetArgs(args)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	run("init", "--name", "Demo")
	reqPath := filepath.Join(root, "collections", "up", "post.yaml")
	if err := os.MkdirAll(filepath.Dir(reqPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reqPath, []byte("version: 1\nkind: request\nid: up.post\nname: Up\nmethod: POST\nurl: 'https://x'\nfiles:\n  document: ./f.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("describe", "up.post", "--json")
	if !strings.Contains(output.String(), "\"files\":{\"document\":\"./f.txt\"}") {
		t.Fatalf("describe --json missing files: %q", output.String())
	}
}
