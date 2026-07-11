package responsevalue

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jagadishg/arbor/internal/model"
)

var segmentPattern = regexp.MustCompile(`^([^\[]+)(?:\[([0-9]+)\])?$`)

func Select(response *model.Response, selector string) (any, error) {
	selector = strings.TrimSpace(selector)
	switch selector {
	case "status", "statusCode":
		return response.StatusCode, nil
	case "statusText":
		return response.Status, nil
	case "duration", "durationMs":
		return float64(response.Duration.Microseconds()) / 1000, nil
	case "size":
		return response.Size, nil
	}
	if strings.HasPrefix(selector, "headers.") {
		key := strings.TrimPrefix(selector, "headers.")
		for name, values := range response.Headers {
			if strings.EqualFold(name, key) {
				return strings.Join(values, ", "), nil
			}
		}
		return nil, fmt.Errorf("header %q not found", key)
	}
	if selector == "body" {
		return string(response.Body), nil
	}
	if strings.HasPrefix(selector, "body.") {
		var value any
		if err := json.Unmarshal(response.Body, &value); err != nil {
			return nil, fmt.Errorf("response body is not JSON: %w", err)
		}
		return selectPath(value, strings.TrimPrefix(selector, "body."))
	}
	return nil, fmt.Errorf("unsupported selector %q", selector)
}

func selectPath(value any, path string) (any, error) {
	current := value
	for _, rawSegment := range strings.Split(path, ".") {
		match := segmentPattern.FindStringSubmatch(rawSegment)
		if match == nil {
			return nil, fmt.Errorf("invalid path segment %q", rawSegment)
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", rawSegment)
		}
		current, ok = object[match[1]]
		if !ok {
			return nil, fmt.Errorf("field %q not found", match[1])
		}
		if match[2] != "" {
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("field %q is not an array", match[1])
			}
			index, _ := strconv.Atoi(match[2])
			if index >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", index)
			}
			current = items[index]
		}
	}
	return current, nil
}

func String(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}
