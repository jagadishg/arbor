package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
