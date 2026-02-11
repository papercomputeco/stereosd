# `stereosd`

`stereosd` is a lightweight control plane daemon that runs as part of a StereOS instance.
It provides the bridge between the host system (Masterblaster, cloud controllers, etc.)
and StereOS as an out-of-band control plane.

> [!WARNING]
> 🚧🏗️ StereOS is in active development - APIs will break 🚜🚧

`stereosd` handles:

- Lifecycle signaling (boot status, agent status, workload readiness, health) over virtio-vsock
- Directory mounting for agents (virtio-fs / 9p from jcard.toml `[[shared]]`)
- Secret injection for agents (from host over vsock --> tmpfs-backed files)
- Graceful shutdown coordination (`before_down` hooks, clean shutdown of AI agents)
- Admin operations on system
