// Package ageutil wraps filippo.io/age for encrypting and decrypting dotfiles.
package ageutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
)

// Key holds the credential needed to encrypt and decrypt age files.
// Exactly one of IdentityFile or Passphrase should be non-empty.
type Key struct {
	IdentityFile string // path to an age identity file (secret key)
	Passphrase   string // scrypt passphrase (used when IdentityFile is empty)
}

// EncryptFile reads src (plaintext), encrypts it with k, and writes the result to dst.
// The encrypted file uses age's binary format.
func (k *Key) EncryptFile(src, dst string) error {
	plaintext, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read plaintext: %w", err)
	}

	recipients, err := k.recipients()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return fmt.Errorf("write ciphertext: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finalise ciphertext: %w", err)
	}

	return os.WriteFile(dst, buf.Bytes(), 0o600)
}

// DecryptFile reads src (age-encrypted), decrypts it with k, and writes
// the plaintext to dst.
func (k *Key) DecryptFile(src, dst string) error {
	ciphertext, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read ciphertext: %w", err)
	}

	identities, err := k.identities()
	if err != nil {
		return err
	}

	r, err := age.Decrypt(bytes.NewReader(ciphertext), identities...)
	if err != nil {
		return fmt.Errorf("age decrypt: %w", err)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read plaintext: %w", err)
	}

	return os.WriteFile(dst, plaintext, 0o600)
}

// recipients returns the age recipients for encryption.
func (k *Key) recipients() ([]age.Recipient, error) {
	if k.Passphrase != "" {
		r, err := age.NewScryptRecipient(k.Passphrase)
		if err != nil {
			return nil, fmt.Errorf("create scrypt recipient: %w", err)
		}
		return []age.Recipient{r}, nil
	}

	identities, err := k.parseIdentityFile()
	if err != nil {
		return nil, err
	}
	var recipients []age.Recipient
	for _, id := range identities {
		if x, ok := id.(*age.X25519Identity); ok {
			recipients = append(recipients, x.Recipient())
		}
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no X25519 identities found in %s", k.IdentityFile)
	}
	return recipients, nil
}

// identities returns the age identities for decryption.
func (k *Key) identities() ([]age.Identity, error) {
	if k.Passphrase != "" {
		id, err := age.NewScryptIdentity(k.Passphrase)
		if err != nil {
			return nil, fmt.Errorf("create scrypt identity: %w", err)
		}
		return []age.Identity{id}, nil
	}
	return k.parseIdentityFile()
}

func (k *Key) parseIdentityFile() ([]age.Identity, error) {
	if k.IdentityFile == "" {
		return nil, fmt.Errorf("no age identity file configured; set age.identity in dotular.yaml or DOTULAR_AGE_IDENTITY")
	}
	f, err := os.Open(k.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("open identity file: %w", err)
	}
	defer f.Close()

	identities, err := age.ParseIdentities(f)
	if err != nil {
		return nil, fmt.Errorf("parse identities: %w", err)
	}
	return identities, nil
}

// RepoPath returns the on-disk repo path for an encrypted file item.
// If the source path does not already end in ".age" it appends it.
func RepoPath(src string) string {
	if strings.HasSuffix(src, ".age") {
		return src
	}
	return src + ".age"
}
