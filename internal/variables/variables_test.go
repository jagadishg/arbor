package variables

import (
	"testing"

	"github.com/jagadishg/arbor/internal/model"
)

type fakeSecrets map[string]string

func (f fakeSecrets) Resolve(reference string) (string, error) { return f[reference], nil }

func TestPrecedenceAndRedaction(t *testing.T) {
	ws := &model.Workspace{Variables: map[string]string{"host": "workspace", "id": "1"}}
	env := &model.Environment{Variables: map[string]string{"host": "environment"}, Secrets: map[string]string{"token": "test://token"}}
	set, err := New(ws, env, map[string]string{"id": "2"}, fakeSecrets{"test://token": "sensitive"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := set.Resolve("{{host}}/{{id}}?token={{ token }}")
	if err != nil || got != "environment/2?token=sensitive" {
		t.Fatalf("Resolve() = %q, %v", got, err)
	}
	if redacted := set.Redact(got); redacted != "environment/2?token=••••••" {
		t.Fatalf("Redact() = %q", redacted)
	}
}

func TestUndefinedVariable(t *testing.T) {
	set, _ := New(&model.Workspace{}, nil, nil, nil)
	if _, err := set.Resolve("{{missing}}"); err == nil {
		t.Fatal("expected missing variable error")
	}
}
