// dirs.go handles creation of the runtime directory structure under /run/stereos.
//
// While systemd-tmpfiles (configured in stereosd.nix) creates the base directories,
// stereosd also ensures they exist at startup as a belt-and-suspenders measure.
package stereosd

import (
	"fmt"
	"log"
	"os"
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

	log.Printf("dirs: runtime directories ready (%s)", dirs.Base)
	return nil
}
