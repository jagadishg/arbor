package skill

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

const Name = "arbor-api-authoring"

const ownershipMarker = ".arbor-skill"

func ReadReference(name string) ([]byte, error) {
	return fs.ReadFile(Files, "assets/arbor-api-authoring/references/"+name)
}

type Target struct {
	Agent string
	Path  string
}

func Targets(agent string) ([]Target, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(agent) {
	case "codex":
		return []Target{
			{Agent: "codex", Path: filepath.Join(home, ".codex", "skills", Name)},
			{Agent: "shared", Path: filepath.Join(home, ".agents", "skills", Name)},
		}, nil
	case "agents", "shared":
		return []Target{{Agent: "shared", Path: filepath.Join(home, ".agents", "skills", Name)}}, nil
	case "claude":
		return []Target{{Agent: "claude", Path: filepath.Join(home, ".claude", "skills", Name)}}, nil
	case "all", "":
		return []Target{
			{Agent: "codex", Path: filepath.Join(home, ".codex", "skills", Name)},
			{Agent: "shared", Path: filepath.Join(home, ".agents", "skills", Name)},
			{Agent: "claude", Path: filepath.Join(home, ".claude", "skills", Name)},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported agent %q; choose codex, claude, agents, or all", agent)
	}
}

func Install(agent string) ([]Target, error) {
	targets, err := Targets(agent)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if err := copyTree(target.Path); err != nil {
			return nil, fmt.Errorf("install %s skill: %w", target.Agent, err)
		}
		if err := os.WriteFile(filepath.Join(target.Path, ownershipMarker), []byte(Name+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("mark %s skill: %w", target.Agent, err)
		}
	}
	return targets, nil
}

func Status(agent string) ([]Target, error) {
	targets, err := Targets(agent)
	if err != nil {
		return nil, err
	}
	for index := range targets {
		if _, err := os.Stat(filepath.Join(targets[index].Path, ownershipMarker)); err == nil {
			targets[index].Path += " (installed)"
		} else if os.IsNotExist(err) {
			targets[index].Path += " (not installed)"
		}
	}
	return targets, nil
}

func Uninstall(agent string) ([]Target, error) {
	targets, err := Targets(agent)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		marker, err := os.ReadFile(filepath.Join(target.Path, ownershipMarker))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(marker)) != Name {
			return nil, fmt.Errorf("refusing to remove %s: ownership marker does not match", target.Path)
		}
		if err := os.RemoveAll(target.Path); err != nil {
			return nil, fmt.Errorf("uninstall %s skill: %w", target.Agent, err)
		}
	}
	return targets, nil
}

// InitProject creates an AGENTS.md template when none exists. If the project
// already has instructions, it returns the Arbor section for the caller to
// present rather than overwriting user-authored guidance.
func InitProject(directory string) (created bool, instructions string, err error) {
	path := filepath.Join(directory, "AGENTS.md")
	data, err := ProjectInstructions()
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(path); err == nil {
		return false, string(data), nil
	} else if !os.IsNotExist(err) {
		return false, "", err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return false, "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func ProjectInstructions() ([]byte, error) {
	return fs.ReadFile(Files, "assets/project/AGENTS.md")
}

func copyTree(destination string) error {
	return fs.WalkDir(Files, "assets/arbor-api-authoring", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "assets/arbor-api-authoring")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return os.MkdirAll(destination, 0o755)
		}
		target := filepath.Join(destination, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(Files, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func homeDir() (string, error) {
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}
	if runtime.GOOS == "windows" {
		if home := os.Getenv("USERPROFILE"); home != "" {
			return home, nil
		}
	}
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	return current.HomeDir, nil
}
