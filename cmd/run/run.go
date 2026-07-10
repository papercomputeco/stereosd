// Package runcmder implements the "run" subcommand which starts the stereosd
// daemon.
//
// This is the primary command — it initializes all subsystems (vsock, IPC,
// lifecycle, secrets, mounts) and blocks until shutdown.
package runcmder

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	stereosd "github.com/papercomputeco/stereosd/pkg"
)

const runLongDesc string = `Start the stereosd daemon.

Initializes the control plane: creates runtime directories, opens the
virtio-vsock listener for host communication, starts the HTTP API server,
begins polling agentd for agent status, and blocks until a shutdown signal
(SIGINT, SIGTERM) or a host-initiated shutdown command is received.

Examples:
  stereosd run`

const runShortDesc string = "Start the stereosd daemon"

var (
	listenMode   string
	poweroffMode string
)

// NewRunCmd creates the "run" subcommand.
func NewRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: runShortDesc,
		Long:  runLongDesc,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDaemon()
		},
	}

	cmd.Flags().StringVar(&listenMode, "listen-mode", "auto",
		`Control plane listener mode: "vsock", "tcp", or "auto" (try vsock, fall back to tcp)`)
	cmd.Flags().StringVar(&poweroffMode, "poweroff", "systemctl",
		`Shutdown power-off strategy: "systemctl" (systemd) or "signal-init" (SIGTERM PID 1, for capstan)`)

	return cmd
}

func runDaemon() error {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("stereosd: starting StereOS daemon")

	if err := stereosd.ValidatePoweroffMode(poweroffMode); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	config := stereosd.DefaultConfig()
	config.ListenMode = listenMode
	config.PoweroffMode = poweroffMode

	daemon := stereosd.NewDaemonWithConfig(config)
	if err := daemon.Run(ctx); err != nil {
		return err
	}

	log.Println("stereosd: shutdown complete")
	return nil
}
