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
