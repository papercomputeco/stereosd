// Package stereosd implements the StereOS daemon — the control plane bridge
// between the host system and StereOS.
//
// stereosd runs inside every StereOS instance and provides:
//   - Lifecycle signaling over virtio-vsock (CID:3, port 1024)
//   - Shared directory mounting (virtio-fs / 9p)
//   - Secret injection to tmpfs-backed files
//   - Graceful shutdown coordination
//   - Unix socket IPC for agentd communication
//
// Communication channel: virtio-vsock (AF_VSOCK) keeps the control plane
// independent of network configuration (critical when network.mode = "none").
package stereosd

import (
	"context"
	"fmt"
	"log"
	"sync"
)

const (
	// VsockPort is the vsock port stereosd listens on for host communication.
	VsockPort = 1024

	// VsockCID is the guest CID for vsock communication.
	// CID 2 is reserved for the host; CID 3 is the first guest CID.
	VsockCID = 3

	// SecretDir is the tmpfs-backed directory where stereosd writes injected
	// secrets for agentd to consume. Never written to persistent disk.
	SecretDir = "/run/stereos/secrets"

	// SocketPath is the unix socket path that agentd uses to communicate
	// with stereosd.
	SocketPath = "/run/stereos/stereosd.sock"
)

// ListenerFactory creates the vsock listener. This is a function type so
// it can be replaced in tests with a TCP or Unix listener.
type ListenerFactory func(port uint32) (VsockListener, error)

// Config holds the daemon configuration.
type Config struct {
	// VsockPort is the port to listen on for host communication.
	VsockPort uint32

	// SocketPath is the unix socket path for agentd IPC.
	SocketPath string

	// RuntimeDirs defines the runtime directory paths.
	RuntimeDirs RuntimeDirs

	// ListenerFactory creates the vsock listener.
	// If nil, the real vsock listener is used.
	ListenerFactory ListenerFactory

	// Commander abstracts system commands. If nil, uses real exec.
	Commander Commander
}

// DefaultConfig returns the default daemon configuration.
func DefaultConfig() Config {
	return Config{
		VsockPort:   VsockPort,
		SocketPath:  SocketPath,
		RuntimeDirs: DefaultRuntimeDirs(),
	}
}

// Daemon is the StereOS daemon. It orchestrates all stereosd subsystems.
type Daemon struct {
	config    Config
	lifecycle *LifecycleManager
	secrets   *SecretManager
	mounts    *MountManager
	ipc       *IPCServer
	shutdown  *ShutdownCoordinator

	// shutdownRequested is closed when a shutdown command is received,
	// signaling the main Run loop to begin shutdown.
	shutdownRequested chan struct{}
	shutdownOnce      sync.Once
}

// NewDaemon creates a new stereosd instance with default configuration.
func NewDaemon() *Daemon {
	return NewDaemonWithConfig(DefaultConfig())
}

// NewDaemonWithConfig creates a new stereosd instance with the given configuration.
func NewDaemonWithConfig(config Config) *Daemon {
	commander := config.Commander
	if commander == nil {
		commander = ExecCommander{}
	}

	lifecycle := NewLifecycleManager()
	secrets := NewSecretManager(config.RuntimeDirs.Secrets)
	mounts := NewMountManager(commander)

	d := &Daemon{
		config:            config,
		lifecycle:         lifecycle,
		secrets:           secrets,
		mounts:            mounts,
		shutdownRequested: make(chan struct{}),
	}

	// IPC server (stereosd -> agentd communication via HTTP API)
	d.ipc = NewIPCServer(config.SocketPath)
	d.ipc.SetDaemon(d)

	// Shutdown coordinator
	d.shutdown = NewShutdownCoordinator(d.ipc, mounts, lifecycle, commander)

	return d
}

// Run starts the stereosd daemon and blocks until the context is cancelled
// or a shutdown is requested by the host.
func (d *Daemon) Run(ctx context.Context) error {
	log.Println("stereosd: initializing control plane")

	// Step 1: Create runtime directories
	if err := EnsureRuntimeDirs(d.config.RuntimeDirs); err != nil {
		return fmt.Errorf("runtime dirs: %w", err)
	}

	d.lifecycle.Transition(StateBooting, "runtime directories ready")

	// Step 2: Start the vsock listener
	vsockListener, err := d.createVsockListener()
	if err != nil {
		return fmt.Errorf("vsock listener: %w", err)
	}
	vsockServer := NewVsockServer(vsockListener, d)

	// Step 3: Start the IPC unix socket
	// Both servers run in background goroutines
	errCh := make(chan error, 2)

	go func() {
		if err := vsockServer.Serve(ctx); err != nil {
			errCh <- fmt.Errorf("vsock server: %w", err)
		}
	}()

	go func() {
		if err := d.ipc.Serve(ctx); err != nil {
			errCh <- fmt.Errorf("ipc server: %w", err)
		}
	}()

	// Step 4: Report readiness
	d.lifecycle.Transition(StateReady, "all subsystems started")
	log.Println("stereosd: ready")

	// Step 5: Wait for shutdown signal or context cancellation
	select {
	case <-ctx.Done():
		log.Println("stereosd: context cancelled, shutting down")
	case <-d.shutdownRequested:
		log.Println("stereosd: shutdown requested by host")
	case err := <-errCh:
		log.Printf("stereosd: server error: %v", err)
		return err
	}

	// Cleanup
	d.lifecycle.Transition(StateShutdown, "shutting down")
	d.ipc.Close()

	return nil
}

// createVsockListener creates the vsock listener using the configured factory
// or falls back to the real vsock implementation.
func (d *Daemon) createVsockListener() (VsockListener, error) {
	if d.config.ListenerFactory != nil {
		return d.config.ListenerFactory(d.config.VsockPort)
	}
	return NewRealVsockListener(d.config.VsockPort)
}

// requestShutdown signals that a shutdown has been requested.
// Safe to call multiple times.
func (d *Daemon) requestShutdown() {
	d.shutdownOnce.Do(func() {
		close(d.shutdownRequested)
	})
}

// -- VsockHandler implementation --------------------------------------------
// HandleVsockMessage processes messages received from the host over vsock.

func (d *Daemon) HandleVsockMessage(ctx context.Context, env *Envelope) (*Envelope, error) {
	switch env.Type {
	case MsgPing:
		return NewEnvelope(MsgPong, nil)

	case MsgInjectSecret:
		var payload SecretPayload
		if err := env.DecodePayload(&payload); err != nil {
			return nil, fmt.Errorf("decode secret payload: %w", err)
		}
		if err := d.secrets.Inject(&payload); err != nil {
			return NewEnvelope(MsgAck, &AckPayload{
				ReplyTo: MsgInjectSecret,
				OK:      false,
				Error:   err.Error(),
			})
		}
		return NewEnvelope(MsgAck, &AckPayload{
			ReplyTo: MsgInjectSecret,
			OK:      true,
		})

	case MsgMount:
		var payload MountPayload
		if err := env.DecodePayload(&payload); err != nil {
			return nil, fmt.Errorf("decode mount payload: %w", err)
		}
		if err := d.mounts.Mount(&payload); err != nil {
			return NewEnvelope(MsgAck, &AckPayload{
				ReplyTo: MsgMount,
				OK:      false,
				Error:   err.Error(),
			})
		}
		return NewEnvelope(MsgAck, &AckPayload{
			ReplyTo: MsgMount,
			OK:      true,
		})

	case MsgShutdown:
		var payload ShutdownPayload
		// Payload is optional for shutdown
		if env.Payload != nil {
			env.DecodePayload(&payload)
		}

		// Run shutdown in a goroutine so we can ack immediately
		go func() {
			if err := d.shutdown.Execute(ctx, &payload); err != nil {
				log.Printf("stereosd: shutdown error: %v", err)
			}
			d.requestShutdown()
		}()

		return NewEnvelope(MsgAck, &AckPayload{
			ReplyTo: MsgShutdown,
			OK:      true,
		})

	default:
		return nil, fmt.Errorf("unknown message type: %s", env.Type)
	}
}

// Lifecycle returns the lifecycle manager for external access.
func (d *Daemon) Lifecycle() *LifecycleManager {
	return d.lifecycle
}

// Secrets returns the secret manager for external access.
func (d *Daemon) Secrets() *SecretManager {
	return d.secrets
}

// Mounts returns the mount manager for external access.
func (d *Daemon) Mounts() *MountManager {
	return d.mounts
}
