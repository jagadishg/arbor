package variables

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/secrets"
)

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\}\}`)

type Set struct {
	values  map[string]string
	secrets map[string]struct{}
}

func New(ws *model.Workspace, environment *model.Environment, runtime map[string]string, provider secrets.Provider) (*Set, error) {
	set := &Set{values: map[string]string{}, secrets: map[string]struct{}{}}
	for key, value := range ws.Variables {
		set.values[key] = value
	}
	if environment != nil {
		for key, value := range environment.Variables {
			set.values[key] = value
		}
		for key, reference := range environment.Secrets {
			if provider == nil {
				return nil, fmt.Errorf("resolve secret %q: no secret provider configured", key)
			}
			value, err := provider.Resolve(reference)
			if err != nil {
				return nil, fmt.Errorf("resolve secret %q: %w", key, err)
			}
			set.values[key] = value
			set.secrets[value] = struct{}{}
		}
	}
	for key, value := range runtime {
		set.values[key] = value
	}
	return set, nil
}

func (s *Set) With(values map[string]string) *Set {
	next := &Set{values: map[string]string{}, secrets: map[string]struct{}{}}
	for key, value := range s.values {
		next.values[key] = value
	}
	for value := range s.secrets {
		next.secrets[value] = struct{}{}
	}
	for key, value := range values {
		next.values[key] = value
	}
	return next
}

func (s *Set) Resolve(input string) (string, error) {
	var missing []string
	resolved := placeholder.ReplaceAllStringFunc(input, func(match string) string {
		parts := placeholder.FindStringSubmatch(match)
		value, ok := s.values[parts[1]]
		if !ok {
			missing = append(missing, parts[1])
			return match
		}
		return value
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("undefined variables: %s", strings.Join(missing, ", "))
	}
	return resolved, nil
}

func (s *Set) Redact(input string) string {
	for secret := range s.secrets {
		if secret != "" {
			input = strings.ReplaceAll(input, secret, "••••••")
		}
	}
	return input
}

func (s *Set) Values() map[string]string {
	values := make(map[string]string, len(s.values))
	for key, value := range s.values {
		values[key] = value
	}
	return values
}

// Secrets returns the resolved secret values, used to redact sensitive request
// details when displaying a request without revealing them.
func (s *Set) Secrets() []string {
	secrets := make([]string, 0, len(s.secrets))
	for value := range s.secrets {
		secrets = append(secrets, value)
	}
	return secrets
}
