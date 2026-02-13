// shutdown.go implements graceful shutdown coordination.
//
// When the host sends a shutdown command (via "mb down"), stereosd:
//  1. Unmounts all shared directories
//  2. Syncs filesystems
//  3. Initiates system shutdown via systemctl poweroff
//
// agentd is an independent systemd service that manages its own lifecycle.
// When systemctl poweroff runs, systemd sends SIGTERM to all services
// (including agentd), allowing each to shut down gracefully within their
// configured TimeoutStopSec.
package stereosd

import (
	"context"
	"fmt"
	"log"
)

// ShutdownCoordinator manages the graceful shutdown sequence.
type ShutdownCoordinator struct {
	mounts    *MountManager
	lifecycle *LifecycleManager
	commander Commander
}

// NewShutdownCoordinator creates a new shutdown coordinator.
func NewShutdownCoordinator(
	mounts *MountManager,
	lifecycle *LifecycleManager,
	commander Commander,
) *ShutdownCoordinator {
	if commander == nil {
		commander = ExecCommander{}
	}
	return &ShutdownCoordinator{
		mounts:    mounts,
		lifecycle: lifecycle,
		commander: commander,
	}
}

// Execute runs the full graceful shutdown sequence.
func (sc *ShutdownCoordinator) Execute(ctx context.Context, payload *ShutdownPayload) error {
	reason := "host requested shutdown"
	if payload != nil && payload.Reason != "" {
		reason = payload.Reason
	}

	log.Printf("shutdown: initiating graceful shutdown (reason: %s)", reason)
	sc.lifecycle.Transition(StateShutdown, reason)

	// Step 1: Unmount shared directories
	log.Println("shutdown: unmounting shared directories")
	sc.mounts.UnmountAll()

	// Step 2: Sync filesystems
	log.Println("shutdown: syncing filesystems")
	if err := sc.commander.Run("sync"); err != nil {
		log.Printf("shutdown: sync warning: %v", err)
	}

	// Step 3: Initiate system shutdown. systemd will send SIGTERM to all
	// managed services (including agentd), allowing each to handle its
	// own graceful shutdown.
	log.Println("shutdown: initiating system poweroff")
	if err := sc.commander.Run("systemctl", "poweroff"); err != nil {
		return fmt.Errorf("shutdown: systemctl poweroff: %w", err)
	}

	return nil
}
