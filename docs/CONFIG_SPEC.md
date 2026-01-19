# Watermelon Configuration Specification

This document describes the `.watermelon.toml` configuration file format for Watermelon sandboxes.

## Overview

The `.watermelon.toml` file defines how your project's sandbox VM is configured. Place this file in your project's root directory.

```toml
# Example .watermelon.toml
[vm]
image = "ubuntu-22.04"

[network]
allow = ["registry.npmjs.org", "github.com"]

[tools]
"node:20-slim" = ["node", "npm", "npx"]

[mounts]
# "~/.gitconfig" = { target = "/home/dev/.gitconfig" }

[ports]
forward = [3000, 8080]

[resources]
memory = "4GB"
cpus = 2
disk = "10GB"

[security]
on_violation = "log"

[ide]
command = "code"
```

---

## Sections

### `[vm]`

Configures the base virtual machine.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `image` | string | `"ubuntu-22.04"` | Base OS image for the VM |

**Supported images:**
- `ubuntu-22.04` (recommended)

```toml
[vm]
image = "ubuntu-22.04"
```

---

### `[network]`

Controls network access from the sandbox. By default, all outbound network access is blocked.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allow` | string[] | `[]` | List of allowed domains/IPs |

**Domain format:**
- Plain domain: `"example.com"`
- Wildcard subdomain: `"*.example.com"`
- Domain with port: `"example.com:443"`
- IP address: `"192.168.1.1"`

**Security:** Domains cannot contain shell metacharacters (`;|&$\`\`).

```toml
[network]
allow = [
    # Package registries
    "registry.npmjs.org",
    "pypi.org",
    "files.pythonhosted.org",

    # Git hosting
    "github.com",
    "*.githubusercontent.com",

    # Wildcards for subdomains
    "*.huggingface.co",
]
```

**To completely block network access:**
```toml
[network]
allow = []
```

---

### `[tools]`

Defines containerized tools available in the sandbox. Tools are run via nerdctl containers with host networking enabled.

| Field | Type | Description |
|-------|------|-------------|
| `"image:tag"` | string[] | List of commands to expose from this container image |

**Format:** `"<docker-image>:<tag>" = ["cmd1", "cmd2", ...]`

Each command becomes available as a wrapper script in `/usr/local/bin/` inside the VM.

```toml
[tools]
# Node.js tools
"node:20-slim" = ["node", "npm", "npx"]

# Python tools
"python:3.12-slim" = ["python", "python3", "pip"]

# Foundry (Ethereum development)
"ghcr.io/foundry-rs/foundry" = ["forge", "cast", "anvil", "chisel"]

# Go compiler
"golang:1.22" = ["go"]

# Rust toolchain
"rust:latest" = ["cargo", "rustc"]
```

**How it works:**
1. When you run `npm install` in the sandbox, it executes:
   ```bash
   nerdctl run --rm -it --network=host -v /project:/project -w /project node:20-slim npm install
   ```
2. The `--network=host` flag ensures ports bind to the VM's network
3. Lima's port forwarding exposes these ports to the host

---

### `[mounts]`

Additional host paths to mount into the VM (beyond the project directory).

| Field | Type | Description |
|-------|------|-------------|
| `"<host-path>"` | Mount | Mount configuration object |

**Mount object:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `target` | string | required | Path inside the VM |
| `mode` | string | `"ro"` | Mount mode: `"ro"` (read-only) or `"rw"` (read-write) |

```toml
[mounts]
# Git config (read-only)
"~/.gitconfig" = { target = "/home/dev/.gitconfig" }

# SSH keys (read-only) - use with caution
"~/.ssh" = { target = "/home/dev/.ssh", mode = "ro" }

# npm auth tokens
"~/.npmrc" = { target = "/home/dev/.npmrc" }

# Shared cache directory (read-write)
"~/.cache/huggingface" = { target = "/home/dev/.cache/huggingface", mode = "rw" }
```

**Note:** The project directory is always mounted at `/project` with read-write access.

---

### `[ports]`

Ports to forward from the VM to the host machine.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `forward` | int[] | `[]` | List of ports to forward |

**Port requirements:**
- Must be in range 1-65535
- Ports are forwarded bidirectionally (guest port = host port)

```toml
[ports]
# Single port
forward = [3000]

# Multiple ports
forward = [3000, 8000, 8080, 8545]
```

**Common ports by framework:**

| Framework | Port |
|-----------|------|
| Vite | 5173 |
| Next.js | 3000 |
| Django | 8000 |
| FastAPI | 8000 |
| Anvil (Ethereum) | 8545 |
| Jupyter | 8888 |
| TensorBoard | 6006 |

---

### `[resources]`

VM resource allocation.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `memory` | string | `"2GB"` | RAM allocation |
| `cpus` | int | `1` | Number of CPU cores (minimum: 1) |
| `disk` | string | `"10GB"` | Disk size |

**Size format:** Number followed by unit (`MB`, `GB`, `TB`)

```toml
[resources]
memory = "4GB"
cpus = 2
disk = "15GB"
```

**Recommended settings by use case:**

| Use Case | Memory | CPUs | Disk |
|----------|--------|------|------|
| Simple Node.js | 2GB | 1 | 10GB |
| React/Next.js | 4GB | 2 | 15GB |
| Smart contracts | 4GB | 2 | 15GB |
| Machine learning | 16GB | 4 | 50GB |
| Security audit | 2GB | 1 | 5GB |

---

### `[security]`

Security policy configuration.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `on_violation` | string | `"log"` | Action when network policy is violated |

**Violation actions:**

| Value | Behavior |
|-------|----------|
| `"log"` | Log the violation and allow the request |
| `"fail"` | Block the request and log an error |
| `"silent"` | Block the request silently |

```toml
[security]
# Development: see what's being blocked
on_violation = "log"

# Production/audit: strict blocking
on_violation = "fail"

# Quiet mode: block without noise
on_violation = "silent"
```

---

### `[ide]`

Configures the IDE for the `watermelon code` command.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `command` | string | `"code"` | IDE command to launch |

**Supported IDE commands:**

| IDE | Command |
|-----|---------|
| VS Code | `code` |
| Cursor | `cursor` |
| VSCodium | `codium` |
| VS Code Insiders | `code-insiders` |

```toml
[ide]
# VS Code (default)
command = "code"

# Cursor
command = "cursor"

# VSCodium
command = "codium"
```

**How it works:**

When you run `watermelon code`, it executes:
```bash
<command> --remote ssh-remote+lima-<vmname> /project
```

This opens your IDE connected to the sandbox VM via SSH Remote, directly in the `/project` directory.

**Security:** The IDE command is validated to prevent shell injection (no metacharacters allowed).

---

## Complete Examples

### Minimal Configuration

```toml
[vm]
image = "ubuntu-22.04"

[tools]
"node:20-slim" = ["node", "npm", "npx"]

[resources]
memory = "2GB"
cpus = 1
disk = "10GB"
```

### Full-Stack Web Development

```toml
[vm]
image = "ubuntu-22.04"

[network]
allow = [
    "registry.npmjs.org",
    "pypi.org",
    "files.pythonhosted.org",
    "github.com",
    "*.githubusercontent.com",
]

[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[ports]
forward = [3000, 8000]

[resources]
memory = "8GB"
cpus = 4
disk = "20GB"

[security]
on_violation = "log"
```

### Maximum Security (Audit Mode)

```toml
[vm]
image = "ubuntu-22.04"

[network]
allow = []

[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[ports]
forward = []

[resources]
memory = "2GB"
cpus = 1
disk = "5GB"

[security]
on_violation = "fail"
```

---

## Validation Rules

The configuration is validated at VM creation time:

1. **Resources:**
   - `cpus` must be ≥ 1
   - `memory` and `disk` must be non-empty

2. **Security:**
   - `on_violation` must be one of: `log`, `fail`, `silent`

3. **Network:**
   - Domains cannot contain shell metacharacters: `;|&$\`\`

4. **Ports:**
   - Each port must be in range 1-65535

---

## File Location

Watermelon looks for `.watermelon.toml` in the current working directory when running commands. The VM name is derived from the project path to ensure consistent naming across sessions.
