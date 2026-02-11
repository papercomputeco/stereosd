// Package rootcmder defines the root cobra command for stereosd.
//
// The root command has no action of its own; it serves as the parent for all
// subcommands (run, version, etc.).
package rootcmder

import (
	"github.com/spf13/cobra"

	runcmder "github.com/papercomputeco/stereosd/cmd/run"
	versioncmder "github.com/papercomputeco/stereosd/cmd/version"
)

const rootLongDesc string = `stereosd is the StereOS daemon — the control plane bridge between the
host and StereOS.

It runs inside every StereOS instance and provides:
- Lifecycle signaling over virtio-vsock
- Shared directory mounting (virtio-fs / 9p)
- Secret injection to tmpfs-backed files
- Graceful shutdown coordination
- Unix socket IPC for agentd communication`

const rootShortDesc string = "StereOS daemon control plane"

// NewRootCmd creates the top-level stereosd command and registers all
// subcommands. This is the entrypoint for stereosd.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stereosd",
		Short: rootShortDesc,
		Long:  rootLongDesc,
	}

	cmd.AddCommand(runcmder.NewRunCmd())
	cmd.AddCommand(versioncmder.NewVersionCmd())

	return cmd
}
