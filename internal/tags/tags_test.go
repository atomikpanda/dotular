package tags

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMatches(t *testing.T) {
	tests := []struct {
		name    string
		machine []string
		only    []string
		exclude []string
		want    bool
	}{
		{"no constraints", []string{"darwin", "amd64"}, nil, nil, true},
		{"only match", []string{"darwin", "amd64"}, []string{"darwin"}, nil, true},
		{"only no match", []string{"linux", "amd64"}, []string{"darwin"}, nil, false},
		{"exclude match", []string{"darwin", "amd64"}, nil, []string{"darwin"}, false},
		{"exclude no match", []string{"linux", "amd64"}, nil, []string{"darwin"}, true},
		{"only and exclude both match", []string{"darwin", "work"}, []string{"darwin"}, []string{"work"}, false},
		{"only match exclude no match", []string{"darwin", "home"}, []string{"darwin"}, []string{"work"}, true},
		{"empty machine tags", []string{}, []string{"darwin"}, nil, false},
		{"empty machine no constraints", []string{}, nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.machine, tt.only, tt.exclude); got != tt.want {
				t.Errorf("Matches(%v, %v, %v) = %v, want %v", tt.machine, tt.only, tt.exclude, got, tt.want)
			}
		})
	}
}

func TestAutoDetect(t *testing.T) {
	detected := AutoDetect()
	if len(detected) < 2 {
		t.Fatalf("expected at least 2 tags, got %d", len(detected))
	}
	if detected[0] != runtime.GOOS {
		t.Errorf("first tag = %q, want %q", detected[0], runtime.GOOS)
	}
	if detected[1] != runtime.GOARCH {
		t.Errorf("second tag = %q, want %q", detected[1], runtime.GOARCH)
	}
}

func TestConfigPath(t *testing.T) {
	p := ConfigPath()
	if p == "" {
		t.Error("ConfigPath() should not be empty")
	}
	if filepath.Base(p) != "machine.yaml" {
		t.Errorf("ConfigPath() basename = %q", filepath.Base(p))
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Create temp dir and override ConfigPath by manipulating HOME.
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfg := &MachineConfig{Tags: []string{"darwin", "amd64", "work"}}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(loaded.Tags))
	}
	if loaded.Tags[0] != "darwin" {
		t.Errorf("tag 0 = %q", loaded.Tags[0])
	}
}

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tags) != 0 {
		t.Errorf("expected 0 tags for missing config, got %d", len(cfg.Tags))
	}
}

func TestEnsureInitialised(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := EnsureInitialised(); err != nil {
		t.Fatal(err)
	}

	// File should exist now.
	if _, err := os.Stat(ConfigPath()); err != nil {
		t.Errorf("expected machine.yaml to exist: %v", err)
	}

	// Second call should be no-op.
	if err := EnsureInitialised(); err != nil {
		t.Fatal(err)
	}
}

func TestAdd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Start with a config.
	Save(&MachineConfig{Tags: []string{"darwin"}})

	if err := Add("work"); err != nil {
		t.Fatal(err)
	}

	cfg, _ := Load()
	if len(cfg.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(cfg.Tags))
	}

	// Adding duplicate should be no-op.
	if err := Add("work"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = Load()
	if len(cfg.Tags) != 2 {
		t.Errorf("expected still 2 tags after duplicate add, got %d", len(cfg.Tags))
	}
}
