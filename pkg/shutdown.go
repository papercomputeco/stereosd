// shutdown.go implements graceful shutdown coordination.
//
// When the host sends a shutdown command (via "mb down"), stereosd:
//  1. Signals agentd to stop all agent harnesses gracefully
//  2. Waits for agentd to confirm agents have stopped (or timeout)
//  3. Unmounts all shared directories
//  4. Syncs filesystems
//  5. Initiates system shutdown
package stereosd

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	// DefaultGracePeriod is the default time to wait for agents to stop
	// before forcing shutdown.
	DefaultGracePeriod = 30 * time.Second
)

// ShutdownCoordinator manages the graceful shutdown sequence.
type ShutdownCoordinator struct {
	ipc       *IPCServer
	mounts    *MountManager
	lifecycle *LifecycleManager
	commander Commander

	// agentsStopped is closed when agentd confirms all agents have stopped.
	agentsStopped chan struct{}
}

// NewShutdownCoordinator creates a new shutdown coordinator.
func NewShutdownCoordinator(
	ipc *IPCServer,
	mounts *MountManager,
	lifecycle *LifecycleManager,
	commander Commander,
) *ShutdownCoordinator {
	if commander == nil {
		commander = ExecCommander{}
	}
	return &ShutdownCoordinator{
		ipc:           ipc,
		mounts:        mounts,
		lifecycle:     lifecycle,
		commander:     commander,
		agentsStopped: make(chan struct{}),
	}
}

// NotifyAgentsStopped should be called when agentd confirms all agents
// have stopped (in response to a stop_agents command).
func (sc *ShutdownCoordinator) NotifyAgentsStopped() {
	select {
	case sc.agentsStopped <- struct{}{}:
	default:
	}
}

// Execute runs the full graceful shutdown sequence.
func (sc *ShutdownCoordinator) Execute(ctx context.Context, payload *ShutdownPayload) error {
	reason := "host requested shutdown"
	if payload != nil && payload.Reason != "" {
		reason = payload.Reason
	}

	gracePeriod := DefaultGracePeriod
	if payload != nil && payload.GracePeriodSec > 0 {
		gracePeriod = time.Duration(payload.GracePeriodSec) * time.Second
	}

	log.Printf("shutdown: initiating graceful shutdown (reason: %s, grace: %s)", reason, gracePeriod)
	sc.lifecycle.Transition(StateShutdown, reason)

	// Step 1: Signal agentd to stop agents
	if sc.ipc.IsAgentdConnected() {
		log.Println("shutdown: requesting agentd stop all agents")
		env, err := NewEnvelope(MsgStopAgents, nil)
		if err != nil {
			log.Printf("shutdown: failed to create stop_agents message: %v", err)
		} else if err := sc.ipc.Send(env); err != nil {
			log.Printf("shutdown: failed to send stop_agents to agentd: %v", err)
		} else {
			// Step 2: Wait for agents to stop
			log.Printf("shutdown: waiting up to %s for agents to stop", gracePeriod)
			select {
			case <-sc.agentsStopped:
				log.Println("shutdown: all agents stopped")
			case <-time.After(gracePeriod):
				log.Println("shutdown: grace period expired, proceeding with shutdown")
			}
		}
	} else {
		log.Println("shutdown: agentd not connected, skipping agent stop")
	}

	// Step 3: Unmount shared directories
	log.Println("shutdown: unmounting shared directories")
	sc.mounts.UnmountAll()

	// Step 4: Sync filesystems
	log.Println("shutdown: syncing filesystems")
	if err := sc.commander.Run("sync"); err != nil {
		log.Printf("shutdown: sync warning: %v", err)
	}

	// Step 5: Initiate system shutdown
	log.Println("shutdown: initiating system poweroff")
	if err := sc.commander.Run("systemctl", "poweroff"); err != nil {
		return fmt.Errorf("shutdown: systemctl poweroff: %w", err)
	}

	return nil
}
