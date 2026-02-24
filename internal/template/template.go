// Package template renders Go template strings against a parameter map.
// Registry modules use {{ .param }} syntax throughout their item fields.
package template

import (
	"bytes"
	"fmt"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/atomikpanda/dotular/internal/config"
)

// Render executes the Go template string s with params as the data object.
func Render(s string, params map[string]any) (string, error) {
	t, err := template.New("").Option("missingkey=zero").Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", s, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("execute template %q: %w", s, err)
	}
	return buf.String(), nil
}

// RenderItem renders all string fields of item that may contain Go template
// expressions. It works by marshalling the item to YAML, rendering the
// resulting string as a template, then unmarshalling back. This approach
// automatically covers every string field without explicit enumeration.
func RenderItem(item config.Item, params map[string]any) (config.Item, error) {
	if len(params) == 0 {
		return item, nil
	}

	data, err := yaml.Marshal(item)
	if err != nil {
		return item, fmt.Errorf("marshal item for template rendering: %w", err)
	}

	rendered, err := Render(string(data), params)
	if err != nil {
		return item, fmt.Errorf("render item: %w", err)
	}

	var result config.Item
	if err := yaml.Unmarshal([]byte(rendered), &result); err != nil {
		return item, fmt.Errorf("unmarshal rendered item: %w", err)
	}
	return result, nil
}
