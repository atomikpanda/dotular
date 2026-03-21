package registry

import (
	"context"
	"fmt"

	"github.com/atomikpanda/dotular/internal/ui"
	"gopkg.in/yaml.v3"
)

// IndexEntry represents a single module in the registry index.
type IndexEntry struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

type indexFile struct {
	Modules []IndexEntry `yaml:"modules"`
}

// IndexURL returns the URL to the official registry index.
func IndexURL() string {
	return "https://raw.githubusercontent.com/" +
		"atomikpanda/dotular/main/modules/index.yaml"
}

// ParseIndex parses raw YAML bytes into a list of index entries.
func ParseIndex(data []byte) ([]IndexEntry, error) {
	var idx indexFile
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse registry index: %w", err)
	}
	return idx.Modules, nil
}

// FetchIndex downloads and parses the official registry index.
// It uses the same download infrastructure as module fetching.
func FetchIndex(ctx context.Context, u *ui.UI) ([]IndexEntry, error) {
	data, err := download(ctx, IndexURL())
	if err != nil {
		return nil, fmt.Errorf("fetch registry index: %w", err)
	}
	return ParseIndex(data)
}
