// Package tags manages machine-specific tags used to gate module application.
package tags

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"gopkg.in/yaml.v3"
)

// MachineConfig is the schema of ~/.config/dotular/machine.yaml.
type MachineConfig struct {
	Tags []string `yaml:"tags"`
}

// ConfigPath returns the path to the machine config file.
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dotular", "machine.yaml")
}

// Load reads the machine config, returning an empty config if the file does not exist.
func Load() (*MachineConfig, error) {
	data, err := os.ReadFile(ConfigPath())
	if os.IsNotExist(err) {
		return &MachineConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read machine config: %w", err)
	}
	var cfg MachineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse machine config: %w", err)
	}
	return &cfg, nil
}

// Save writes cfg to the machine config file, creating parent directories.
func Save(cfg *MachineConfig) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// AutoDetect returns a baseline set of tags derived from the current machine.
func AutoDetect() []string {
	tags := []string{runtime.GOOS, runtime.GOARCH}
	if h, err := os.Hostname(); err == nil && h != "" {
		tags = append(tags, h)
	}
	return tags
}

// EnsureInitialised writes the machine config with auto-detected tags if it
// does not already exist.
func EnsureInitialised() error {
	if _, err := os.Stat(ConfigPath()); err == nil {
		return nil // already exists
	}
	return Save(&MachineConfig{Tags: AutoDetect()})
}

// Add appends tag to the machine config if not already present.
func Add(tag string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	if slices.Contains(cfg.Tags, tag) {
		return nil
	}
	cfg.Tags = append(cfg.Tags, tag)
	return Save(cfg)
}

// Matches returns true when machineTags satisfies the onlyTags/excludeTags
// constraints defined on a module.
//
//   - If onlyTags is non-empty, at least one must be present in machineTags.
//   - If excludeTags is non-empty, none may be present in machineTags.
func Matches(machineTags, onlyTags, excludeTags []string) bool {
	for _, t := range excludeTags {
		if slices.Contains(machineTags, t) {
			return false
		}
	}
	if len(onlyTags) == 0 {
		return true
	}
	for _, t := range onlyTags {
		if slices.Contains(machineTags, t) {
			return true
		}
	}
	return false
}
