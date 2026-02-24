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
type AgeConfig struct {
	Identity   string `yaml:"identity,omitempty"`
	Passphrase string `yaml:"passphrase,omitempty"` // literal or "env:VARNAME"
}

// Module groups related items under a named application or topic.
// A module may reference a registry module via From; at resolve time the
// registry module's items are fetched, parameterised, and merged with Override.
type Module struct {
	// Local module identity.
	Name        string      `yaml:"name,omitempty"`
	Items       []Item      `yaml:"items,omitempty"`
	OnlyTags    []string    `yaml:"only_tags,omitempty"`
	ExcludeTags []string    `yaml:"exclude_tags,omitempty"`
	Hooks       ModuleHooks `yaml:"hooks,omitempty"`

	// Registry module reference (mutually exclusive with Items in source YAML;
	// after resolution Items is populated from the registry module).
	From     string         `yaml:"from,omitempty"`     // e.g. "dotular.dev/modules/neovim@1.0.0"
	With     map[string]any `yaml:"with,omitempty"`     // parameter overrides
	Override []Item         `yaml:"override,omitempty"` // items that replace matching registry items
}

// IsRegistry returns true when this module is backed by a registry reference.
func (m Module) IsRegistry() bool { return m.From != "" }

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
	Link        bool        `yaml:"link,omitempty"`
	Permissions string      `yaml:"permissions,omitempty"` // Unix octal, e.g. "0600"
	Encrypted   bool        `yaml:"encrypted,omitempty"`

	// --- directory ---
	// Directory manages a whole directory tree. Supports the same direction,
	// link, and permissions semantics as file items.
	Directory string `yaml:"directory,omitempty"`

	// --- binary ---
	// Binary downloads a pre-built binary from Source URLs, extracts it, and
	// installs it to InstallTo. Version is used for template rendering and
	// can be referenced in Source URLs via {{ .version }}.
	Binary    string      `yaml:"binary,omitempty"`
	Version   string      `yaml:"version,omitempty"`
	Source    PlatformMap `yaml:"source,omitempty"`  // download URL per OS
	InstallTo string      `yaml:"install_to,omitempty"` // destination directory

	// --- run ---
	// Run executes an inline shell command. After is informational: it names
	// the item type this run step logically depends on (ordering is determined
	// by declaration order in the items list).
	Run   string `yaml:"run,omitempty"`
	After string `yaml:"after,omitempty"`

	// --- shared ---
	Via    string `yaml:"via,omitempty"`
	SkipIf string `yaml:"skip_if,omitempty"`
	Verify string `yaml:"verify,omitempty"`
	Hooks  ItemHooks `yaml:"hooks,omitempty"`
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
	case i.Directory != "":
		return "directory"
	case i.Binary != "":
		return "binary"
	case i.Run != "":
		return "run"
	default:
		return "unknown"
	}
}

// PrimaryValue returns the primary field value used for item matching (e.g.
// when merging registry overrides).
func (i Item) PrimaryValue() string {
	switch i.Type() {
	case "package":
		return i.Package
	case "script":
		return i.Script
	case "setting":
		return i.Setting
	case "file":
		return i.File
	case "directory":
		return i.Directory
	case "binary":
		return i.Binary
	case "run":
		return i.Run
	default:
		return ""
	}
}

// EffectiveDirection returns the file/directory transfer direction, defaulting
// to "push".
func (i Item) EffectiveDirection() string {
	switch i.Direction {
	case "pull", "sync":
		return i.Direction
	default:
		return "push"
	}
}

// PlatformMap holds a per-OS value. It accepts two YAML forms:
//
//   - Scalar: a single string applied to all platforms.
//   - Mapping: per-OS keys (macos, windows, linux).
type PlatformMap struct {
	MacOS   string
	Windows string
	Linux   string
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

// IsZero reports whether all platform values are empty.
func (p PlatformMap) IsZero() bool {
	return p.MacOS == "" && p.Windows == "" && p.Linux == ""
}

// UnmarshalYAML implements yaml.Unmarshaler. It accepts both a scalar string
// (used for all platforms) and the standard macos/windows/linux mapping.
func (p *PlatformMap) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		p.MacOS = value.Value
		p.Windows = value.Value
		p.Linux = value.Value
		return nil
	case yaml.MappingNode:
		type raw struct {
			MacOS   string `yaml:"macos"`
			Windows string `yaml:"windows"`
			Linux   string `yaml:"linux"`
		}
		var m raw
		if err := value.Decode(&m); err != nil {
			return err
		}
		p.MacOS, p.Windows, p.Linux = m.MacOS, m.Windows, m.Linux
		return nil
	default:
		return fmt.Errorf("destination/source must be a string or macos/windows/linux mapping")
	}
}

// MarshalYAML implements yaml.Marshaler so round-trips work correctly.
func (p PlatformMap) MarshalYAML() (any, error) {
	// If all values are identical (set from a scalar), marshal back as scalar.
	if p.MacOS != "" && p.MacOS == p.Windows && p.MacOS == p.Linux {
		return p.MacOS, nil
	}
	return map[string]string{
		"macos":   p.MacOS,
		"windows": p.Windows,
		"linux":   p.Linux,
	}, nil
}

// Load reads and parses a config file. It accepts both the new mapping format
// (with a "modules" key) and the legacy bare-sequence format.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return Config{}, nil
	}

	doc := root.Content[0]
	var cfg Config

	switch doc.Kind {
	case yaml.MappingNode:
		if err := doc.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	case yaml.SequenceNode:
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
