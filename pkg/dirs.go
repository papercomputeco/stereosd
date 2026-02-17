// dirs.go handles creation of the runtime directory structure under /run/stereos.
//
// While systemd-tmpfiles (configured in stereosd.nix) creates the base directories,
// stereosd also ensures they exist at startup as a belt-and-suspenders measure.
package stereosd

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"strconv"
)

// RuntimeDirs defines the directory paths stereosd manages.
type RuntimeDirs struct {
	// Base is the root runtime directory.
	Base string
	// Secrets is where injected secrets are stored (tmpfs-backed).
	Secrets string
	// Config is the directory for configuration files (e.g., jcard.toml).
	Config string
}

// DefaultRuntimeDirs returns the default runtime directory paths.
func DefaultRuntimeDirs() RuntimeDirs {
	return RuntimeDirs{
		Base:    "/run/stereos",
		Secrets: SecretDir,
		Config:  ConfigDir,
	}
}

// EnsureRuntimeDirs creates the runtime directory structure with
// appropriate permissions. This is idempotent.
func EnsureRuntimeDirs(dirs RuntimeDirs) error {
	type dirSpec struct {
		path string
		mode os.FileMode
	}

	specs := []dirSpec{
		{dirs.Base, 0755},
		{dirs.Secrets, 0700},
		{dirs.Config, 0755},
	}

	for _, spec := range specs {
		if err := os.MkdirAll(spec.path, spec.mode); err != nil {
			return fmt.Errorf("create runtime dir %s: %w", spec.path, err)
		}
		// MkdirAll may not set the right mode if the dir already exists
		if err := os.Chmod(spec.path, spec.mode); err != nil {
			return fmt.Errorf("chmod runtime dir %s: %w", spec.path, err)
		}
	}

	// Set group ownership of the base directory to "admin" so admin users
	// can traverse it and access sockets. Non-fatal if the group doesn't exist.
	if err := chownToGroup(dirs.Base, AdminGroup); err != nil {
		log.Printf("dirs: warning: chown %s to group %s: %v", dirs.Base, AdminGroup, err)
	}

	log.Printf("dirs: runtime directories ready (%s)", dirs.Base)
	return nil
}

// chownToGroup sets the group ownership of a file to the named group,
// keeping the current owner unchanged. Returns an error if the group
// does not exist or the chown fails.
func chownToGroup(path, groupName string) error {
	grp, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup group %s: %w", groupName, err)
	}
	gid, err := strconv.Atoi(grp.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %s: %w", grp.Gid, err)
	}
	// -1 for uid means "don't change owner"
	if err := os.Chown(path, -1, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}
