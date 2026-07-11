// Package config manages Arbor's central, user-level configuration: the
// registry of known workspaces and the last-used workspace. It lives alongside
// the interaction files (aliases/hotkeys) under the OS config directory and is
// distinct from the per-workspace files, which remain the source of truth for a
// workspace's contents.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Version is the schema version of the central config file.
const Version = 1

// Entry is a registered workspace: a display name and the absolute path to the
// directory containing its arbor.yaml.
type Entry struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// Config is the central user configuration.
type Config struct {
	Version       int     `yaml:"version"`
	Workspaces    []Entry `yaml:"workspaces,omitempty"`
	LastWorkspace string  `yaml:"lastWorkspace,omitempty"`
}

// Path returns the location of the central config file. It honours the
// ARBOR_CONFIG environment variable when set, otherwise it lives beside the
// interaction files under the OS config directory.
func Path() (string, error) {
	if custom := os.Getenv("ARBOR_CONFIG"); custom != "" {
		return custom, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "arbor", "config.yaml"), nil
}

// Load reads the central config. A missing file is not an error: it returns an
// empty config so first run behaves like an empty registry. Workspace paths are
// expanded (~) and made absolute.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{Version: Version}, nil
	}
	if err != nil {
		return nil, err
	}
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if config.Version == 0 {
		config.Version = Version
	}
	for index := range config.Workspaces {
		config.Workspaces[index].Path = normalize(config.Workspaces[index].Path)
	}
	config.LastWorkspace = normalize(config.LastWorkspace)
	return &config, nil
}

// Save writes the central config, creating its directory if necessary.
func (c *Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if c.Version == 0 {
		c.Version = Version
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Register adds or refreshes a workspace, keyed by absolute path. When name is
// empty an existing name is kept, otherwise the folder base is used. It returns
// the stored entry.
func (c *Config) Register(path, name string) Entry {
	path = normalize(path)
	for index := range c.Workspaces {
		if c.Workspaces[index].Path == path {
			if name != "" {
				c.Workspaces[index].Name = name
			}
			return c.Workspaces[index]
		}
	}
	if name == "" {
		name = filepath.Base(path)
	}
	entry := Entry{Name: name, Path: path}
	c.Workspaces = append(c.Workspaces, entry)
	sort.Slice(c.Workspaces, func(i, j int) bool { return c.Workspaces[i].Name < c.Workspaces[j].Name })
	return entry
}

// Remove deletes a workspace by name. It reports whether an entry was removed.
func (c *Config) Remove(name string) bool {
	for index := range c.Workspaces {
		if c.Workspaces[index].Name == name {
			c.Workspaces = append(c.Workspaces[:index], c.Workspaces[index+1:]...)
			return true
		}
	}
	return false
}

// Find returns the entry registered under name.
func (c *Config) Find(name string) (Entry, bool) {
	for _, entry := range c.Workspaces {
		if entry.Name == name {
			return entry, true
		}
	}
	return Entry{}, false
}

// Touch records path as the most recently used workspace.
func (c *Config) Touch(path string) {
	c.LastWorkspace = normalize(path)
}

func normalize(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" || (len(path) >= 2 && path[:2] == "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
