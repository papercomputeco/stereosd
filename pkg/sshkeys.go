// sshkeys.go implements SSH public key injection — receiving a public key
// from the host over vsock and writing it to a user's authorized_keys file.
//
// This enables ephemeral per-sandbox SSH authentication: the host generates
// a fresh Ed25519 keypair per sandbox lifecycle, injects the public key here,
// and uses the private key for `mb ssh`. No pre-baked credentials exist in
// the distributable image.
package stereosd

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// knownKeyPrefixes are the recognized SSH public key type prefixes.
// A valid public key line must start with one of these.
var knownKeyPrefixes = []string{
	"ssh-ed25519",
	"ssh-rsa",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
	"sk-ssh-ed25519@openssh.com",
	"sk-ecdsa-sha2-nistp256@openssh.com",
}

// UserLookupFunc abstracts os/user.Lookup for testing.
type UserLookupFunc func(username string) (*user.User, error)

// SSHKeyManager handles writing SSH public keys to users' authorized_keys files.
type SSHKeyManager struct {
	// userLookup resolves usernames to user info. Defaults to user.Lookup.
	userLookup UserLookupFunc
}

// NewSSHKeyManager creates a new SSHKeyManager that uses os/user.Lookup
// for user resolution.
func NewSSHKeyManager() *SSHKeyManager {
	return &SSHKeyManager{
		userLookup: user.Lookup,
	}
}

// newSSHKeyManagerWithLookup creates an SSHKeyManager with a custom user
// lookup function. This is used in tests to avoid needing real system users.
func newSSHKeyManagerWithLookup(lookup UserLookupFunc) *SSHKeyManager {
	return &SSHKeyManager{
		userLookup: lookup,
	}
}

// InjectAuthorizedKey writes the public key from the payload to the target
// user's ~/.ssh/authorized_keys file. It:
//  1. Validates that the user and public key fields are non-empty
//  2. Validates the public key format (must start with a recognized key type)
//  3. Resolves the user's home directory via the system user database
//  4. Creates ~/.ssh/ if it doesn't exist (mode 0700, owned by the user)
//  5. Writes the public key to ~/.ssh/authorized_keys (mode 0600, owned by the user)
func (m *SSHKeyManager) InjectAuthorizedKey(payload *SSHKeyPayload) error {
	if payload.User == "" {
		return fmt.Errorf("user cannot be empty")
	}
	if payload.PublicKey == "" {
		return fmt.Errorf("public_key cannot be empty")
	}

	// Validate that the key starts with a recognized type prefix.
	if !isValidSSHPublicKey(payload.PublicKey) {
		return fmt.Errorf("invalid SSH public key format: must start with a recognized key type (e.g., ssh-ed25519)")
	}

	// Resolve the user to get home directory, uid, gid.
	u, err := m.userLookup(payload.User)
	if err != nil {
		return fmt.Errorf("lookup user %q: %w", payload.User, err)
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("parse uid %q: %w", u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %q: %w", u.Gid, err)
	}

	// Ensure ~/.ssh/ exists with correct ownership and permissions.
	sshDir := filepath.Join(u.HomeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("create %s: %w", sshDir, err)
	}
	if err := os.Chmod(sshDir, 0700); err != nil {
		return fmt.Errorf("chmod %s: %w", sshDir, err)
	}
	if err := os.Chown(sshDir, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", sshDir, err)
	}

	// Write the authorized_keys file atomically: write to .tmp, then rename.
	authorizedKeysPath := filepath.Join(sshDir, "authorized_keys")
	tmpPath := authorizedKeysPath + ".tmp"

	// Ensure the key ends with a newline.
	keyContent := strings.TrimRight(payload.PublicKey, "\n") + "\n"

	if err := os.WriteFile(tmpPath, []byte(keyContent), 0600); err != nil {
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := os.Chown(tmpPath, uid, gid); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chown %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, authorizedKeysPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s: %w", authorizedKeysPath, err)
	}

	log.Printf("sshkeys: injected authorized key for user %q (%s)", payload.User, authorizedKeysPath)
	return nil
}

// isValidSSHPublicKey checks whether the key string starts with a recognized
// SSH public key type prefix.
func isValidSSHPublicKey(key string) bool {
	for _, prefix := range knownKeyPrefixes {
		if strings.HasPrefix(key, prefix+" ") {
			return true
		}
	}
	return false
}
