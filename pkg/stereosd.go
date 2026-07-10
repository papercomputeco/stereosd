// Package stereosd implements the StereOS daemon — the control plane bridge
// between the host system and StereOS.
//
// stereosd runs inside every StereOS instance and provides:
//   - Lifecycle signaling over virtio-vsock (CID:3, port 1024)
//   - Shared directory mounting (virtio-fs / 9p)
//   - Secret injection to tmpfs-backed files
//   - Graceful shutdown coordination
//   - HTTP API for operator tooling and status queries
//   - Polling agentd's HTTP API for agent status
//
// Communication channel: virtio-vsock (AF_VSOCK) keeps the control plane
// independent of network configuration (critical when network.mode = "none").
//
// agentd integration: agentd operates on a reconciliation loop, reading
// configuration from /etc/stereos/jcard.toml and secrets from
// /run/stereos/secrets/ on a recurring tick. stereosd polls agentd's HTTP
// API (served on a Unix socket) to fetch agent status rather than relying
// on push-based updates.
package stereosd

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/papercomputeco/stereosd/pkg/tcp"
	"github.com/papercomputeco/stereosd/pkg/vsock"
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

	// ConfigDir is the directory where stereosd writes configuration files
	// (e.g., jcard.toml) for agentd to consume.
	ConfigDir = "/etc/stereos"

	// ConfigPath is the path where the jcard.toml config is written.
	ConfigPath = "/etc/stereos/jcard.toml"

	// SocketPath is the unix socket path for the stereosd HTTP API.
	SocketPath = "/run/stereos/stereosd.sock"

	// AdminGroup is the group that gets read/write access to daemon sockets.
	// Members of this group (typically the "admin" user) can connect to
	// stereosd and agentd sockets and attach to agent tmux sessions.
	AdminGroup = "admin"
)

// Config holds the daemon configuration.
type Config struct {
	// VsockPort is the port to listen on for host communication.
	VsockPort uint32

	// ListenMode controls the control plane listener type.
	// Valid values: "vsock" (AF_VSOCK only), "tcp" (TCP only),
	// "auto" (try vsock, fall back to tcp). Default: "auto".
	ListenMode string

	// SocketPath is the unix socket path for the stereosd HTTP API.
	SocketPath string

	// AgentdSocketPath is the unix socket path for agentd's HTTP API.
	// stereosd polls this socket to fetch agent status.
	AgentdSocketPath string

	// PollInterval is how often stereosd polls agentd for status.
	// If zero, DefaultPollInterval is used.
	PollInterval time.Duration

	// RuntimeDirs defines the runtime directory paths.
	RuntimeDirs RuntimeDirs

	// Commander abstracts system commands. If nil, uses real exec.
	Commander Commander

	// PoweroffMode selects how shutdown powers the machine off:
	// "systemctl" (default) or "signal-init" (SIGTERM PID 1, for capstan).
	PoweroffMode string
}

// DefaultConfig returns the default daemon configuration.
func DefaultConfig() Config {
	return Config{
		VsockPort:        VsockPort,
		ListenMode:       "auto",
		SocketPath:       SocketPath,
		AgentdSocketPath: AgentdSocketPath,
		PollInterval:     DefaultPollInterval,
		RuntimeDirs:      DefaultRuntimeDirs(),
		PoweroffMode:     PoweroffSystemctl,
	}
}

// Daemon is the StereOS daemon. It orchestrates all stereosd subsystems.
type Daemon struct {
	config    Config
	lifecycle *LifecycleManager
	secrets   *SecretManager
	sshkeys   *SSHKeyManager
	mounts    *MountManager
	ipc       *IPCServer
	agentd    *AgentdClient
	shutdown  *ShutdownCoordinator

	// listener overrides the auto-detected control plane listener when
	// set. If nil, createListener selects one based on Config.ListenMode.
	listener net.Listener

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

	if config.AgentdSocketPath == "" {
		config.AgentdSocketPath = AgentdSocketPath
	}
	if config.PollInterval == 0 {
		config.PollInterval = DefaultPollInterval
	}

	lifecycle := NewLifecycleManager()
	secrets := NewSecretManager(config.RuntimeDirs.Secrets)
	sshkeys := NewSSHKeyManager()
	mounts := NewMountManager(commander)

	d := &Daemon{
		config:            config,
		lifecycle:         lifecycle,
		secrets:           secrets,
		sshkeys:           sshkeys,
		mounts:            mounts,
		agentd:            NewAgentdClient(config.AgentdSocketPath),
		shutdownRequested: make(chan struct{}),
	}

	// IPC server (stereosd HTTP API)
	d.ipc = NewIPCServer(config.SocketPath)
	d.ipc.SetDaemon(d)

	// Shutdown coordinator
	d.shutdown = NewShutdownCoordinator(mounts, lifecycle, commander, config.PoweroffMode)

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

	// Step 2: Start the control plane listener
	ln, err := d.createListener()
	if err != nil {
		return fmt.Errorf("listener: %w", err)
	}
	server := NewServer(ln, d)

	// Step 3: Start the HTTP API and vsock servers in background goroutines
	errCh := make(chan error, 2)

	go func() {
		if err := server.Serve(ctx); err != nil {
			errCh <- fmt.Errorf("server: %w", err)
		}
	}()

	go func() {
		if err := d.ipc.Serve(ctx); err != nil {
			errCh <- fmt.Errorf("ipc server: %w", err)
		}
	}()

	// Step 4: Start the agentd status poller
	poller := NewAgentdPoller(d.agentd, d.lifecycle, d.config.PollInterval)
	go poller.Run(ctx)

	// Step 5: Report readiness
	d.lifecycle.Transition(StateReady, "all subsystems started")
	log.Println("stereosd: ready")

	// Step 6: Wait for shutdown signal or context cancellation
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

// createListener creates the control plane listener based on the configured
// listen mode. When d.listener is already set, it is returned directly.
func (d *Daemon) createListener() (net.Listener, error) {
	if d.listener != nil {
		return d.listener, nil
	}

	switch d.config.ListenMode {
	case "tcp":
		return tcp.NewListener(d.config.VsockPort)
	case "vsock":
		return vsock.NewListener(d.config.VsockPort)
	default: // "auto"
		// Check if a vsock transport is actually attached (e.g., vhost-vsock-pci
		// from the hypervisor). AF_VSOCK sockets can be created even without a
		// transport (the kernel module is loaded), but the listener will never
		// receive connections without a device from the host side.
		if !vsock.TransportAvailable() {
			log.Printf("vsock: no vsock transport detected, using TCP listener")
			return tcp.NewListener(d.config.VsockPort)
		}
		l, err := vsock.NewListener(d.config.VsockPort)
		if err != nil {
			log.Printf("vsock: AF_VSOCK unavailable (%v), falling back to TCP", err)
			return tcp.NewListener(d.config.VsockPort)
		}
		return l, nil
	}
}

// requestShutdown signals that a shutdown has been requested.
// Safe to call multiple times.
func (d *Daemon) requestShutdown() {
	d.shutdownOnce.Do(func() {
		close(d.shutdownRequested)
	})
}

// -- Handler implementation -------------------------------------------------
// HandleMessage processes messages received from the host.

func (d *Daemon) HandleMessage(ctx context.Context, env *Envelope) (*Envelope, error) {
	switch env.Type {
	case MsgPing:
		return NewEnvelope(MsgPong, nil)

	case MsgGetHealth:
		health := d.lifecycle.Health()
		return NewEnvelope(MsgHealth, &health)

	case MsgSetConfig:
		var payload ConfigPayload
		if err := env.DecodePayload(&payload); err != nil {
			return nil, fmt.Errorf("decode config payload: %w", err)
		}
		if err := os.WriteFile(ConfigPath, []byte(payload.Content), 0644); err != nil {
			return NewEnvelope(MsgAck, &AckPayload{
				ReplyTo: MsgSetConfig,
				OK:      false,
				Error:   err.Error(),
			})
		}
		log.Printf("config: wrote %s (%d bytes)", ConfigPath, len(payload.Content))
		return NewEnvelope(MsgAck, &AckPayload{
			ReplyTo: MsgSetConfig,
			OK:      true,
		})

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

	case MsgInjectSSHKey:
		var payload SSHKeyPayload
		if err := env.DecodePayload(&payload); err != nil {
			return nil, fmt.Errorf("decode ssh key payload: %w", err)
		}
		if err := d.sshkeys.InjectAuthorizedKey(&payload); err != nil {
			return NewEnvelope(MsgAck, &AckPayload{
				ReplyTo: MsgInjectSSHKey,
				OK:      false,
				Error:   err.Error(),
			})
		}
		return NewEnvelope(MsgAck, &AckPayload{
			ReplyTo: MsgInjectSSHKey,
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

// SSHKeys returns the SSH key manager for external access.
func (d *Daemon) SSHKeys() *SSHKeyManager {
	return d.sshkeys
}

// Agentd returns the agentd client for external access.
func (d *Daemon) Agentd() *AgentdClient {
	return d.agentd
}
