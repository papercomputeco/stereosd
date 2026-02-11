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
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/papercomputeco/stereosd/pkg"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("stereosd: starting StereOS daemon")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	daemon := stereosd.NewDaemon()
	if err := daemon.Run(ctx); err != nil {
		log.Fatalf("stereosd: fatal: %v", err)
		os.Exit(1)
	}

	log.Println("stereosd: shutdown complete")
}
