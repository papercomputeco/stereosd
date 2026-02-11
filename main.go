// Package main is the entry point for stereosd (StereOS daemon).
//
// stereosd is a lightweight daemon that runs inside every StereOS instance and
// provides the bridge between the host system (Masterblaster, Kubernetes
// kubevirt, cloud controllers, etc.) and StereOS as an out-of-band control
// plane.
//
// Responsibilities:
//   - Lifecycle signaling (boot status, readiness, health) over virtio-vsock
//   - Shared directory mounting (virtio-fs / 9p from jcard.toml [[shared]])
//   - Secret injection (from host over vsock -> tmpfs-backed files)
//   - Graceful shutdown coordination (before_down hooks, clean shutdown)
package main

import (
	"fmt"
	"os"

	rootcmder "github.com/papercomputeco/stereosd/cmd/root"
)

func main() {
	if err := rootcmder.NewRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "stereosd: %v\n", err)
		os.Exit(1)
	}
}
