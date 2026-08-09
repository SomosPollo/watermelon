---
name: watermelon
description: Use when a .watermelon.toml file exists in the project, when asked to set up sandboxed development environments, or when running package manager commands (npm install, pip install, cargo build) and watermelon is available on the system
---

# Watermelon

## Overview

Watermelon runs developer commands inside a Lima-managed Linux VM on macOS or Linux. Unmounted host credentials and system files stay outside the VM, while the default strict policy blocks non-allowlisted external traffic.

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
- Never call `watermelon stop` or `watermelon destroy` unless the user asks — VMs persist intentionally so project dependencies and VM-local state survive between sessions
- Treat interactive and `watermelon exec` shells as unprivileged. Do not use `sudo`; declare tools and global packages in config and recreate when needed
- If `watermelon exec` fails with a network error, run `watermelon logs` to inspect network policy events, then help the user add only trusted, required destinations to `[network].allow`

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
| `watermelon logs [--clear]` | Show/clear network policy events |

**Installation (if not available):**
```bash
# Install limactl from Lima first.
# macOS: brew install lima
# Linux: install Lima with your distro package manager or upstream package.
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
# "fail": strict default (block + rate-limited policy events)
# "log": discovery (allow + rate-limited IPv4 policy events; IPv6 not captured)
# "silent": strict, quiet (block without policy events)
# "ask": prompt for non-allowlisted TCP; reject other non-allowlisted traffic
enforcement = "fail"

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

The default `fail` mode blocks new non-allowlisted external traffic and records rate-limited policy events. Workload DNS is redirected to a managed resolver that answers only policy names. Loopback, established/related responses, and scoped DHCPv4 lease traffic required by VM control networking remain available; the DHCP exception is not arbitrary external UDP access. `silent` has the same strict DNS behavior without policy events. Use `log` only for discovery because it allows non-allowlisted traffic and resolves arbitrary names; its rate-limited events cover IPv4 only. `ask` also resolves arbitrary names so it can prompt.

In `fail`/`silent`, exact domain names are resolved once during trusted VM bootstrap; recreate the VM to refresh changed addresses. Wildcards resolve subdomains dynamically and do not include the apex. Per-process resolvers combine general and process rules. IPv6 is disabled in `fail`, `silent`, and `ask` because enforcement is currently IPv4-only; `log` leaves IPv6 enabled and does not capture it in policy events.

### Per-Process Network Isolation

```toml
[network]
allow = ["registry.npmjs.org", "pypi.org", "files.pythonhosted.org"]

[network.process]
claude = ["api.anthropic.com", "*.anthropic.com"]
codex = ["api.openai.com"]
aider = ["api.anthropic.com", "api.openai.com"]
```

Rules are **additive** — each process gets the general `allow` list plus its own domains. Processes not listed use only the general rules. Each configured process is launched through a root-owned helper into its own Linux network namespace. Watermelon narrowly authorizes only that helper; general passwordless `sudo` is removed, and namespace/helper names are internal. `fail`, `silent`, and `ask` enforce the rules; discovery mode (`log`) observes and allows non-allowlisted traffic.

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

Requires the matching tool image in `[tools]`. Packages are baked into a custom container image at provision time, after workload policy is active; required registries and download hosts must be allowed in blocking modes. Use `[provision]` and recreate the VM for reliable global CLI wrappers—an ad-hoc install does not automatically expose every new executable in the guest command path.

### Mounts and Ports

```toml
[mounts]
"~/.gitconfig" = { target = "/mnt/watermelon/gitconfig" }
"~/.ssh" = { target = "/mnt/watermelon/ssh", mode = "ro" }  # ro = read-only (default), rw = read-write

[ports]
forward = [3000, 8000, 8080]  # Range: 1-65535
```

Additional mount targets must be `/mnt/watermelon` or a descendant so they cannot shadow guest system, home, policy, or project paths. Point tools explicitly at mounted configuration files. The project directory is separately mounted at `/project` (read-write).

## Common Configs by Stack

| Stack | Tools | Key Domains | Ports |
|-------|-------|-------------|-------|
| Node/React/Vite | `"node:20-slim"` = `["node", "npm", "npx"]` | `registry.npmjs.org` | 3000, 5173 |
| Python/Django | `"python:3.12-slim"` = `["python", "python3", "pip"]` | `pypi.org`, `files.pythonhosted.org` | 8000 |
| Rust | `"rust:latest"` = `["cargo", "rustc"]` | `crates.io`, `static.crates.io` | — |
| Go | `"golang:1.22"` = `["go"]` | `proxy.golang.org`, `sum.golang.org` | — |
| Foundry | `"ghcr.io/foundry-rs/foundry"` = `["forge", "cast", "anvil", "chisel"]` | `github.com`, `*.githubusercontent.com` for installs | 8545 |

Configured base images are pulled during trusted bootstrap before workload policy is activated. Do not add registry domains to `[network].allow` solely for image pulls.

**Full example — AI coding with per-process isolation:**
```toml
[vm]
image = "ubuntu-22.04"

[network]
allow = [
    "registry.npmjs.org",
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
enforcement = "fail"
```

## Troubleshooting

**Network failures after `watermelon exec`:**
1. Run `watermelon logs` to inspect rate-limited network policy events
2. Add legitimate domains to `[network].allow` in `.watermelon.toml`
3. Run `watermelon logs --clear`
4. Destroy and recreate VM: `watermelon destroy --force && watermelon run --no-shell`
5. Retry the command

In `fail`, policy events represent blocked traffic. In discovery mode (`log`), they represent traffic that was observed and allowed.

**VM not found:** Run `watermelon run` first to create the VM, then use `watermelon exec`.

**Config changes not taking effect:** Network, tool, and port changes require VM reprovisioning: `watermelon destroy --force` then `watermelon run --no-shell`.

**Port not accessible on host:** Ensure the port is listed in `[ports].forward`. Reprovisioning required for port changes.
