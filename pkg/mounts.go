// mounts.go implements shared directory mounting for virtio-fs and 9p shares.
//
// The host defines shared directories in jcard.toml [[shared]] entries.
// When stereosd receives a mount message, it:
//  1. Creates the guest mount point directory if needed
//  2. Calls mount(2) with the appropriate filesystem type and options
//  3. Sets ownership to the agent user
//
// Supported filesystem types:
//   - "virtiofs" — virtio-fs (preferred, better performance)
//   - "9p"       — Plan 9 filesystem (fallback, wider compatibility)
package stereosd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MountManager handles mounting and unmounting shared directories.
type MountManager struct {
	// mounts tracks active mounts for cleanup during shutdown
	mounts []MountPayload

	// commander abstracts system commands for testing
	commander Commander
}

// Commander abstracts system command execution for testability.
type Commander interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
}

// ExecCommander uses os/exec to run real system commands.
type ExecCommander struct{}

// Run executes a command and returns any error.
func (ExecCommander) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Output executes a command and returns its combined output.
func (ExecCommander) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// NewMountManager creates a new mount manager.
func NewMountManager(commander Commander) *MountManager {
	if commander == nil {
		commander = ExecCommander{}
	}
	return &MountManager{
		commander: commander,
	}
}

// Mount mounts a shared directory based on the mount payload.
func (mm *MountManager) Mount(m *MountPayload) error {
	if m.Tag == "" {
		return fmt.Errorf("mount tag cannot be empty")
	}
	if m.GuestPath == "" {
		return fmt.Errorf("mount guest_path cannot be empty")
	}
	if m.FSType == "" {
		return fmt.Errorf("mount fs_type cannot be empty")
	}

	// Validate filesystem type
	switch m.FSType {
	case "virtiofs", "9p":
		// OK
	default:
		return fmt.Errorf("unsupported filesystem type: %q (must be virtiofs or 9p)", m.FSType)
	}

	// Sanitize the guest path — must be absolute
	guestPath := filepath.Clean(m.GuestPath)
	if !filepath.IsAbs(guestPath) {
		return fmt.Errorf("guest_path must be absolute: %q", m.GuestPath)
	}

	// Prevent mounting over critical system directories
	for _, blocked := range []string{"/", "/nix", "/etc", "/bin", "/boot", "/dev", "/proc", "/sys", "/run"} {
		if guestPath == blocked || strings.HasPrefix(guestPath, blocked+"/") {
			return fmt.Errorf("cannot mount over system directory: %q", guestPath)
		}
	}

	// Create mount point directory if needed
	if err := os.MkdirAll(guestPath, 0755); err != nil {
		return fmt.Errorf("create mount point %q: %w", guestPath, err)
	}

	// Build mount options
	var opts []string
	if m.FSType == "9p" {
		opts = append(opts, "trans=virtio,version=9p2000.L")
	}
	if m.ReadOnly {
		opts = append(opts, "ro")
	}

	// Construct mount command
	args := []string{"-t", m.FSType}
	if len(opts) > 0 {
		args = append(args, "-o", strings.Join(opts, ","))
	}
	args = append(args, m.Tag, guestPath)

	log.Printf("mounts: mounting %s (%s) at %s", m.Tag, m.FSType, guestPath)

	if err := mm.commander.Run("mount", args...); err != nil {
		return fmt.Errorf("mount %s at %s: %w", m.Tag, guestPath, err)
	}

	// Set ownership to agent user (best effort — the user may not exist
	// if this is called very early, but systemd-tmpfiles should have
	// already created the user).
	mm.commander.Run("chown", "agent:agent", guestPath)

	// Track the mount for cleanup
	mm.mounts = append(mm.mounts, *m)

	log.Printf("mounts: successfully mounted %s at %s", m.Tag, guestPath)
	return nil
}

// UnmountAll unmounts all tracked shared directories in reverse order.
func (mm *MountManager) UnmountAll() {
	for i := len(mm.mounts) - 1; i >= 0; i-- {
		m := mm.mounts[i]
		guestPath := filepath.Clean(m.GuestPath)
		log.Printf("mounts: unmounting %s", guestPath)

		if err := mm.commander.Run("umount", guestPath); err != nil {
			log.Printf("mounts: warning: failed to unmount %s: %v", guestPath, err)
		}
	}
	mm.mounts = nil
}

// ActiveMounts returns a copy of the currently active mount list.
func (mm *MountManager) ActiveMounts() []MountPayload {
	result := make([]MountPayload, len(mm.mounts))
	copy(result, mm.mounts)
	return result
}
