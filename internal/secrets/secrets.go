package secrets

import (
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

type Provider interface {
	Resolve(reference string) (string, error)
}

type SystemProvider struct{}

func (SystemProvider) Resolve(reference string) (string, error) {
	scheme, target, ok := strings.Cut(reference, "://")
	if !ok || target == "" {
		return "", fmt.Errorf("invalid secret reference %q", reference)
	}
	switch scheme {
	case "env":
		value, found := os.LookupEnv(target)
		if !found {
			return "", fmt.Errorf("environment variable %q is not set", target)
		}
		return value, nil
	case "keychain":
		service, account, found := strings.Cut(target, "/")
		if !found || service == "" || account == "" {
			return "", fmt.Errorf("keychain reference must be keychain://service/account")
		}
		value, err := keyring.Get(service, account)
		if err != nil {
			return "", fmt.Errorf("read keychain secret %s/%s: %w", service, account, err)
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported secret scheme %q", scheme)
	}
}
