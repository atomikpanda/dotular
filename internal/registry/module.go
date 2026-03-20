// Package registry fetches, caches, verifies, and resolves remote Dotular
// module definitions.
package registry

import (
	"strings"

	"github.com/atomikpanda/dotular/internal/config"
)

// TrustLevel classifies the source of a registry module.
type TrustLevel int

const (
	// Official modules are from the DefaultRegistry (github.com/atomikpanda/dotular).
	Official TrustLevel = iota
	// GitHub modules are from other github.com repositories.
	GitHub
	// External modules are from arbitrary URLs.
	External
)

func (t TrustLevel) String() string {
	switch t {
	case Official:
		return "official"
	case GitHub:
		return "github"
	default:
		return "external"
	}
}

// Param defines a single parameter accepted by a registry module.
type Param struct {
	Default     any    `yaml:"default,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// RemoteModule is the on-disk format for a published registry module.
type RemoteModule struct {
	Name    string            `yaml:"name"`
	Version string            `yaml:"version,omitempty"`
	Params  map[string]Param  `yaml:"params,omitempty"`
	Items   []config.Item     `yaml:"items"`
}

// Ref holds a parsed registry reference string (e.g. "github.com/atomikpanda/dotular/modules/neovim@main").
type Ref struct {
	Raw     string
	Host    string
	Path    string
	Version string
	Trust   TrustLevel
	FetchURL string
}

// DefaultRegistry is the GitHub repository used to expand shorthand module
// references (e.g. "wezterm" → "github.com/atomikpanda/dotular/modules/wezterm@main").
const DefaultRegistry = "github.com/atomikpanda/dotular"

// ParseRef parses a registry reference string. Bare names without a host
// (e.g. "wezterm") are expanded against the DefaultRegistry.
func ParseRef(raw string) Ref {
	name, version, _ := strings.Cut(raw, "@")
	// Shorthand: bare name with no slashes → default registry module.
	if !strings.Contains(name, "/") {
		expanded := DefaultRegistry + "/modules/" + name
		if version != "" {
			expanded += "@" + version
		} else {
			expanded += "@main"
		}
		return ParseRef(expanded)
	}
	parts := strings.SplitN(name, "/", 2)
	host := parts[0]
	path := ""
	if len(parts) > 1 {
		path = parts[1]
	}

	trust, fetchURL := resolveTrustAndURL(host, path, version)
	return Ref{
		Raw:      raw,
		Host:     host,
		Path:     path,
		Version:  version,
		Trust:    trust,
		FetchURL: fetchURL,
	}
}

func resolveTrustAndURL(host, path, version string) (TrustLevel, string) {
	switch host {
	case "github.com":
		// github.com/user/repo@ref → raw file URL.
		// Supports two forms:
		//   github.com/user/repo@ref           → .../user/repo/<ref>/dotular-module.yaml
		//   github.com/user/repo/sub/path@ref  → .../user/repo/<ref>/sub/path.yaml
		ref := version
		if ref == "" {
			ref = "main"
		}
		parts := strings.SplitN(path, "/", 3)
		var url string
		if len(parts) <= 2 {
			// Simple: github.com/user/repo
			url = "https://raw.githubusercontent.com/" + path + "/" + ref + "/dotular-module.yaml"
		} else {
			// Extended: github.com/user/repo/sub/path
			repoPath := parts[0] + "/" + parts[1]
			subPath := parts[2]
			url = "https://raw.githubusercontent.com/" + repoPath + "/" + ref + "/" + subPath + ".yaml"
		}

		// Determine trust: official if from the DefaultRegistry repo.
		trust := GitHub
		if strings.HasPrefix(path, "atomikpanda/dotular") {
			trust = Official
		}
		return trust, url

	default:
		// Fallback: treat as a direct HTTPS URL.
		url := "https://" + host + "/" + path
		if version != "" {
			url += "@" + version
		}
		return External, url
	}
}
