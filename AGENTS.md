# AGENTS.md

### Project Overview

`stereosd` is the control plane daemon for StereOS, a Linux based operating system
purpose built for AI agents.

**Language:** Go 1.25+
**Go Module:** `github.com/papercomputeco/stereosd`

### Architecture

```
Host (Masterblaster vmhost)
  │
  │  NDJSON over AF_VSOCK (CID 3, port 1024)
  │  or TCP :1024 fallback (macOS/HVF)
  │
  ▼
┌──────────────────────────────────────────────────────┐
│  stereosd                                            │
│                                                      │
│  Server (vsock/tcp)         IPCServer (unix socket)  │
│  ├─ HandleMessage()         ├─ /v1/ping              │
│  │  ├─ ping -> pong         ├─ /v1/health            │
│  │  ├─ get_health -> health ├─ /v1/secrets           │
│  │  ├─ set_config           ├─ /v1/mounts            │
│  │  ├─ inject_secret        ├─ /v1/agents            │
│  │  ├─ inject_ssh_key       └─ /v1/shutdown          │
│  │  ├─ mount                                         │
│  │  └─ shutdown             AgentdPoller (5s tick)   │
│  │                          └─ GET agentd /v1/agents │
│  ├─ SecretManager                                    │
│  ├─ SSHKeyManager                                    │
│  ├─ MountManager                                     │
│  ├─ LifecycleManager                                 │
│  └─ ShutdownCoordinator                              │
└──────────────────────────────────────────────────────┘
  │                                    │
  │ /run/stereos/stereosd.sock         │ /run/stereos/agentd.sock
  │ (local consumers)                  │ (polls agentd HTTP API)
  ▼                                    ▼
  admin tools, CLI                    agentd
```

### Do

- Always use the Ginkgo/Gomega testing frameworks when writing tests.
- Always use `make` operations for development: use `make help` to understand
  the various operations available.
- Follow idiomatic Go and prefer using the `func NewExampleStruct() *ExampleStruct`
  paradigm seen throughout.

### Project Structure

- `pkg/` - Go packages. Use the `go doc` command to get the documentation on the various
  package's public API.
- `.github/` - GitHub metadata and action workflows.
- `flake.nix` - The development Nix flake which bundles all necessary dependencies for development.

### Build System

The project uses a Makefile for all build and dev operations. Utilize `make help`
to see all available commands.

Build artifacts land in the `build/` directory.

### Testing

Always write tests using Ginkgo/Gomega.

Integration tests inject a real TCP `net.Listener` and a `mockCommander`
(records exec calls instead of running them) to test the full daemon without
requiring vsock, mount, or poweroff capabilities.

Hurl integration tests (`hurls/`) test the IPC HTTP API against a running
`stereosd` instance.

```bash
# unit + integration tests
make test

# API smoke tests
cd hurls && make all SOCKET=/run/stereos/stereosd.sock
```
