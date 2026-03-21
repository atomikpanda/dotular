package registry

import (
	"testing"
)

func TestParseIndex(t *testing.T) {
	data := []byte(`
modules:
  - name: wezterm
    version: "1.0.0"
  - name: neovim
    version: "2.0.0"
`)
	entries, err := ParseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "wezterm" {
		t.Errorf("entries[0].Name = %q", entries[0].Name)
	}
	if entries[1].Version != "2.0.0" {
		t.Errorf("entries[1].Version = %q", entries[1].Version)
	}
}

func TestParseIndexEmpty(t *testing.T) {
	data := []byte(`modules: []`)
	entries, err := ParseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseIndexInvalid(t *testing.T) {
	_, err := ParseIndex([]byte("{{invalid"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestIndexURL(t *testing.T) {
	got := IndexURL()
	want := "https://raw.githubusercontent.com/atomikpanda/dotular/main/modules/index.yaml"
	if got != want {
		t.Errorf("IndexURL() = %q, want %q", got, want)
	}
}
