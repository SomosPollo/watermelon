---
name: watermelon
description: Use when a .watermelon.toml file exists in the project, when asked to set up sandboxed development environments, or when running package manager commands (npm install, pip install, cargo build) and watermelon is available on the system
---

# Watermelon

## Overview

Watermelon sandboxes developer commands inside a Lima-managed Linux VM on macOS, protecting the host from untrusted packages. All commands (npm, pip, cargo, etc.) run isolated — host credentials, system files, and network access are shielded.

**Core principle:** If `.watermelon.toml` exists in the project, route all build/install/test commands through `watermelon exec` automatically.

## Agent Behavior

**Detection:** Check for `.watermelon.toml` in the project root. If present, all package manager and build commands MUST go through `watermelon exec`.

```bash
# WRONG - runs on host, defeats sandboxing
npm install
pip install requests
cargo build

# RIGHT - runs inside sandbox
watermelon exec "npm install"
watermelon exec "pip install requests"
watermelon exec "cargo build"
```

**Compound commands** — chain inside a single exec:
```bash
watermelon exec "npm install && npm run build && npm test"
```

**Rules:**
- Use `watermelon exec` for discrete commands (default)
- Use `watermelon run` only when the user explicitly asks for an interactive shell
- Never call `watermelon stop` or `watermelon destroy` unless the user asks — VMs persist intentionally so installed packages survive between sessions
- If `watermelon exec` fails with a network error, run `watermelon logs` to find blocked domains, then help the user add them to `[network].allow`

## CLI Quick Reference

| Command | Purpose |
|---------|---------|
| `watermelon init` | Create `.watermelon.toml` template |
| `watermelon run` | Open interactive shell in sandbox |
| `watermelon exec "<cmd>"` | Run command in sandbox (default for all commands) |
| `watermelon code` | Open IDE connected to sandbox via SSH |
| `watermelon status` | Show VM status for current project |
| `watermelon list` | List all watermelon VMs |
| `watermelon stop` | Stop VM, preserve state |
| `watermelon destroy [--force]` | Delete VM permanently |
| `watermelon logs [--clear]` | Show/clear blocked network requests |

**Installation (if not available):**
```bash
brew install lima
curl -fsSL https://raw.githubusercontent.com/saeta-eth/watermelon/main/install.sh | sh
```

## Config Reference (`.watermelon.toml`)

### VM and Resources

```toml
[vm]
image = "ubuntu-22.04"  # Only supported image

[resources]
memory = "4GB"   # Default: 2GB. Format: number + MB/GB/TB
cpus = 2         # Default: 1. Minimum: 1
disk = "20GB"    # Default: 10GB

[security]
enforcement = "log"  # "log" (allow + log), "fail" (block + log), "silent" (block quietly)

[ide]
command = "code"  # "code", "cursor", "codium", "code-insiders"
```

### Network

```toml
[network]
allow = [
    "registry.npmjs.org",       # Plain domain
    "*.githubusercontent.com",  # Wildcard subdomain
    "example.com:443",          # Domain with port
    "192.168.1.1",              # IP address
]
```

All outbound network is blocked by default. Only listed domains are allowed. DNS and localhost are always permitted.

### Per-Process Network Isolation

```toml
[network]
allow = ["registry.npmjs.org", "pypi.org", "files.pythonhosted.org"]

[network.process]
claude = ["api.anthropic.com", "*.anthropic.com"]
codex = ["api.openai.com"]
aider = ["api.anthropic.com", "api.openai.com"]
```

Rules are **additive** — each process gets the general `allow` list plus its own domains. Processes not listed use only the general rules. Each process runs in its own Linux network namespace.

### Tools (containerized)

```toml
[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]
"golang:1.22" = ["go"]
"rust:latest" = ["cargo", "rustc"]
"ghcr.io/foundry-rs/foundry" = ["forge", "cast", "anvil", "chisel"]
```

Each command becomes a wrapper script running inside the container with the project mounted at `/project`.

### Provision (pre-installed packages)

```toml
[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[provision]
npm = ["@anthropic-ai/claude-code", "typescript"]
pip = ["aider-chat", "black"]
# Also supports: cargo, go, gem
```

Requires the matching tool image in `[tools]`. Packages are baked into a custom container image at provision time.

### Mounts and Ports

```toml
[mounts]
"~/.gitconfig" = { target = "/home/dev/.gitconfig" }
"~/.ssh" = { target = "/home/dev/.ssh", mode = "ro" }  # ro = read-only (default), rw = read-write

[ports]
forward = [3000, 8000, 8080]  # Range: 1-65535
```

Project directory is always mounted at `/project` (read-write).

## Common Configs by Stack

| Stack | Tools | Key Domains | Ports |
|-------|-------|-------------|-------|
| Node/React/Vite | `"node:20-slim"` = `["node", "npm", "npx"]` | `registry.npmjs.org` | 3000, 5173 |
| Python/Django | `"python:3.12-slim"` = `["python", "python3", "pip"]` | `pypi.org`, `files.pythonhosted.org` | 8000 |
| Rust | `"rust:latest"` = `["cargo", "rustc"]` | `crates.io`, `static.crates.io` | — |
| Go | `"golang:1.22"` = `["go"]` | `proxy.golang.org`, `sum.golang.org` | — |
| Foundry | `"ghcr.io/foundry-rs/foundry"` = `["forge", "cast", "anvil", "chisel"]` | `ghcr.io`, `pkg-containers.githubusercontent.com` | 8545 |

**All container images also need Docker registry domains:**
```
registry-1.docker.io
auth.docker.io
production.cloudflare.docker.com
docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com
```

**Full example — AI coding with per-process isolation:**
```toml
[vm]
image = "ubuntu-22.04"

[network]
allow = [
    "registry.npmjs.org",
    "registry-1.docker.io",
    "auth.docker.io",
    "production.cloudflare.docker.com",
]

[network.process]
claude = ["api.anthropic.com", "*.anthropic.com"]
codex = ["api.openai.com"]

[tools]
"node:20-slim" = ["node", "npm", "npx"]

[provision]
npm = ["@anthropic-ai/claude-code"]

[ports]
forward = [3000]

[resources]
memory = "4GB"
cpus = 2

[security]
enforcement = "log"
```

## Troubleshooting

**Network failures after `watermelon exec`:**
1. Run `watermelon logs` to see blocked domains
2. Add legitimate domains to `[network].allow` in `.watermelon.toml`
3. Run `watermelon logs --clear`
4. Destroy and recreate VM: `watermelon destroy --force && watermelon run`
5. Retry the command

**VM not found:** Run `watermelon run` first to create the VM, then use `watermelon exec`.

**Config changes not taking effect:** Network, tool, and port changes require VM reprovisioning: `watermelon destroy --force` then `watermelon run`.

**Port not accessible on host:** Ensure the port is listed in `[ports].forward`. Reprovisioning required for port changes.
