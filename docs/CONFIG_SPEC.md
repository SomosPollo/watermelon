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
# "~/.gitconfig" = { target = "/mnt/watermelon/gitconfig" }

[ports]
forward = [3000, 8080]

[resources]
memory = "4GB"
cpus = 2
disk = "10GB"

[security]
enforcement = "fail"

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
- `ubuntu-22.04`

```toml
[vm]
image = "ubuntu-22.04"
```

---

### `[network]`

Controls network access from the sandbox. Non-allowlisted outbound behavior depends on `[security].enforcement`: the default strict mode blocks and records it, while discovery mode allows it and records rate-limited IPv4 policy events.

Workload DNS is transparently redirected to a managed resolver. In `fail` and `silent`, that resolver answers only names covered by the applicable general or per-process policy; other names receive a local negative response. In `log` and `ask`, it resolves arbitrary names so discovery and interactive prompts can work. Loopback, established/related response traffic, and scoped DHCPv4 lease traffic required by VM control networking remain available in every mode. The DHCP exception does not allow arbitrary external UDP.

In `fail` and `silent`, exact domain rules are resolved once during trusted VM bootstrap and served as exact records; recreate the VM to refresh those records if the destination's addresses change. A wildcard such as `"*.example.com"` dynamically resolves subdomains only; it does not include the apex `"example.com"`, which must be added separately when needed. A per-process resolver combines the general rules with that process's additional rules.

Network enforcement currently operates on IPv4. Watermelon disables IPv6 in `fail`, `silent`, and `ask` so it cannot bypass a blocking policy. Discovery mode (`log`) leaves IPv6 enabled; its rate-limited policy events currently capture IPv4 traffic only.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allow` | string[] | `[]` | List of allowed domains/IPs |

**Domain format:**
- Plain domain: `"example.com"`
- Wildcard subdomain: `"*.example.com"`
- Domain with port: `"example.com:443"`
- IP address: `"192.168.1.1"`

**Security:** Domains are parsed before rendering. Supported values are plain domains, wildcard subdomains without ports, IPv4 addresses, and plain domains or IPv4 addresses with TCP ports.

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

**To block new external connections, use an empty allow list with strict enforcement:**
```toml
[network]
allow = []

[security]
enforcement = "fail"
```

An empty allow list still permits the managed DNS path, loopback, established/related responses, and scoped VM-control DHCPv4 lease traffic. It does not grant arbitrary external UDP access.

#### `[network.process]`

Per-process network rules. Each key is a process name, and the value is a list of additional domains that process can access (in addition to the general `allow` list).

| Field | Type | Description |
|-------|------|-------------|
| `"<process-name>"` | string[] | Additional allowed domains for this process |

**Behavior:**
- Rules are **additive**: process-specific domains are added to the general `allow` list
- Processes not listed use only the general `allow` rules
- Wildcards supported: `"*.example.com"`
- `fail`, `silent`, and `ask` enforce these rules; `log` only observes non-allowlisted traffic

**Implementation:** Each listed process runs in a dedicated Linux network namespace with its own firewall and resolver policy. Its command wrapper invokes a root-owned, per-process launcher through a narrowly scoped authorization created by Watermelon. Namespace and helper identifiers are internal implementation details and are not derived into a stable, human-readable `watermelon-<process>` name. Other shell and `watermelon exec` commands run as the unprivileged VM user; general passwordless `sudo` is removed after provisioning.

```toml
[network]
allow = ["registry.npmjs.org", "pypi.org"]

[network.process]
claude = ["api.anthropic.com", "*.anthropic.com"]
codex = ["api.openai.com"]
aider = ["api.anthropic.com", "api.openai.com"]
```

**Process-name syntax:** Keys must be at most 255 bytes and match `[A-Za-z0-9_][A-Za-z0-9._+-]*`. They must begin with an ASCII letter, digit, or underscore; leading `-` or `.`, whitespace, control characters, path separators, and shell syntax are rejected.

**Note:** Requires VM reprovisioning (`watermelon destroy --force && watermelon run --no-shell`) to apply changes. Destroying the VM removes its state but does not delete `.watermelon.toml`.

---

### `[provision]`

Packages to install during VM provisioning. Each key corresponds to a package manager.

User-configured package installation runs after workload network policy is active, so its registries and download hosts must be covered by `[network].allow` in blocking modes.

Declare global CLI packages here and recreate the VM when you need reliable command wrappers. Do not rely on an ad-hoc global install to expose a newly introduced executable in the guest command path; wrapper discovery and creation happen during VM provisioning.

| Field | Type | Requires Tool | Install Command |
|-------|------|---------------|-----------------|
| `npm` | string[] | `node` image | `npm install -g <pkg>` |
| `pip` | string[] | `python` image | `pip install <pkg>` |
| `cargo` | string[] | `rust` image | `cargo install <pkg>` |
| `go` | string[] | `go` or `golang` image | `go install <pkg>` |
| `gem` | string[] | `ruby` image | `gem install <pkg>` |

**Validation:**
- Package names cannot contain shell metacharacters (`;|&$\`\``)
- Each package manager requires a matching tool image in `[tools]`
- If the package manager command is not found at provision time, provisioning fails

```toml
[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[provision]
npm = ["@anthropic-ai/claude-code", "typescript"]
pip = ["aider-chat", "black"]
```

**Use case:** Install AI coding assistants and development tools automatically:

```toml
[provision]
npm = ["@anthropic-ai/claude-code"]  # Claude Code CLI
pip = ["aider-chat"]                  # Aider AI assistant
cargo = ["ripgrep", "fd-find"]        # Fast search tools
```

---

### `[tools]`

Defines containerized tools available in the sandbox. Tools are run via nerdctl containers with host networking enabled.

| Field | Type | Description |
|-------|------|-------------|
| `"image:tag"` | string[] | List of commands to expose from this container image |

**Format:** `"<docker-image>:<tag>" = ["cmd1", "cmd2", ...]`

Each command becomes available as a wrapper script in `/usr/local/bin/` inside the VM.

Configured base images are pulled during Watermelon's trusted bootstrap, before workload network policy is activated. Do not add container-registry domains to `[network].allow` solely for these image pulls; add them only if the workload itself must contact a registry.

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
| `target` | string | required | `/mnt/watermelon` or a descendant path inside the VM |
| `mode` | string | `"ro"` | Mount mode: `"ro"` (read-only) or `"rw"` (read-write) |

```toml
[mounts]
# Git config (read-only)
"~/.gitconfig" = { target = "/mnt/watermelon/gitconfig" }

# SSH keys (read-only) - use with caution
"~/.ssh" = { target = "/mnt/watermelon/ssh", mode = "ro" }

# npm auth tokens
"~/.npmrc" = { target = "/mnt/watermelon/npmrc" }

# Shared cache directory (read-write)
"~/.cache/huggingface" = { target = "/mnt/watermelon/cache/huggingface", mode = "rw" }
```

Targets are normalized and must remain at or below `/mnt/watermelon`; `..` traversal is rejected. This dedicated namespace prevents additional mounts from shadowing guest system, home, policy, or project paths. Applications do not automatically treat these paths as home-directory configuration: point the relevant tool at the mounted file or directory explicitly.

**Note:** The project directory is separately mounted at `/project` with read-write access.

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
| `enforcement` | string | `"fail"` | How to enforce network policy |

**Enforcement modes:**

| Value | Behavior |
|-------|----------|
| `"fail"` | **Strict default:** block non-allowlisted traffic, record a rate-limited policy event, and resolve only policy names |
| `"log"` | **Discovery:** allow non-allowlisted traffic, record rate-limited IPv4 policy events, and resolve arbitrary names; IPv6 is not captured |
| `"silent"` | **Strict, quiet:** block non-allowlisted traffic without recording policy events and resolve only policy names |
| `"ask"` | **Interactive:** resolve arbitrary names, prompt for non-allowlisted TCP connections, reject other non-allowlisted traffic, and persist always-allow choices |

```toml
[security]
# Choose one mode. The strict default is:
enforcement = "fail"

# Alternatives:
# enforcement = "log"     # Discovery: allow; record IPv4 events (IPv6 is not captured)
# enforcement = "silent"  # Strict, quiet: block without policy events
# enforcement = "ask"     # Interactive: prompt for TCP; reject other non-allowlisted traffic
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
enforcement = "fail"
```

### Most Restrictive Built-in Policy (Audit Mode)

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
enforcement = "fail"
```

Even this policy retains the managed DNS path, loopback, established/related responses, and scoped DHCPv4 lease traffic required by VM control networking. That DHCP exception is not general external UDP access.

---

## Validation Rules

The configuration is validated at VM creation time:

1. **Resources:**
   - `cpus` must be ≥ 1
   - `memory` and `disk` must be non-empty

2. **Security:**
   - `enforcement` must be one of: `log`, `fail`, `silent`, `ask`

3. **Network:**
   - Domains are parsed as plain hosts, wildcard subdomains, IPv4 addresses, or host/IP plus TCP port
   - Wildcard domains cannot include ports

4. **Ports:**
   - Each port must be in range 1-65535

---

## File Location

Watermelon looks for `.watermelon.toml` in the current working directory when running commands. The VM name is derived from the project path to ensure consistent naming across sessions.
