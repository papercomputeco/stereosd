// shutdown.go implements graceful shutdown coordination.
//
// When the host sends a shutdown command (via "mb down"), stereosd:
//  1. Unmounts all shared directories
//  2. Syncs filesystems
//  3. Powers the machine off using the configured strategy
//
// The power-off strategy depends on the init system:
//   - Under the full systemd-based stereOS image, `systemctl poweroff` lets
//     systemd SIGTERM every service (including agentd) for graceful shutdown.
//   - Under a minimal init (capstan as PID 1), there is no systemd; instead we
//     SIGTERM PID 1 and let init stop the supervised services and call
//     reboot(2) itself.
package stereosd

import (
	"context"
	"fmt"
	"log"
	"syscall"
)

// Poweroff strategy names (Config.PoweroffMode).
const (
	// PoweroffSystemctl powers off via `systemctl poweroff`. Default; correct
	// under the systemd-based stereOS image.
	PoweroffSystemctl = "systemctl"
	// PoweroffSignalInit asks PID 1 to shut down by sending it SIGTERM. Use
	// under a minimal init (capstan) that owns the orderly stop + power-off.
	PoweroffSignalInit = "signal-init"
)

// poweroffFunc performs the final power-off step of a shutdown.
type poweroffFunc func(Commander, func() error) error

func systemctlPoweroff(c Commander, _ func() error) error {
	return c.Run("systemctl", "poweroff")
}

func killPID1() error {
	return syscall.Kill(1, syscall.SIGTERM)
}

func signalInitPoweroff(_ Commander, killPID1 func() error) error {
	// Under a minimal init (capstan as PID 1), SIGTERM tells init to stop the
	// supervised services and reboot(RB_POWER_OFF). No command is run.
	return killPID1()
}

func poweroffFor(mode string) poweroffFunc {
	switch mode {
	case "", PoweroffSystemctl:
		return systemctlPoweroff
	case PoweroffSignalInit:
		return signalInitPoweroff
	default:
		return func(Commander, func() error) error {
			return ValidatePoweroffMode(mode)
		}
	}
}

// ValidatePoweroffMode returns an error when mode is not a supported power-off
// strategy.
func ValidatePoweroffMode(mode string) error {
	switch mode {
	case "", PoweroffSystemctl, PoweroffSignalInit:
		return nil
	default:
		return fmt.Errorf("unknown poweroff mode %q (valid: %q, %q)", mode, PoweroffSystemctl, PoweroffSignalInit)
	}
}

// ShutdownCoordinator manages the graceful shutdown sequence.
type ShutdownCoordinator struct {
	mounts    *MountManager
	lifecycle *LifecycleManager
	commander Commander
	poweroff  poweroffFunc
	killPID1  func() error
}

// NewShutdownCoordinator creates a new shutdown coordinator. poweroffMode
// selects the final power-off step (see the Poweroff* constants); an empty or
// unknown value makes shutdown return a power-off error.
func NewShutdownCoordinator(
	mounts *MountManager,
	lifecycle *LifecycleManager,
	commander Commander,
	poweroffMode string,
) *ShutdownCoordinator {
	return newShutdownCoordinatorWithKiller(mounts, lifecycle, commander, poweroffMode, killPID1)
}

func newShutdownCoordinatorWithKiller(
	mounts *MountManager,
	lifecycle *LifecycleManager,
	commander Commander,
	poweroffMode string,
	killPID1 func() error,
) *ShutdownCoordinator {
	if commander == nil {
		commander = ExecCommander{}
	}
	if killPID1 == nil {
		killPID1 = func() error {
			return fmt.Errorf("kill PID 1 function is not configured")
		}
	}
	return &ShutdownCoordinator{
		mounts:    mounts,
		lifecycle: lifecycle,
		commander: commander,
		poweroff:  poweroffFor(poweroffMode),
		killPID1:  killPID1,
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

	// Step 3: Power off using the configured strategy.
	log.Println("shutdown: initiating power-off")
	if err := sc.poweroff(sc.commander, sc.killPID1); err != nil {
		return fmt.Errorf("shutdown: poweroff: %w", err)
	}

	return nil
}
