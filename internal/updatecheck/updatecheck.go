// Package updatecheck checks GitHub for a newer stable Arbor release.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jagadishg/arbor/internal/config"
)

const (
	defaultURL = "https://api.github.com/repos/jagadishg/arbor/releases"
	cacheName  = "update-check.json"
	cacheAge   = 24 * time.Hour
)

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type cache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// Check returns a notification when a newer stable release is available.
// Network and cache failures are intentionally suppressed so they never
// affect Arbor startup.
func Check(ctx context.Context, current, url string) string {
	if strings.TrimSpace(current) == "" || current == "dev" || disabled() {
		return ""
	}
	path, err := cachePath()
	if err != nil {
		return ""
	}
	if cached, err := readCache(path); err == nil && time.Since(cached.CheckedAt) < cacheAge {
		return notification(current, cached.LatestVersion)
	}
	if url == "" {
		url = defaultURL
	}
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "arbor-update-check")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var releases []release
	if err := json.NewDecoder(response.Body).Decode(&releases); err != nil {
		return ""
	}
	latest := stableLatest(releases)
	_ = writeCache(path, cache{CheckedAt: time.Now(), LatestVersion: latest})
	return notification(current, latest)
}

func stableLatest(releases []release) string {
	var versions []string
	for _, item := range releases {
		if !item.Draft && !item.Prerelease && validVersion(item.TagName) {
			versions = append(versions, item.TagName)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) > 0 })
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}

func notification(current, latest string) string {
	if !validVersion(current) || !validVersion(latest) || compareVersions(latest, current) <= 0 {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return fmt.Sprintf("Update available: %s → %s. Run `brew upgrade arbor`.", current, latest)
	}
	return fmt.Sprintf("Update available: %s → %s. Run `go install github.com/jagadishg/arbor/cmd/arbor@%s`.", current, latest, latest)
}

func validVersion(value string) bool {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(value), "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func compareVersions(a, b string) int {
	parse := func(value string) [3]int {
		var result [3]int
		parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
		for i := range parts {
			for _, char := range parts[i] {
				result[i] = result[i]*10 + int(char-'0')
			}
		}
		return result
	}
	left, right := parse(a), parse(b)
	for i := range left {
		if left[i] > right[i] {
			return 1
		}
		if left[i] < right[i] {
			return -1
		}
	}
	return 0
}

func disabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("ARBOR_NO_UPDATE_CHECK")))
	return value == "1" || value == "true" || value == "yes"
}

func cachePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheName), nil
}

func readCache(path string) (cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cache{}, err
	}
	var value cache
	if err := json.Unmarshal(data, &value); err != nil {
		return cache{}, err
	}
	return value, nil
}

func writeCache(path string, value cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
