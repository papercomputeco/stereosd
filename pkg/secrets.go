// secrets.go implements secret injection — receiving secrets from the host
// over vsock and writing them to tmpfs-backed files.
//
// Secrets are written to /run/stereos/secrets/<name> with restricted
// permissions (0600 by default). The /run filesystem is tmpfs, so secrets
// are never written to persistent disk.
//
// After writing, the secret value is cleared from memory.
package stereosd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// SecretManager handles writing and managing secrets on the tmpfs.
type SecretManager struct {
	secretDir string
}

// NewSecretManager creates a new secret manager rooted at the given directory.
func NewSecretManager(secretDir string) *SecretManager {
	return &SecretManager{
		secretDir: secretDir,
	}
}

// Inject writes a secret to the tmpfs-backed secret directory.
// The file is created with the specified mode (default 0600).
// The secret value in the payload is cleared after writing.
func (sm *SecretManager) Inject(secret *SecretPayload) error {
	if secret.Name == "" {
		return fmt.Errorf("secret name cannot be empty")
	}

	// Sanitize the name to prevent path traversal
	cleanName := filepath.Base(secret.Name)
	if cleanName != secret.Name || strings.Contains(cleanName, "..") {
		return fmt.Errorf("invalid secret name: %q (must be a simple filename)", secret.Name)
	}

	targetPath := filepath.Join(sm.secretDir, cleanName)

	mode := os.FileMode(secret.Mode)
	if mode == 0 {
		mode = 0600
	}

	// Write the secret file atomically: write to temp, then rename
	tmpPath := targetPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(secret.Value), mode); err != nil {
		return fmt.Errorf("write secret %q: %w", cleanName, err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename secret %q: %w", cleanName, err)
	}

	// Clear the value from the payload to minimize time in memory
	secret.Value = ""

	log.Printf("secrets: injected %q (%s)", cleanName, mode)
	return nil
}

// List returns the names of all injected secrets.
func (sm *SecretManager) List() ([]string, error) {
	entries, err := os.ReadDir(sm.secretDir)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasSuffix(entry.Name(), ".tmp") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// Remove deletes a secret from the tmpfs store.
func (sm *SecretManager) Remove(name string) error {
	cleanName := filepath.Base(name)
	if cleanName != name {
		return fmt.Errorf("invalid secret name: %q", name)
	}

	targetPath := filepath.Join(sm.secretDir, cleanName)
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove secret %q: %w", cleanName, err)
	}

	log.Printf("secrets: removed %q", cleanName)
	return nil
}

// SecretDir returns the directory where secrets are stored.
func (sm *SecretManager) SecretDir() string {
	return sm.secretDir
}
