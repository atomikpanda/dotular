package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level document. It supports two on-disk formats:
//
//   - New (mapping): has a "modules" key and optional "age" key.
//   - Legacy (sequence): a bare list of modules (no global settings).
type Config struct {
	Age     *AgeConfig `yaml:"age,omitempty"`
	Modules []Module   `yaml:"modules"`
}

// AgeConfig holds age encryption credentials for encrypted file items.
// Exactly one of Identity or Passphrase should be set.
// Passphrase supports the "env:VARNAME" syntax to read from an environment variable.
type AgeConfig struct {
	Identity   string `yaml:"identity,omitempty"`   // path to age identity file
	Passphrase string `yaml:"passphrase,omitempty"` // literal or "env:VARNAME"
}

// Module groups related items under a named application or topic.
type Module struct {
	Name        string      `yaml:"name"`
	Items       []Item      `yaml:"items"`
	OnlyTags    []string    `yaml:"only_tags,omitempty"`
	ExcludeTags []string    `yaml:"exclude_tags,omitempty"`
	Hooks       ModuleHooks `yaml:"hooks,omitempty"`
}

// ModuleHooks are shell commands that run around module application.
type ModuleHooks struct {
	BeforeApply string `yaml:"before_apply,omitempty"`
	AfterApply  string `yaml:"after_apply,omitempty"`
	BeforeSync  string `yaml:"before_sync,omitempty"`
	AfterSync   string `yaml:"after_sync,omitempty"`
}

// Item represents a single configuration action within a module.
// The item type is determined by which primary field is populated.
type Item struct {
	// --- package ---
	Package string `yaml:"package,omitempty"`

	// --- script ---
	Script string `yaml:"script,omitempty"`

	// --- setting ---
	Setting string `yaml:"setting,omitempty"`
	Key     string `yaml:"key,omitempty"`
	Value   any    `yaml:"value,omitempty"`

	// --- file ---
	File        string      `yaml:"file,omitempty"`
	Destination PlatformMap `yaml:"destination,omitempty"`
	Direction   string      `yaml:"direction,omitempty"` // push | pull | sync (default: push)
	Link        bool        `yaml:"link,omitempty"`      // symlink instead of copy
	Permissions string      `yaml:"permissions,omitempty"` // Unix octal string, e.g. "0600"
	Encrypted   bool        `yaml:"encrypted,omitempty"` // stored encrypted with age

	// --- shared ---
	// Via specifies the package manager (brew, winget, …) or script source (remote, local).
	Via string `yaml:"via,omitempty"`

	// --- idempotency ---
	// SkipIf is a shell command; if it exits 0 the item is skipped.
	SkipIf string `yaml:"skip_if,omitempty"`

	// --- verification ---
	// Verify is a shell command run after apply; non-zero exit is a failure.
	Verify string `yaml:"verify,omitempty"`

	// --- hooks ---
	Hooks ItemHooks `yaml:"hooks,omitempty"`
}

// ItemHooks are shell commands that run around individual item application.
type ItemHooks struct {
	BeforeApply string `yaml:"before_apply,omitempty"`
	AfterApply  string `yaml:"after_apply,omitempty"`
	BeforeSync  string `yaml:"before_sync,omitempty"`
	AfterSync   string `yaml:"after_sync,omitempty"`
}

// Type returns the item's action type string.
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

// ForOS returns the value for the given runtime.GOOS string.
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

// Load reads and parses a config file. It accepts both the new mapping format
// (with a "modules" key) and the legacy bare-sequence format.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	// Parse into a raw YAML node so we can inspect the root kind.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return Config{}, nil // empty file
	}

	doc := root.Content[0]
	var cfg Config

	switch doc.Kind {
	case yaml.MappingNode:
		if err := doc.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	case yaml.SequenceNode:
		// Legacy format: bare list of modules.
		if err := doc.Decode(&cfg.Modules); err != nil {
			return Config{}, fmt.Errorf("parse config (legacy format): %w", err)
		}
	default:
		return Config{}, fmt.Errorf("config root must be a mapping or sequence, got kind %d", doc.Kind)
	}

	return cfg, nil
}

// Module returns the named module, or nil if not found.
func (c Config) Module(name string) *Module {
	for i := range c.Modules {
		if c.Modules[i].Name == name {
			return &c.Modules[i]
		}
	}
	return nil
}
