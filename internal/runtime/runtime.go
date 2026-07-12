package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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

func (e *Executor) Execute(ctx context.Context, definition model.Request, vars *variables.Set) (*model.Response, *model.SentRequest, error) {
	request, err := BuildRequest(ctx, definition, vars)
	if err != nil {
		return nil, nil, err
	}
	sent := SnapshotRequest(definition, request, vars)
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	if definition.Timeout != "" {
		timeout, err := time.ParseDuration(definition.Timeout)
		if err != nil {
			return nil, sent, fmt.Errorf("parse request timeout: %w", err)
		}
		clone := *client
		clone.Timeout = timeout
		client = &clone
	}
	started := time.Now()
	response, err := client.Do(request)
	duration := time.Since(started)
	if err != nil {
		return nil, sent, fmt.Errorf("execute %s %s: %w", request.Method, vars.Redact(request.URL.String()), err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, sent, fmt.Errorf("read response body: %w", err)
	}
	return &model.Response{
		Status: response.Status, StatusCode: response.StatusCode, Headers: response.Header,
		Body: body, Duration: duration, Size: int64(len(body)), URL: vars.Redact(request.URL.String()),
	}, sent, nil
}

// SnapshotRequest captures the resolved request for display in the request pane.
// The body is read via GetBody so the request stays sendable; multipart bodies
// are summarised rather than dumping raw file bytes.
func SnapshotRequest(definition model.Request, request *http.Request, vars *variables.Set) *model.SentRequest {
	sent := &model.SentRequest{
		Method:  request.Method,
		URL:     request.URL.String(),
		Headers: map[string][]string{},
		Secrets: vars.Secrets(),
	}
	for key, values := range request.Header {
		copied := make([]string, len(values))
		copy(copied, values)
		sent.Headers[key] = copied
	}
	if strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data") {
		sent.Body = multipartSummary(definition, vars)
		return sent
	}
	if request.GetBody != nil {
		if reader, err := request.GetBody(); err == nil {
			if data, readErr := io.ReadAll(reader); readErr == nil {
				sent.Body = string(data)
			}
		}
	}
	return sent
}

// multipartSummary renders the form fields and attached files of a multipart
// request as a readable list, avoiding raw binary file contents.
func multipartSummary(definition model.Request, vars *variables.Set) string {
	var builder strings.Builder
	for _, key := range sortedKeys(definition.Form) {
		resolved, err := vars.Resolve(definition.Form[key])
		if err != nil {
			resolved = definition.Form[key]
		}
		fmt.Fprintf(&builder, "%s: %s\n", key, resolved)
	}
	for _, field := range sortedKeys(definition.Files) {
		resolved, err := vars.Resolve(definition.Files[field])
		if err != nil {
			resolved = definition.Files[field]
		}
		fmt.Fprintf(&builder, "%s: @%s (file)\n", field, resolved)
	}
	return strings.TrimRight(builder.String(), "\n")
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

	body, contentType, err := encodeRequestBody(definition, vars)
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

// encodeRequestBody chooses how to encode the request body: multipart/form-data
// when files are attached, application/x-www-form-urlencoded for form fields
// alone, otherwise the JSON/text body.
func encodeRequestBody(definition model.Request, vars *variables.Set) (io.Reader, string, error) {
	if len(definition.Files) > 0 {
		return encodeMultipart(definition, vars)
	}
	if len(definition.Form) > 0 {
		return encodeForm(definition.Form, vars)
	}
	return encodeBody(definition.Body, vars)
}

func encodeForm(form map[string]string, vars *variables.Set) (io.Reader, string, error) {
	values := url.Values{}
	for _, key := range sortedKeys(form) {
		resolved, err := vars.Resolve(form[key])
		if err != nil {
			return nil, "", fmt.Errorf("resolve form field %q: %w", key, err)
		}
		values.Set(key, resolved)
	}
	return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil
}

func encodeMultipart(definition model.Request, vars *variables.Set) (io.Reader, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, key := range sortedKeys(definition.Form) {
		resolved, err := vars.Resolve(definition.Form[key])
		if err != nil {
			return nil, "", fmt.Errorf("resolve form field %q: %w", key, err)
		}
		if err := writer.WriteField(key, resolved); err != nil {
			return nil, "", err
		}
	}
	baseDir := ""
	if definition.Path != "" {
		baseDir = filepath.Dir(definition.Path)
	}
	for _, field := range sortedKeys(definition.Files) {
		resolvedPath, err := vars.Resolve(definition.Files[field])
		if err != nil {
			return nil, "", fmt.Errorf("resolve file %q: %w", field, err)
		}
		path := resolvedPath
		if !filepath.IsAbs(path) && baseDir != "" {
			path = filepath.Join(baseDir, path)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, "", fmt.Errorf("attach file %q: %w", field, err)
		}
		part, err := writer.CreateFormFile(field, filepath.Base(path))
		if err != nil {
			file.Close()
			return nil, "", err
		}
		if _, err := io.Copy(part, file); err != nil {
			file.Close()
			return nil, "", fmt.Errorf("read file %q: %w", field, err)
		}
		file.Close()
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return &buffer, writer.FormDataContentType(), nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
