package ageutil

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func TestRepoPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"secrets.env", "secrets.env.age"},
		{"secrets.env.age", "secrets.env.age"},
		{".bashrc", ".bashrc.age"},
		{"path/to/file", "path/to/file.age"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := RepoPath(tt.input); got != tt.want {
				t.Errorf("RepoPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEncryptDecryptPassphrase(t *testing.T) {
	dir := t.TempDir()

	// Create a plaintext file.
	plain := filepath.Join(dir, "secret.txt")
	content := []byte("super secret data")
	if err := os.WriteFile(plain, content, 0o644); err != nil {
		t.Fatal(err)
	}

	key := &Key{Passphrase: "test-password-123"}

	// Encrypt.
	encrypted := filepath.Join(dir, "secret.txt.age")
	if err := key.EncryptFile(plain, encrypted); err != nil {
		t.Fatal(err)
	}

	// Verify encrypted file exists and differs from plaintext.
	encData, err := os.ReadFile(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if string(encData) == string(content) {
		t.Error("encrypted data should differ from plaintext")
	}

	// Decrypt.
	decrypted := filepath.Join(dir, "decrypted.txt")
	if err := key.DecryptFile(encrypted, decrypted); err != nil {
		t.Fatal(err)
	}

	decData, err := os.ReadFile(decrypted)
	if err != nil {
		t.Fatal(err)
	}
	if string(decData) != string(content) {
		t.Errorf("decrypted = %q, want %q", string(decData), string(content))
	}
}

func TestDecryptMissingFile(t *testing.T) {
	key := &Key{Passphrase: "test"}
	err := key.DecryptFile("/nonexistent/file.age", "/tmp/out")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestEncryptMissingFile(t *testing.T) {
	key := &Key{Passphrase: "test"}
	err := key.EncryptFile("/nonexistent/file", "/tmp/out.age")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestNoIdentityFile(t *testing.T) {
	key := &Key{}
	dir := t.TempDir()
	plain := filepath.Join(dir, "test.txt")
	os.WriteFile(plain, []byte("data"), 0o644)

	err := key.EncryptFile(plain, filepath.Join(dir, "test.age"))
	if err == nil {
		t.Error("expected error with no key configured")
	}
}

func TestEncryptDecryptWithIdentityFile(t *testing.T) {
	dir := t.TempDir()

	// Generate a new age identity.
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	// Write identity file.
	keyFile := filepath.Join(dir, "key.txt")
	os.WriteFile(keyFile, []byte(identity.String()+"\n"), 0o600)

	// Create plaintext.
	plain := filepath.Join(dir, "secret.txt")
	os.WriteFile(plain, []byte("identity-encrypted"), 0o644)

	key := &Key{IdentityFile: keyFile}

	// Encrypt.
	encrypted := filepath.Join(dir, "secret.txt.age")
	if err := key.EncryptFile(plain, encrypted); err != nil {
		t.Fatal(err)
	}

	// Decrypt.
	decrypted := filepath.Join(dir, "decrypted.txt")
	if err := key.DecryptFile(encrypted, decrypted); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(decrypted)
	if string(data) != "identity-encrypted" {
		t.Errorf("decrypted = %q", string(data))
	}
}

func TestParseIdentityFileInvalid(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "bad.txt")
	os.WriteFile(keyFile, []byte("not a valid key"), 0o600)

	key := &Key{IdentityFile: keyFile}
	plain := filepath.Join(dir, "test.txt")
	os.WriteFile(plain, []byte("data"), 0o644)

	err := key.EncryptFile(plain, filepath.Join(dir, "out.age"))
	if err == nil {
		t.Error("expected error for invalid identity file")
	}
}

func TestParseIdentityFileMissing(t *testing.T) {
	key := &Key{IdentityFile: "/nonexistent/key.txt"}
	plain := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(plain, []byte("data"), 0o644)

	err := key.EncryptFile(plain, filepath.Join(t.TempDir(), "out.age"))
	if err == nil {
		t.Error("expected error for missing identity file")
	}
}

func TestEncryptedFilePermissions(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "secret.txt")
	os.WriteFile(plain, []byte("data"), 0o644)

	key := &Key{Passphrase: "test"}
	encrypted := filepath.Join(dir, "secret.txt.age")
	if err := key.EncryptFile(plain, encrypted); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("encrypted file permissions = %o, want 0600", perm)
	}
}
