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
	// Official modules are maintained by Dotular and hosted at dotular.dev/modules.
	Official TrustLevel = iota
	// Community modules are published by third parties via dotular.dev/community.
	// They are not reviewed and should be treated as untrusted.
	Community
	// Private modules are hosted on arbitrary URLs or GitHub repositories.
	Private
)

func (t TrustLevel) String() string {
	switch t {
	case Official:
		return "official"
	case Community:
		return "community"
	default:
		return "private"
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

// Ref holds a parsed registry reference string (e.g. "dotular.dev/modules/neovim@1.0.0").
type Ref struct {
	Raw     string
	Host    string
	Path    string
	Version string
	Trust   TrustLevel
	FetchURL string
}

// ParseRef parses a registry reference string.
func ParseRef(raw string) Ref {
	name, version, _ := strings.Cut(raw, "@")
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
	case "dotular.dev":
		if strings.HasPrefix(path, "modules/") {
			// Official: https://dotular.dev/modules/<name>/<version>.yaml
			v := version
			if v == "" {
				v = "latest"
			}
			modName := strings.TrimPrefix(path, "modules/")
			url := "https://dotular.dev/modules/" + modName + "/" + v + ".yaml"
			return Official, url
		}
		if strings.HasPrefix(path, "community/") {
			url := "https://dotular.dev/" + path
			if version != "" {
				url += "@" + version
			}
			return Community, url
		}
		return Private, "https://" + host + "/" + path

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
		return Private, url

	default:
		// Fallback: treat as a direct HTTPS URL.
		url := "https://" + host + "/" + path
		if version != "" {
			url += "@" + version
		}
		return Private, url
	}
}
