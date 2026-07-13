package variables

import (
	"fmt"
	"testing"

	"github.com/jagadishg/arbor/internal/model"
)

type fakeSecrets map[string]string

func (f fakeSecrets) Resolve(reference string) (string, error) { return f[reference], nil }

type failingSecrets struct{}

func (failingSecrets) Resolve(reference string) (string, error) {
	return "", fmt.Errorf("secret %q is unavailable", reference)
}

func TestUnusedSecretIsResolvedLazily(t *testing.T) {
	ws := &model.Workspace{}
	env := &model.Environment{Secrets: map[string]string{"unused": "env://MISSING"}}
	set, err := New(ws, env, nil, failingSecrets{})
	if err != nil {
		t.Fatalf("New() returned an error for an unused secret: %v", err)
	}
	if got, err := set.Resolve("/health"); err != nil || got != "/health" {
		t.Fatalf("Resolve() = %q, %v", got, err)
	}
}

func TestReferencedSecretStillReturnsResolutionError(t *testing.T) {
	ws := &model.Workspace{}
	env := &model.Environment{Secrets: map[string]string{"token": "env://MISSING"}}
	set, err := New(ws, env, nil, failingSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.Resolve("Bearer {{token}}"); err == nil || err.Error() != `resolve secret "token": secret "env://MISSING" is unavailable` {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestRuntimeOverrideDoesNotResolveSecret(t *testing.T) {
	env := &model.Environment{Secrets: map[string]string{"token": "env://MISSING"}}
	set, err := New(&model.Workspace{}, env, map[string]string{"token": "runtime-token"}, failingSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := set.Resolve("Bearer {{token}}"); err != nil || got != "Bearer runtime-token" {
		t.Fatalf("Resolve() = %q, %v", got, err)
	}
}

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
