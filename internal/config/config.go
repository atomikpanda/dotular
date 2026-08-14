package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level document. It supports two on-disk formats:
//
//   - New (mapping): has a "modules" key and optional "age" key.
//   - Legacy (sequence): a bare list of modules (no global settings).
type Config struct {
	Age     *AgeConfig `yaml:"age,omitempty"`
	Modules []Module   `yaml:"modules,omitempty"`
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
	From     string         `yaml:"from,omitempty"`     // e.g. "github.com/atomikpanda/dotular/modules/neovim@main"
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
	Source    PlatformMap `yaml:"source,omitempty"`     // download URL per OS
	InstallTo string      `yaml:"install_to,omitempty"` // destination directory

	// --- run ---
	// Run executes an inline shell command. After is informational: it names
	// the item type this run step logically depends on (ordering is determined
	// by declaration order in the items list).
	Run   string `yaml:"run,omitempty"`
	After string `yaml:"after,omitempty"`

	// Via is parsed on every item type but only read for two: the package
	// manager for `package`, and "remote"/"local" for `script`. It is silently
	// ignored on the other five.
	Via string `yaml:"via,omitempty"`

	// --- shared: honoured on every item type ---
	SkipIf string    `yaml:"skip_if,omitempty"`
	Verify string    `yaml:"verify,omitempty"`
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

// DefaultDirection is the file/directory transfer direction assumed when an
// item does not set one.
const DefaultDirection = "push"

// EffectiveDirection returns the file/directory transfer direction, defaulting
// to DefaultDirection.
func (i Item) EffectiveDirection() string {
	switch i.Direction {
	case "pull", "sync":
		return i.Direction
	default:
		return DefaultDirection
	}
}

// ItemValidationOptions controls validation for item data at different
// configuration boundaries.
type ItemValidationOptions struct {
	AllowDirectionTemplates bool
}

// ValidateDirection checks a non-empty explicit transfer direction.
func ValidateDirection(direction string) error {
	switch direction {
	case "push", "pull", "sync":
		return nil
	default:
		return fmt.Errorf("direction %q must be push, pull, or sync", direction)
	}
}

// ValidateItems checks primary-field cardinality and explicit directions in
// declaration order.
func ValidateItems(items []Item, opts ItemValidationOptions) error {
	for itemIndex, item := range items {
		fields := [...]struct{ name, value string }{
			{"package", item.Package},
			{"script", item.Script},
			{"setting", item.Setting},
			{"file", item.File},
			{"directory", item.Directory},
			{"binary", item.Binary},
			{"run", item.Run},
		}
		names := make([]string, 0, 2)
		for _, field := range fields {
			if field.value != "" {
				names = append(names, field.name)
			}
		}
		if len(names) != 1 {
			found := "none"
			if len(names) != 0 {
				found = strings.Join(names, ", ")
			}
			return fmt.Errorf("item %d: expected exactly one primary field; found %s", itemIndex+1, found)
		}
		if item.Direction != "" &&
			!(opts.AllowDirectionTemplates && strings.Contains(item.Direction, "{{")) {
			if err := ValidateDirection(item.Direction); err != nil {
				return fmt.Errorf("item %d: %w", itemIndex+1, err)
			}
		}
	}
	return nil
}

// Validate checks local modules and registry overrides in declaration order.
func (c Config) Validate() error {
	for moduleIndex, mod := range c.Modules {
		module := fmt.Sprintf("module %d", moduleIndex+1)
		if mod.Name != "" {
			module += fmt.Sprintf(" (%q)", mod.Name)
		}
		if mod.From != "" && len(mod.Items) != 0 {
			return fmt.Errorf("%s: from and items are mutually exclusive", module)
		}
		if err := ValidateItems(mod.Items, ItemValidationOptions{}); err != nil {
			return fmt.Errorf("%s: items: %w", module, err)
		}
		if err := ValidateItems(mod.Override, ItemValidationOptions{}); err != nil {
			return fmt.Errorf("%s: override: %w", module, err)
		}
	}
	return nil
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
		// Walk key/value pairs manually so that YAML null values (bare ~)
		// are preserved as the literal string "~" — important for paths.
		var seenMacOS, seenWindows, seenLinux bool
		for i := 0; i+1 < len(value.Content); i += 2 {
			keyNode := value.Content[i]
			key := keyNode.Value
			val := value.Content[i+1]
			v := val.Value
			// YAML reads "~" as null, but as a path it means the home
			// directory, so keep it. Every other null spelling ("null",
			// "Null", a bare key) means "unset" — take the empty string
			// rather than the literal source text.
			if val.Tag == "!!null" && v != "~" {
				v = ""
			}
			switch key {
			case "macos":
				if seenMacOS {
					return fmt.Errorf("line %d: duplicate platform key %q", keyNode.Line, key)
				}
				seenMacOS = true
				p.MacOS = v
			case "windows":
				if seenWindows {
					return fmt.Errorf("line %d: duplicate platform key %q", keyNode.Line, key)
				}
				seenWindows = true
				p.Windows = v
			case "linux":
				if seenLinux {
					return fmt.Errorf("line %d: duplicate platform key %q", keyNode.Line, key)
				}
				seenLinux = true
				p.Linux = v
			default:
				return fmt.Errorf("line %d: unknown platform key %q (want macos, linux, or windows)", keyNode.Line, key)
			}
		}
		return nil
	default:
		// The field name is not reachable from here, and naming both
		// "destination/source" misreports whichever one is actually malformed.
		// yaml.v3 adds no position to errors from a custom unmarshaler, so
		// point at the offending node directly instead.
		return fmt.Errorf("line %d: must be a string or a macos/windows/linux mapping", value.Line)
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

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	switch doc.Kind {
	case yaml.MappingNode:
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	case yaml.SequenceNode:
		if err := decoder.Decode(&cfg.Modules); err != nil {
			return Config{}, fmt.Errorf("parse config (legacy format): %w", err)
		}
	default:
		return Config{}, fmt.Errorf("config root must be a mapping with a \"modules\" key, or a bare sequence of modules; got %s", nodeKindName(doc.Kind))
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

// nodeKindName names the yaml.Kind values that can reach Load's default arm,
// so a malformed config reports "a scalar" rather than a raw kind integer.
// Mapping and sequence roots are handled before the arm that calls this, and
// an empty document is rejected earlier.
func nodeKindName(k yaml.Kind) string {
	switch k {
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	default:
		return fmt.Sprintf("kind %d", k)
	}
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

// Save marshals the config and writes it to path using the mapping format.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
