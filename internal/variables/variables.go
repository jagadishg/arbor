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
	values     map[string]string
	overrides  map[string]string
	secretRefs map[string]string
	secrets    map[string]struct{}
	provider   secrets.Provider
}

func New(ws *model.Workspace, environment *model.Environment, runtime map[string]string, provider secrets.Provider) (*Set, error) {
	set := &Set{
		values:     map[string]string{},
		overrides:  map[string]string{},
		secretRefs: map[string]string{},
		secrets:    map[string]struct{}{},
		provider:   provider,
	}
	for key, value := range ws.Variables {
		set.values[key] = value
	}
	if environment != nil {
		for key, value := range environment.Variables {
			set.values[key] = value
		}
		for key, reference := range environment.Secrets {
			set.secretRefs[key] = reference
		}
	}
	for key, value := range runtime {
		set.overrides[key] = value
	}
	return set, nil
}

func (s *Set) With(values map[string]string) *Set {
	next := &Set{
		values:     map[string]string{},
		overrides:  map[string]string{},
		secretRefs: map[string]string{},
		secrets:    map[string]struct{}{},
		provider:   s.provider,
	}
	for key, value := range s.values {
		next.values[key] = value
	}
	for key, value := range s.overrides {
		next.overrides[key] = value
	}
	for key, reference := range s.secretRefs {
		next.secretRefs[key] = reference
	}
	for value := range s.secrets {
		next.secrets[value] = struct{}{}
	}
	for key, value := range values {
		next.overrides[key] = value
	}
	return next
}

func (s *Set) Resolve(input string) (string, error) {
	var missing []string
	var resolutionErr error
	resolved := placeholder.ReplaceAllStringFunc(input, func(match string) string {
		parts := placeholder.FindStringSubmatch(match)
		key := parts[1]
		if value, ok := s.overrides[key]; ok {
			return value
		}
		if reference, ok := s.secretRefs[key]; ok {
			if s.provider == nil {
				resolutionErr = fmt.Errorf("resolve secret %q: no secret provider configured", key)
				return match
			}
			value, err := s.provider.Resolve(reference)
			if err != nil {
				resolutionErr = fmt.Errorf("resolve secret %q: %w", key, err)
				return match
			}
			s.values[key] = value
			s.secrets[value] = struct{}{}
			return value
		}
		if value, ok := s.values[key]; ok {
			return value
		}
		if resolutionErr == nil {
			missing = append(missing, parts[1])
		}
		return match
	})
	if resolutionErr != nil {
		return "", resolutionErr
	}
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
	for key, value := range s.overrides {
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
