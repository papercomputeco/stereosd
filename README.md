# `stereosd` 📦

Control plane daemon for [stereOS](https://github.com/papercomputeco/stereos).
Manages the system and bridges with the host orchestrator.

`stereosd` handles:

- **Lifecycle signaling** - boot status, readiness, and health reported to
  the host over vsock
- **Secret injection** - host pushes secrets over vsock, `stereosd` writes them
  to admin tmpfs (`/run/stereos/secrets/`)
- **SSH key injection** - ephemeral per-sandbox `authorized_keys` installation
- **Shared directory mounting** - virtio-fs and 9p mounts from host-provided tags
- **Graceful shutdown** - unmounts, filesystem sync, `systemctl poweroff`
- **agentd polling** - periodically queries agentd for agent status, reports
  to the host as part of health

### Start sequence

1. Kicked by `systemd`
1. Create runtime directories (`/run/stereos`, `/run/stereos/secrets`, `/etc/stereos`)
1. Transition to `booting`
1. Create control plane listener (vsock or TCP based on `--listen-mode`)
1. Start the NDJSON message server
1. Start the IPC HTTP server on `/run/stereos/stereosd.sock`
1. Start the agentd status poller
1. Transition to `ready`

### Listener selection (`--listen-mode`)

| Mode | Behavior |
|------|----------|
| `auto` | Check `vsock.TransportAvailable()`, use vsock if available, else TCP |
| `vsock` | AF_VSOCK only (Linux/KVM with `vhost-vsock-pci`) |
| `tcp` | TCP `0.0.0.0:1024` only (macOS/HVF with QEMU user-mode networking) |

## Wire protocol

Newline-delimited JSON (NDJSON) over AF_VSOCK or TCP. One JSON object per
line, max 1MB per message.

### Envelope

```json
{"type": "<message_type>", "payload": { ... }}
```

### Messages

| Type | Direction | Payload | Response |
|------|-----------|---------|----------|
| `ping` | host -> guest | none | `pong` |
| `get_health` | host -> guest | none | `health` |
| `set_config` | host -> guest | `ConfigPayload` | `ack` |
| `inject_secret` | host -> guest | `SecretPayload` | `ack` |
| `inject_ssh_key` | host -> guest | `SSHKeyPayload` | `ack` |
| `mount` | host -> guest | `MountPayload` | `ack` |
| `shutdown` | host -> guest | `ShutdownPayload` | `ack` (immediate, then poweroff) |
| `lifecycle` | guest -> host | `LifecyclePayload` | (push, no response) |

## Subsystems

### `SecretManager`

Writes secrets to `/run/stereos/secrets/<name>` (tmpfs, `admin` owned, never on persistent disk).

- Atomic writes: write to `.tmp`, then `rename()`
- Default file mode `0600`, configurable via `SecretPayload.Mode`
- `secret.Value` is zeroed from the payload struct after writing (memory safety)
- Name validation: must be a simple filename (no `/`, no `..`)
- Operations: `Inject`, `List`, `Remove`

### `SSHKeyManager`

Installs SSH public keys into `~/.ssh/authorized_keys` for a given user.

- Resolves home directory via `os/user.Lookup`
- Creates `~/.ssh/` (mode `0700`), writes `authorized_keys` (mode `0600`)
- Atomic write via `.tmp` + `rename()`
- Validates key format against known prefixes: `ssh-ed25519`, `ssh-rsa`,
  `ecdsa-sha2-*`, `sk-ssh-ed25519@openssh.com`,
  `sk-ecdsa-sha2-nistp256@openssh.com`
- Files are owned by the target user (uid/gid from user lookup)

### `MountManager`

Mounts host-shared directories into the guest.

- Supported filesystems: `virtiofs` (`mount -t virtiofs <tag> <path>`) and
  `9p` (`mount -t 9p -o trans=virtio,version=9p2000.L <tag> <path>`)
- Path validation: must be absolute, cannot mount over system directories
  (`/`, `/nix`, `/etc`, `/bin`, `/boot`, `/dev`, `/proc`, `/sys`, `/run`)
- Auto-creates mount point directory (mode `0755`)
- Sets ownership to `agent:agent` (best effort)
- Tracks mounts in order; `UnmountAll()` unmounts in reverse (LIFO)

### `LifecycleManager`

State machine: `booting` -> `ready` -> `healthy` / `degraded` -> `shutdown`

- Thread-safe (`sync.RWMutex`)
- On transition, pushes `lifecycle` envelope to the host via a configurable
  `vsockSend` callback
- Tracks agent statuses (replaced atomically by the agentd poller)
- Promotes `ready` -> `healthy` when at least one agent is running
- `Health()` returns the full `HealthPayload` for `get_health` responses

### `ShutdownCoordinator`

Graceful shutdown sequence:

1. Transition lifecycle to `shutdown`
2. `UnmountAll()` shared directories (reverse order)
3. `sync` filesystems
4. `systemctl poweroff`

systemd handles SIGTERM delivery to agentd and other services within their
configured `TimeoutStopSec`.

### `AgentdClient`

Polls [agentd](https://github.com/papercomputeco/agentd) over its Unix domain
socket (`/run/stereos/agentd.sock`).

- HTTP client with Unix socket transport
- Consumes `GET /v1/health` and `GET /v1/agents`
- Poll interval: 5 seconds
- Atomically replaces agent statuses in the `LifecycleManager`
- agentd being unreachable is expected during boot (logged, not fatal)

## IPC HTTP API

Served on `/run/stereos/stereosd.sock` (mode `0660`, group `admin`).

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/ping` | `{"status": "ok"}` |
| `GET` | `/v1/health` | Full `HealthPayload` (state, uptime, agents) |
| `POST` | `/v1/secrets` | Inject a secret (`SecretPayload` body) |
| `GET` | `/v1/secrets` | List secret names |
| `DELETE` | `/v1/secrets/{name}` | Remove a secret |
| `POST` | `/v1/mounts` | Mount a shared directory (`MountPayload` body) |
| `GET` | `/v1/mounts` | List active mounts |
| `POST` | `/v1/shutdown` | Initiate graceful shutdown (returns `202 Accepted`) |
| `GET` | `/v1/agents` | List agents (cached from agentd poller) |


### Runtime directories

| Path | Mode | Owner | Purpose |
|------|------|-------|---------|
| `/run/stereos` | `0755` | root:admin | Base runtime directory |
| `/run/stereos/secrets` | `0700` | root:root | tmpfs-backed secret store |
| `/etc/stereos` | `0755` | root:root | Configuration (jcard.toml) |

## NixOS module

The flake exports `nixosModules.default` with the following options:

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `services.stereosd.enable` | bool | `false` | Enable the stereosd daemon |
| `services.stereosd.package` | package | flake default | The stereosd package |
| `services.stereosd.listenMode` | enum `["auto" "vsock" "tcp"]` | `"auto"` | Control plane listener mode |
| `services.stereosd.extraArgs` | list of str | `[]` | Additional CLI arguments |

The systemd unit runs after `network.target` and
`systemd-tmpfiles-setup.service`, with `Restart=always` and `DynamicUser=true`
(overridden to `false` by the stereOS NixOS module since stereosd needs root
for vsock binding, mount operations, and secret file ownership).
