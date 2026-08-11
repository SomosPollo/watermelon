---
name: watermelon
description: Use when a .watermelon.toml file exists in the project, when asked to set up sandboxed development environments, or when running package manager commands (npm install, pip install, cargo build) and watermelon is available on the system
---

# Watermelon

## Overview

Watermelon runs developer commands inside a Lima-managed Linux VM on macOS or Linux. Unmounted host credentials and system files stay outside the VM, while the default strict policy blocks non-allowlisted external traffic.

**Core principle:** If `.watermelon.toml` exists in the project, route all build/install/test commands through `watermelon exec` automatically.

## Agent Behavior

**Detection:** Starting in the canonical current directory, check for the
nearest `.watermelon.toml` there or in a physical parent. Check each directory
before stopping at a Git or filesystem boundary. Also stop before a parent that
is other-user-owned or world-writable; accept a root-owned parent only when it
is not group/world-writable. If a config is present, route all package manager
and build commands through `watermelon exec`.

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
- In `ask` mode, let only one `watermelon run`, `exec`, or `code` process control prompts for a VM at a time. Do not use `run --no-shell`; direct SSH does not host prompts
- Keep configured provision-script files present and readable. Watermelon treats their exact host bytes as policy input before status and execution
- Treat `watermelon copy` as an explicit low-level operation: it coordinates the VM name but does not check project ownership, so target only a VM the user owns and expect stop/destroy to interrupt a live transfer and potentially leave a partial destination

## CLI Quick Reference

| Command | Purpose |
|---------|---------|
| `watermelon init` | Create `.watermelon.toml` template |
| `watermelon doctor [--json]` | Check host and Lima readiness without changing a VM |
| `watermelon run [--name NAME] [--workdir PATH]` | Open interactive shell in sandbox |
| `watermelon exec [--name NAME] -- <cmd>` | Run command in sandbox (default for all commands) |
| `watermelon code [--name NAME]` | Open IDE and remain foreground until its remote window exits |
| `watermelon status [--name NAME]` | Show VM status for the resolved project |
| `watermelon list` | List all watermelon VMs |
| `watermelon stop [--name NAME]` | Stop VM immediately, preserve VM-local state |
| `watermelon destroy [--name NAME] [--force]` | Stop and permanently delete VM state |
| `watermelon logs [--name NAME] [--clear]` | Show/clear network policy events |
| `watermelon copy [-r] <src> <dst>` | Copy between host and one explicit `vm-name:path` operand |

Normal `--name` operation still validates the resolved project's config and ownership. `stop` is an immediate fail-closed interrupt. `destroy` also stops a running VM immediately, then waits for terminated Watermelon shell/exec/IDE/copy clients to detach before deleting identity and VM state; `--force` skips confirmation only.

**Installation (if not available):**
```bash
# Requires curl, an OpenSSH client, and stable Lima 2.0.0 or newer.
# macOS 13 or newer: brew install lima
# Linux: install Lima plus the architecture-appropriate QEMU system emulator.
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh | sh
watermelon doctor
```

## Config Reference (`.watermelon.toml`)

### VM and Resources

```toml
[vm]
image = "ubuntu-22.04"  # ubuntu-24.04 is also supported
# name = "my-project-vm"  # Optional fixed, project-owned Lima name
# mount_project = true     # Set false to exclude the host project
# workdir = "/project"     # Must already exist in the guest

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
command = "code"  # Must support --remote and --wait
# workdir = "/project"  # Optional IDE-only existing guest directory
```

An explicit `vm.workdir`, `ide.workdir`, or `run --workdir` path must already exist. Watermelon validates it but does not create it; for a no-mount VM, create a custom path during root provisioning with ownership for the `watermelon` guest user. Fixed names are owned by the canonical creating project and are not shared aliases. A flag overrides `vm.name` for one command but does not bypass a missing/invalid config or another project's ownership.

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

`ask` requires one foreground Watermelon prompt controller per VM. Interactive `run` holds it until the shell exits, `exec` until the command exits, and `code` passes `--wait` and holds it until the remote IDE window exits. A second ask controller cannot bind the saved verdict port. Direct SSH hosts no prompt server and holds no Watermelon usage lease.

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

Each command becomes a wrapper script running inside the container. With the default project mount, it binds and uses `/project`. With `mount_project = false`, it never refers to `/project`: it binds a configured existing `vm.workdir`, or resolves and binds the wrapper's current guest directory when no workdir is configured.

### Provision (pre-installed packages)

```toml
[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[provision]
npm = ["@anthropic-ai/claude-code", "typescript"]
pip = ["aider-chat", "black"]
scripts = ["./vm/setup.sh"]
# Also supports: cargo, go, gem
```

Package fields require the matching tool image in `[tools]`. Packages are baked into a custom container image at provision time, after workload policy is active; required registries and download hosts must be allowed in blocking modes. Use `[provision]` and recreate the VM for reliable global CLI wrappers—an ad-hoc install does not automatically expose every new executable in the guest command path.

`scripts` names current-user-owned, regular UTF-8 host files beneath the project with no symlink or `..` path component. Watermelon embeds their exact bytes and runs them as root after its network policy is active. Scripts must be idempotent and are limited to 1 MiB each and 4 MiB total. Keep the host files present, readable, and current after creation: `status`, `run`, `exec`, and `code` reread them; invalid preparation refuses execution and may fail-closed stop a bound VM.

### Mounts and Ports

```toml
[mounts]
"~/.gitconfig" = { target = "/mnt/watermelon/gitconfig" }
"~/.ssh" = { target = "/mnt/watermelon/ssh", mode = "ro" }  # ro = read-only (default), rw = read-write

[ports]
forward = [3000, 8000, 8080]  # Range: 1-65535
```

Additional mount targets must be `/mnt/watermelon` or a descendant so they cannot shadow guest system, home, policy, or project paths. Point tools explicitly at mounted configuration files. The project directory is separately mounted read-write at `/project` by default and is absent with `vm.mount_project = false`; Watermelon may still mount narrow bootstrap and per-VM log state for its own operation.

Registered fixed-name, no-mount, and ask VMs resolve log storage through per-VM host state; legacy path-derived mounted VMs use the project's `.watermelon/logs.log`. Ask decisions appear through the prompt rather than as policy events. Always use `watermelon logs` so ownership and the correct location are resolved. `watermelon copy` is the exception to normal ownership checks: it accepts one explicit `vm-name:path` and does not read project config, but it does coordinate the name and hold a shared usage lease. Stop/destroy may interrupt the transfer and leave a partial destination; destroy then waits for the copy client to detach before cleanup or name reuse.

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
4. Explain that recreation deletes VM-local state and obtain the user's explicit request before destroying
5. In `fail`, `log`, or `silent`, recreate with `watermelon destroy --force && watermelon run --no-shell`; in `ask`, use `watermelon destroy --force && watermelon run` and keep the shell open
6. Retry the command

In `fail`, policy events represent blocked traffic. In discovery mode (`log`), they represent traffic that was observed and allowed.

**VM not found:** Run `watermelon run` first to create the VM, then use `watermelon exec`.

**Config changes not taking effect:** Network, tool, port, workdir, mount, and provision changes require VM reprovisioning. After the user explicitly authorizes destruction, use `watermelon destroy --force`; recreate with `watermelon run --no-shell` in `fail`/`log`/`silent`, or foreground `watermelon run` in `ask`.

**Configured workdir is missing:** Add an idempotent root provision script that creates it with `watermelon:watermelon` ownership, then ask before recreating. A one-off `run --workdir` must already exist.

**Provision script cannot be read:** Restore the configured current-user-owned host file, or update the config and ask before recreating. Do not bypass the fail-closed verification.

**Ask prompt controller reports address in use:** Another ask-mode `run`, `exec`, or `code` controller already owns the VM's saved verdict port. Wait for it to exit; do not start a parallel prompt controller.

**Port not accessible on host:** Ensure the port is listed in `[ports].forward`. Reprovisioning required for port changes.
