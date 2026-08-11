# Watermelon Configuration Specification

This document describes the `.watermelon.toml` configuration file format for Watermelon sandboxes.

## Overview

The `.watermelon.toml` file defines how your project's sandbox VM is configured. Place this file in your project's root directory. Parsing is strict: unknown or misspelled keys are rejected at every level, including fields inside individual `[mounts]` entries. Watermelon never silently ignores them and substitutes a safety-sensitive default.

```toml
# Example .watermelon.toml
[vm]
image = "ubuntu-22.04"
# name = "my-project-vm"
# mount_project = true
# workdir = "/project"

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
| `name` | string | path-derived | Optional fixed Lima instance name |
| `image` | string | `"ubuntu-22.04"` | Base OS image for the VM |
| `mount_project` | bool | `true` | Mount the host project read-write at `/project` |
| `workdir` | string | `/project` when mounted; guest login directory otherwise | Common working directory for `run`, `exec`, and tool wrappers |

**Supported images:**

- `ubuntu-22.04`
- `ubuntu-24.04`

```toml
[vm]
name = "my-project-vm"
image = "ubuntu-24.04"
mount_project = true
workdir = "/project"
```

**`name`:** A fixed name overrides the normal `watermelon-{project}-{hash}` name. It must be no more than 76 bytes, must match `[a-z0-9]+(?:[._-][a-z0-9]+)*`, and must not end in `.yaml` or `.yml`. Watermelon deliberately requires lowercase: Lima accepts uppercase, but case variants can alias the same instance directory on common macOS filesystems.

A custom-named VM is not a shared global alias. Watermelon records its canonical owning project and an immutable per-instance identity. It refuses collisions with another project or an unmanaged Lima instance and rechecks ownership before lifecycle operations. For normal operation, CLI `--name` overrides this field only for the current command and never creates or accesses a sandbox using default configuration from an unrelated directory. Recovery is limited to verified ownership: `stop` may stop a bound VM while returning a local config error, and `destroy --name` may recover it or clean stale host state from a missing/invalid-config project.

**`mount_project`:** Set this to `false` to keep the host project out of the VM. No `/project` mount is generated. Dedicated read-only bootstrap state and per-VM log state may still be mounted for Watermelon's own operation; these are not project mounts.

**`workdir`:** This must be a clean absolute Linux path. It controls interactive `run` shells, `exec`, and containerized-tool wrappers. Watermelon validates the path syntax but does not create an arbitrary directory in the guest. Create a non-default workdir during provisioning, with ownership suitable for the ordinary `watermelon` user, before the first shell, command, tool wrapper, or IDE tries to use it. The same pre-existing-directory requirement applies to `run --workdir` and `ide.workdir`.

When omitted with `mount_project = true`, `workdir` defaults to the existing `/project` mount. When omitted with `mount_project = false`, the guest login directory is used by `run` and `exec`, while tool wrappers bind their current guest directory into the tool container at the same path. The `run --workdir` flag overrides the configured value for that interactive shell only.

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

**Note:** Requires VM reprovisioning (`watermelon destroy --force && watermelon run --no-shell`) to apply changes. With `enforcement = "ask"`, omit `--no-shell` and keep the resulting interactive shell open. Destroying the VM removes its state but does not delete `.watermelon.toml`.

---

### `[provision]`

Packages to install and host scripts to embed and run during VM provisioning. Each package-manager key installs packages globally inside its configured tool image.

User-configured package installation runs after workload network policy is active, so its registries and download hosts must be covered by `[network].allow` in blocking modes.

Declare global CLI packages here and recreate the VM when you need reliable command wrappers. Do not rely on an ad-hoc global install to expose a newly introduced executable in the guest command path; wrapper discovery and creation happen during VM provisioning.

| Field | Type | Requires Tool | Install Command |
|-------|------|---------------|-----------------|
| `npm` | string[] | `node` image | `npm install -g <pkg>` |
| `pip` | string[] | `python` image | `pip install <pkg>` |
| `cargo` | string[] | `rust` image | `cargo install <pkg>` |
| `go` | string[] | `go` or `golang` image | `go install <pkg>` |
| `gem` | string[] | `ruby` image | `gem install <pkg>` |
| `scripts` | string[] | none | Embed each host file and run it as root in the VM |

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
scripts = ["./vm/setup.sh"]
```

**Provision scripts:** Paths must be relative to the project root. Absolute paths, every lexical `..` component, and symlinks in any path component are rejected. Watermelon reads the scripts on the host and embeds their contents when generating the Lima configuration; the files do not need to be available inside the VM through `/project`, so this also works with `mount_project = false`. They run as root after Watermelon's built-in provisioning and network policy setup.

Treat every script as trusted configuration with root authority inside the VM. Watermelon refuses an empty or unsafe path, a non-regular file, a file not owned by the current host user, invalid UTF-8, or NUL bytes. Each script is limited to 1 MiB and all scripts together to 4 MiB. Because Lima may run provision steps again, scripts must be idempotent.

Keep every configured host script present, readable, and current while the VM exists. Watermelon rereads and validates the exact bytes before `run`, `exec`, and `code`, and while `status` compares the current and applied configurations. It records those ordered digests in the applied configuration, so changing a script at the same path makes the VM stale and requires recreation. A missing, unreadable, or newly invalid script prevents `status` from completing; policy-checked execution commands refuse to use the VM and may stop a verifiably project-owned running VM fail-closed. The ownership-verified `stop` and `destroy` recovery paths remain available.

For example:

```bash
#!/bin/sh
set -eu

# Safe to run more than once.
install -d -m 0755 /opt/my-project
printf '%s\n' 'managed by Watermelon' > /opt/my-project/README
```

**Use case:** Install AI coding assistants and development tools automatically:

```toml
[provision]
npm = ["@anthropic-ai/claude-code"]  # Claude Code CLI
pip = ["aider-chat"]                  # Aider AI assistant
cargo = ["ripgrep", "fd-find"]        # Fast search tools
scripts = ["./vm/setup.sh"]           # Custom root provisioning
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
   This is the default mounted-project case.
2. With `mount_project = false`, wrappers never refer to `/project`. If `vm.workdir` is configured they bind and use that guest path; otherwise they resolve, bind, and use the wrapper's current guest directory.
3. The `--network=host` flag ensures ports bind to the VM's network.
4. Lima's configured port forwarding exposes those ports to the host.

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

**Note:** With the default `mount_project = true`, the project directory is separately mounted at `/project` with read-write access. It is absent when `mount_project = false`.

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
| `"ask"` | **Interactive:** resolve arbitrary names, prompt for non-allowlisted TCP connections, reject other non-allowlisted traffic, and save explicit bare-host allow choices |

```toml
[security]
# Choose one mode. The strict default is:
enforcement = "fail"

# Alternatives:
# enforcement = "log"     # Discovery: allow; record IPv4 events (IPv6 is not captured)
# enforcement = "silent"  # Strict, quiet: block without policy events
# enforcement = "ask"     # Interactive: prompt for TCP; reject other non-allowlisted traffic
```

`ask` requires a foreground Watermelon process to host its verdict server. `watermelon run --no-shell` is therefore rejected: use interactive `watermelon run` and keep its shell open. `watermelon exec` keeps prompts available until the guest command exits. `watermelon code` passes the configured IDE command `--wait` and remains in the foreground, keeping prompts available until the remote IDE window exits.

The observed process in a prompt is informational. **Always Allow and Save** immediately permits the displayed TCP host and port for every process in the current VM runtime, but persists a broader rule: the bare host is added to the global `[network].allow` list with no process, protocol, or port scope. Managed DNS redirection still applies. Applying that saved rule to future VM sessions requires destroying and recreating the VM after the current Watermelon session. Watermelon prints an exact command pinned to the selected VM name, so a later configuration edit cannot retarget the destructive operation; recreation removes VM-local state. Until then, the applied-policy snapshot is stale. The next `run`, `exec`, or `code` refuses the stale configuration and stops a securely bound running VM before returning the recreation instruction.

On Linux, terminal verdicts use the foreground controlling terminal independently of guest stdin. Interactive guest input forwarding pauses for the duration of a prompt. Redirected `watermelon exec` stdin remains dedicated to the guest, while verdicts continue to use the controlling terminal; without one, non-allowlisted requests block by default. macOS uses a native dialog and does not depend on terminal stdin.

Each ask-mode VM has one saved host verdict port, so only one foreground `run`, `exec`, or `code` prompt controller can be active for it at a time. Start another only after the first exits. Direct `ssh lima-<vmname>` does not start a verdict server or acquire Watermelon's VM usage lease; a manually connected workload needs a separate foreground Watermelon prompt controller for prompted network access, and Watermelon stop or destroy can terminate that connection without waiting for it.

---

### `[ide]`

Configures the IDE for the `watermelon code` command.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `command` | string | `"code"` | VS Code-compatible IDE command supporting `--remote` and `--wait` |
| `workdir` | string | `[vm].workdir`, then the VM default | IDE-only remote directory override |

**Supported IDE commands:**

Each command must support both the VS Code-compatible `--remote ssh-remote+<host>` option and `--wait`. The `--wait` contract is required even outside `ask` mode so Watermelon can retain the VM session lease until the IDE window closes.

| IDE | Command |
|-----|---------|
| VS Code | `code` |
| Cursor | `cursor` |
| VSCodium | `codium` |
| VS Code Insiders | `code-insiders` |

```toml
[ide]
command = "cursor"
workdir = "/workspace/app"
```

**How it works:**

When you run `watermelon code`, it executes:
```bash
<command> --wait --remote ssh-remote+lima-<vmname> <workdir>
```

The remote directory is `ide.workdir` when set, otherwise `vm.workdir`, otherwise `/project` for a mounted project. An explicitly configured remote directory must already exist in the guest. With no project mount and no configured workdir, Watermelon omits `<workdir>` and lets the IDE use the guest login directory. `ide.workdir` does not affect `watermelon run` or `watermelon exec`.

Watermelon runs the IDE command in the foreground. It keeps the shared VM usage lease—and, in `ask` mode, the host prompt server—until the IDE command exits after its remote window closes.

**Security:** The IDE command is validated to prevent shell injection. Both workdir fields must be clean absolute Linux paths and reject shell metacharacters, quotes, control whitespace, and NUL bytes.

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

### Project-Owned VM Without a Project Mount

```toml
[vm]
name = "isolated-build-vm"
image = "ubuntu-24.04"
mount_project = false
workdir = "/home/watermelon"

[network]
allow = []

[security]
enforcement = "fail"
```

Create and manage this VM from the directory containing that configuration. To transfer selected files, use explicit copy syntax such as `watermelon copy -r ./src isolated-build-vm:/home/watermelon/`. Fixed names do not let normal `--name` commands bypass project ownership or config validation; the recovery exceptions remain ownership-verified. `copy` is a separate, explicit low-level Lima operation and does not verify Watermelon ownership or project configuration. It does coordinate the selected VM name and holds a shared usage lease for the transfer, so destroy cannot delete state or reuse the name until the copy client detaches. Stop and destroy may still interrupt the transfer by immediately shutting down a running VM, potentially leaving a partial destination.

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

A present configuration is parsed strictly and validated before Watermelon uses it. Unknown keys—including unknown fields nested in a mount entry—are configuration errors. VM creation and applied-configuration comparison also prepare and validate provision-script files:

1. **VM:**
   - `name`, when set, uses the lowercase filesystem-safe syntax above and the 76-byte limit
   - `image` must be `ubuntu-22.04` or `ubuntu-24.04`
   - `workdir`, when set, must be a clean absolute Linux path

2. **Resources:**
   - `cpus` must be ≥ 1
   - `memory` and `disk` must be non-empty

3. **Security:**
   - `enforcement` must be one of: `log`, `fail`, `silent`, `ask`

4. **Network:**
   - Domains are parsed as plain hosts, wildcard subdomains, IPv4 addresses, or host/IP plus TCP port
   - Wildcard domains cannot include ports

5. **Ports:**
   - Each port must be in range 1-65535

6. **Provisioning:**
   - Package names and script paths reject unsafe shell syntax
   - Script paths must be project-relative, contain no `..` component, and contain no symlink component
   - Scripts must pass the ownership, regular-file, UTF-8, and size checks described above

---

## File Location

Watermelon looks for `.watermelon.toml` in the current working directory. Without `vm.name`, the VM name is derived from the canonical project path for consistency. Normal `--name` operation validates the local configuration, binds the selected name to the current project, and never replaces a missing or invalid config with defaults. `stop` and explicit-name `destroy` have the ownership-verified recovery behavior described above. Management commands retain legacy path-derived lookup only when neither a config nor an explicit name is present.
