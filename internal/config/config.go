package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level list of modules.
type Config []Module

// Module groups related items under a named application or topic.
type Module struct {
	Name  string `yaml:"name"`
	Items []Item `yaml:"items"`
}

// Item represents a single configuration action within a module.
// The item type is determined by which field is populated.
type Item struct {
	// Package installation
	Package string `yaml:"package,omitempty"`

	// Script execution
	Script string `yaml:"script,omitempty"`

	// System setting (macOS defaults, Windows registry)
	Setting string `yaml:"setting,omitempty"`
	Key     string `yaml:"key,omitempty"`
	Value   any    `yaml:"value,omitempty"`

	// File copy / symlink
	File        string      `yaml:"file,omitempty"`
	Destination PlatformMap `yaml:"destination,omitempty"`
	Direction   string      `yaml:"direction,omitempty"` // push | pull | sync (default: push)
	Link        bool        `yaml:"link,omitempty"`      // symlink instead of copy

	// Shared: specifies package manager ("brew", "winget", …) or script source ("remote", "local")
	Via string `yaml:"via,omitempty"`
}

// Type returns the action type for this item.
func (i Item) Type() string {
	switch {
	case i.Package != "":
		return "package"
	case i.Script != "":
		return "script"
	case i.Setting != "":
		return "setting"
	case i.File != "":
		return "file"
	default:
		return "unknown"
	}
}

// EffectiveDirection returns the file transfer direction, defaulting to "push".
func (i Item) EffectiveDirection() string {
	switch i.Direction {
	case "pull", "sync":
		return i.Direction
	default:
		return "push"
	}
}

// PlatformMap holds per-OS values for a field.
type PlatformMap struct {
	MacOS   string `yaml:"macos,omitempty"`
	Windows string `yaml:"windows,omitempty"`
	Linux   string `yaml:"linux,omitempty"`
}

// ForOS returns the value appropriate for the given GOOS string ("darwin", "windows", "linux").
func (p PlatformMap) ForOS(goos string) string {
	switch goos {
	case "darwin":
		return p.MacOS
	case "windows":
		return p.Windows
	case "linux":
		return p.Linux
	default:
		return ""
	}
}

// Load reads and parses a YAML config file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Module returns the named module, or nil if not found.
func (c Config) Module(name string) *Module {
	for i := range c {
		if c[i].Name == name {
			return &c[i]
		}
	}
	return nil
}
