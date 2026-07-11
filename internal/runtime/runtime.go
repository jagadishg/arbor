package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/variables"
)

const defaultTimeout = 30 * time.Second

type Executor struct {
	Client *http.Client
}

func New(options model.HTTPOptions) (*Executor, error) {
	timeout := defaultTimeout
	if options.Timeout != "" {
		parsed, err := time.ParseDuration(options.Timeout)
		if err != nil {
			return nil, fmt.Errorf("parse workspace HTTP timeout: %w", err)
		}
		timeout = parsed
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: options.InsecureTLS} //nolint:gosec // Explicit workspace option.
	client := &http.Client{Timeout: timeout, Transport: transport}
	if options.FollowRedirects != nil && !*options.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &Executor{Client: client}, nil
}

func (e *Executor) Execute(ctx context.Context, definition model.Request, vars *variables.Set) (*model.Response, error) {
	request, err := BuildRequest(ctx, definition, vars)
	if err != nil {
		return nil, err
	}
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	if definition.Timeout != "" {
		timeout, err := time.ParseDuration(definition.Timeout)
		if err != nil {
			return nil, fmt.Errorf("parse request timeout: %w", err)
		}
		clone := *client
		clone.Timeout = timeout
		client = &clone
	}
	started := time.Now()
	response, err := client.Do(request)
	duration := time.Since(started)
	if err != nil {
		return nil, fmt.Errorf("execute %s %s: %w", request.Method, vars.Redact(request.URL.String()), err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return &model.Response{
		Status: response.Status, StatusCode: response.StatusCode, Headers: response.Header,
		Body: body, Duration: duration, Size: int64(len(body)), URL: vars.Redact(request.URL.String()),
	}, nil
}

func BuildRequest(ctx context.Context, definition model.Request, vars *variables.Set) (*http.Request, error) {
	rawURL, err := vars.Resolve(definition.URL)
	if err != nil {
		return nil, fmt.Errorf("resolve URL: %w", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid request URL %q", vars.Redact(rawURL))
	}
	query := parsed.Query()
	for key, value := range definition.Query {
		resolved, err := vars.Resolve(value)
		if err != nil {
			return nil, fmt.Errorf("resolve query %q: %w", key, err)
		}
		query.Set(key, resolved)
	}
	parsed.RawQuery = query.Encode()

	body, contentType, err := encodeBody(definition.Body, vars)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(definition.Method), parsed.String(), body)
	if err != nil {
		return nil, err
	}
	for key, value := range definition.Headers {
		resolved, err := vars.Resolve(value)
		if err != nil {
			return nil, fmt.Errorf("resolve header %q: %w", key, err)
		}
		request.Header.Set(key, resolved)
	}
	if contentType != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("User-Agent", "arbor/dev")
	return request, nil
}

func encodeBody(body any, vars *variables.Set) (io.Reader, string, error) {
	if body == nil {
		return nil, "", nil
	}
	if raw, ok := body.(string); ok {
		resolved, err := vars.Resolve(raw)
		return strings.NewReader(resolved), "text/plain", err
	}
	resolved, err := resolveValue(body, vars)
	if err != nil {
		return nil, "", fmt.Errorf("resolve body: %w", err)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("encode JSON body: %w", err)
	}
	return bytes.NewReader(encoded), "application/json", nil
}

func resolveValue(value any, vars *variables.Set) (any, error) {
	switch typed := value.(type) {
	case string:
		return vars.Resolve(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			resolved, err := resolveValue(item, vars)
			if err != nil {
				return nil, err
			}
			result[index] = resolved
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := resolveValue(item, vars)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}
