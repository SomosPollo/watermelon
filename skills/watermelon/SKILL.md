---
name: watermelon
description: Develop projects with a .watermelon.toml using Watermelon to execute project code in a Linux VM. Also use when asked to set up a Watermelon development environment.
---

# Watermelon

## Overview

Watermelon runs developer commands inside a Lima-managed Linux VM on macOS or Linux. Unmounted host credentials and system files stay outside the VM, while the default strict policy blocks non-allowlisted external traffic.

**Core principle:** If `.watermelon.toml` exists in the project, execute project code through `watermelon exec` automatically. This includes dependency installation, builds, tests, scripts, generators, migrations, and development servers. For mounted local projects, read and edit source files with the assistant's normal file tools. When `vm.mount_project = false`, read and edit guest-only source through guest commands; a managed clone's source is at `/workspace/project`, not in the host settings directory.

## Agent Behavior

**Detection:** Starting in the canonical current directory, check for the
nearest `.watermelon.toml` there or in a physical parent. Check each directory
before stopping at a Git or filesystem boundary. Also stop before a parent that
is other-user-owned or world-writable; accept a root-owned parent only when it
is not group/world-writable. If a config is present, route all project code
execution through `watermelon exec`. The resolved config directory selects
the VM; a host subdirectory does not automatically select a guest subdirectory.

```bash
# WRONG - runs on host, defeats sandboxing
npm install
pip install requests
cargo build

# RIGHT - runs inside sandbox
watermelon exec -- npm install
watermelon exec -- pip install requests
watermelon exec -- cargo build
```

**Compound commands** — chain inside a single exec:
```bash
watermelon exec -- sh -c 'npm install && npm run build && npm test'
```

**Rules:**
- Use `watermelon exec` for discrete commands (default)
- Use `watermelon exec --cwd <relative-directory> -- <command>` for a project subdirectory; running the host CLI from a subdirectory alone does not select the guest working directory
- If Watermelon fails, diagnose the failure instead of falling back to host execution
- Review the initial `watermelon up` setup plan with the user; do not add `--yes` without authorization. Ask before changing network policy or mounts
- Use `watermelon run` only when the user explicitly asks for an interactive shell
- Never call `watermelon stop` or `watermelon destroy` unless the user asks — VMs persist intentionally so project dependencies and VM-local state survive between sessions
- Treat interactive and `watermelon exec` shells as unprivileged. Do not use `sudo`; declare tools and global packages in config and recreate when needed
- If `watermelon exec` fails with a network error, run `watermelon logs` to inspect network policy events, then help the user add only trusted, required destinations to `[network].allow`
- In `ask` mode, let only one `watermelon run`, `exec`, or `code` process control prompts for a VM at a time. Do not use `run --no-shell`; direct SSH does not host prompts
- Keep configured provision-script files present and readable. Watermelon treats their exact host bytes as policy input before status and execution
- Treat `watermelon copy` as an explicit low-level operation: it coordinates the VM name but does not check project ownership, so target only a VM the user owns and expect stop/destroy to interrupt a live transfer and potentially leave a partial destination

## Project Setup and Assistant Integration

For an existing local source checkout, run these host commands when setting up
Watermelon is within the user's request:

```bash
watermelon doctor
watermelon init
# Review .watermelon.toml and the initial setup plan before installation.
watermelon config validate
watermelon up
```

`init` detects supported manifests, including nested and mixed-language
projects, and writes an automatic runtime profile and registry allowlist.
It also adds marked guidance to `AGENTS.md` and `CLAUDE.md`, preserving existing
text and existing Watermelon blocks. It refuses an existing config; use
`watermelon init --instructions-only` to add guidance without changing it.
Do not hand-edit generated `[setup]` metadata to bypass a setup failure.

Automatic setup supports Node (npm, pnpm, Yarn), Python (pip, uv, Poetry), Go,
Rust, Java/Kotlin, .NET, Ruby, PHP, and C/C++. Use `init --template <profile>`
to restrict discovery or `--template manual` for custom tools. `up` skips
unchanged dependency installation; use `up --refresh` for removed environments,
indirect dependency inputs, or an intentional reinstall. It displays a new
plan when runtimes or project layout change. `up --runtime <version>` or
`--runtime node=22,python=3.13` selects supported runtime overrides.
Use `exec --cwd` for Python projects so the correct project environment is selected.
Automatic `up` preparation supports `fail`, `silent`, or explicitly selected
`log` enforcement; `ask` uses a foreground `run` session and interactive installation.

For a public repository whose source should stay off the host:

```bash
watermelon clone --no-setup https://github.com/owner/project
cd project
# Inspect guest source using watermelon exec; use up when ready for setup.
watermelon up
```

The destination must not exist. Only credential-free public GitHub HTTPS URLs
are supported. The host destination contains settings, while source and edits
live at `/workspace/project` on a separately retained guest disk. A guest
repository's `.watermelon.toml` is source content, not the trusted host policy.
Use guest commands or a compatible remote IDE to inspect and edit guest source.
`clone` without `--no-setup` also presents the setup plan and prepares approved
dependencies. Its prompt offers `c` to customize runtimes, resources, forwarded
ports and additional network destinations, `e` to edit TOML, and `s` to defer
setup. Customizations are drafts until the revised plan is approved. Initial
`up` on an unapproved managed clone offers the same review. Keep the host
project's config outside guest control.

`watermelon config edit` uses `VISUAL`, then `EDITOR`, then `vi` to edit and
validate a private draft. It preserves the active policy on invalid edits or
concurrent changes and reports the retained draft path. It does not operate on
the VM; VM-affecting edits still require rebuilding. Setup updates preserve
comments and custom settings. Continue to obtain authorization for policy and
mount changes as described above.

When asked to install assistant integrations, use `watermelon agents install`
to detect local Codex and Claude Code installations, or select them explicitly
with `--agent codex`, `--agent claude`, or `--agent all`.
Codex's user skill is `~/.agents/skills/watermelon/SKILL.md`; Claude Code's is
`~/.claude/skills/watermelon/SKILL.md` (or under `CLAUDE_CONFIG_DIR`).
The command installs the skill embedded in the binary without downloads,
leaves identical files unchanged, and refuses differing content. Preserve an
older or customized copy before moving it aside to reinstall. Updating this
repository alone updates neither an older binary nor previously installed
copies; rebuild/install the CLI and reinstall the skill to distribute changes.
Start a new assistant session afterward. The skill and project instructions
guide execution; they do not intercept or restrict the assistant's host shell.

## CLI Quick Reference

| Command | Purpose |
|---------|---------|
| `watermelon agents install [--agent codex,claude]` | Install the shared skill for local assistants |
| `watermelon init [--instructions-only]` | Create configuration and project guidance, or add guidance to an existing config |
| `watermelon clone [--no-setup] <public-github-url> [destination]` | Acquire guest-only source; review setup before dependencies execute |
| `watermelon doctor [--json]` | Check host and Lima readiness without changing a VM |
| `watermelon config validate [--json]` | Validate configuration and provision-script inputs without changing a VM |
| `watermelon config edit` | Edit and validate a host policy draft using VISUAL or EDITOR |
| `watermelon up [--refresh] [--dry-run]` | Review and prepare detected runtimes and dependencies |
| `watermelon rebuild [--name NAME]` | Replace VM system state while retaining host source and managed workspace disks |
| `watermelon run [--name NAME] [--workdir PATH]` | Open interactive shell in sandbox |
| `watermelon exec [--name NAME] [--cwd DIRECTORY] -- <cmd>` | Run a project command in the sandbox |
| `watermelon code [--name NAME]` | Open IDE and remain foreground until its remote window exits |
| `watermelon status [--name NAME] [--json]` | Show VM, recorded policy, and available workspace/setup state |
| `watermelon list` | List all watermelon VMs |
| `watermelon stop [--name NAME]` | Stop VM immediately, preserve VM-local state |
| `watermelon destroy [--name NAME] [--force]` | Delete VM system state; retain mounted host files and managed source disks |
| `watermelon logs [--name NAME] [--clear]` | Show/clear network policy events |
| `watermelon copy [-r] <src> <dst>` | Copy between host and one explicit `vm-name:path` operand |

Normal `--name` operation still validates the resolved project's config and ownership. `stop` is an immediate fail-closed interrupt. `destroy` also stops a running VM immediately, then waits for terminated Watermelon shell/exec/IDE/copy clients to detach before deleting identity and VM state; `--force` skips confirmation only.

Only `destroy --delete-workspace` also permanently deletes a managed source
disk, including uncommitted edits and dependencies. Never add it unless that
data deletion was explicitly requested. `rebuild` also replaces VM-local
system state: preserve needed files outside the retained workspace or mounts
before applying it. `--force` confirms replacement; `--yes` separately approves
a changed setup plan. Use them only when the corresponding actions are authorized.

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

These rules apply to configured assistant CLIs running inside the guest. A host
Codex or Claude Code session keeps its host API connection; using `agents install`
does not require adding assistant API domains to the guest's allowlist.

### Tools (containerized)

```toml
[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]
"rust:latest" = ["cargo", "rustc"]
```

For manual configurations, each declared command becomes a wrapper script running inside the container. With the default project mount, it binds and uses `/project`. With `mount_project = false`, it never refers to `/project`: it binds a configured existing `vm.workdir`, or resolves and binds the wrapper's current guest directory when no workdir is configured.

Mounted-project manual wrappers do not inherit the guest shell's current
subdirectory, environment variables, or Python virtual-environment activation.
Use automatic setup for Python and mixed-language project workflows. Images
with shell entrypoints may discard arguments; use the explicit-entrypoint
helper in the [Foundry example](https://github.com/SomosPollo/watermelon/blob/main/docs/examples/foundry/README.md).

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

Global package fields are for manual configurations and require the matching tool image in `[tools]`; they cannot be combined with automatic setup profiles. Packages are baked into a custom container image at provision time, after workload policy is active; required registries and download hosts must be allowed in blocking modes. Use `[provision]` and rebuild the VM for reliable global CLI wrappers—an ad-hoc install does not automatically expose every new executable in the guest command path.

`scripts` names current-user-owned, regular UTF-8 host files beneath the project with no symlink or `..` path component. Watermelon embeds their exact bytes and runs them as root after its network policy is active. Scripts must be idempotent and are limited to 1 MiB each and 4 MiB total. Keep the host files present, readable, and current after creation: `status`, `run`, `exec`, and `code` reread them; invalid preparation refuses execution and may fail-closed stop a bound VM.

### Mounts and Ports

```toml
[mounts]
"~/.gitconfig" = { target = "/mnt/watermelon/gitconfig" }
"~/.ssh" = { target = "/mnt/watermelon/ssh", mode = "ro" }  # ro = read-only (default), rw = read-write

[ports]
forward = [3000, 8000, 8080]  # Range: 1-65535
```

Additional mount targets must be descendants of `/mnt/watermelon`; the root
itself and its `bootstrap` and `state` subtrees are reserved. Mounts expose
files to the guest, not automatically to tool containers. The project directory
is separately mounted read-write at `/project` by default and is absent with
`vm.mount_project = false`; Watermelon may still mount narrow bootstrap and
per-VM log state for its own operation.

Managed-workspace logs live on the retained guest data disk and require a running VM to read or clear. Other registered VMs resolve log storage through per-VM host state; legacy path-derived mounted VMs use the project's `.watermelon/logs.log`. Ask decisions appear through the prompt rather than as policy events. Always use `watermelon logs` so ownership and the correct location are resolved. `watermelon copy` is the exception to normal ownership checks: it accepts one explicit `vm-name:path` and does not read project config, but it does coordinate the name and hold a shared usage lease. Stop/destroy may interrupt the transfer and leave a partial destination; destroy then waits for the copy client to detach before cleanup or name reuse.

For a development server, forward its guest port in `[ports].forward` and
bind the application to `0.0.0.0` in the guest. Forwarded ports bind to
`127.0.0.1` on the host; run the server through `watermelon exec`.

## Common Configs by Stack

| Stack | Setup | Key Domains | Ports |
|-------|-------|-------------|-------|
| Node/React/Vite | `init --template node` | `registry.npmjs.org` | 3000, 5173 |
| Python/Django | `init --template python` | `pypi.org`, `files.pythonhosted.org` | 8000 |
| Rust | `init --template rust` | `index.crates.io`, `static.crates.io` | — |
| Go | `init --template go` | `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` | — |
| Foundry | [Manual explicit-entrypoint helper](https://github.com/SomosPollo/watermelon/blob/main/docs/examples/foundry/README.md) | `github.com`, `raw.githubusercontent.com`, `binaries.soliditylang.org` | 8545 |

Configured base images are pulled during trusted bootstrap before workload policy is activated. Do not add registry domains to `[network].allow` solely for image pulls.

**Manual example — an assistant CLI inside the VM with per-process isolation:**
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
2. Identify required, trusted destinations and obtain authorization for any policy change
3. Update the host `.watermelon.toml` and run `watermelon config validate`
4. Preserve needed VM-local files, then apply the approved configuration with `watermelon rebuild` in `fail`/`silent`/`log`; in `ask`, follow the CLI's VM-pinned recovery instructions and recreate with a foreground prompt controller
5. Retry the command; retain logs unless clearing them was requested

In `fail`, policy events represent blocked traffic. In discovery mode (`log`), they represent traffic that was observed and allowed.

**VM not found:** For an automatic profile, use `watermelon up` to prepare the approved environment; ordinary `exec` also ensures setup outside `ask` mode. For a manual config, create/start the VM with `watermelon run --no-shell` in `fail`/`log`/`silent`, or a foreground `watermelon run` in `ask`. Then retry `exec`.

**Config changes not taking effect:** Validate the config and inspect `watermelon status` (or `--json`). VM-affecting network, tool, port, workdir, mount, and provision changes require `watermelon rebuild`; stop/start alone does not apply them. IDE-only settings do not require rebuilding. Rebuild preserves mounted host source and managed workspace disks, but replaces VM-local system state. In `ask`, follow Watermelon's exact VM-pinned recovery instruction when a saved verdict makes the applied policy stale; ask-mode recreation needs a foreground prompt controller.

**Dependencies or languages changed:** Run `watermelon up` and review its proposed runtime/project changes. Use `up --refresh` when manifests are unchanged but dependency environments need reinstalling. Malformed or conflicting runtime requirements must be resolved rather than bypassed with host installs.

**Configured workdir is missing:** With authorization, add an idempotent provision script that creates it with `watermelon:watermelon` ownership, validate the config, and rebuild. A one-off `run --workdir` must already exist.

**Provision script cannot be read:** Restore the configured current-user-owned host file, or apply an authorized config change and rebuild. Do not bypass the fail-closed verification.

**Ask prompt controller reports address in use:** Another ask-mode `run`, `exec`, or `code` controller already owns the VM's saved verdict port. Wait for it to exit; do not start a parallel prompt controller.

**Port not accessible on host:** Check `[ports].forward` and the application's guest bind address. Port-forward changes require a rebuild; do not grant additional mounts or network access to troubleshoot a bind-address problem.

For flags and manual setup details, consult the
[command reference](https://github.com/SomosPollo/watermelon/blob/main/docs/COMMANDS.md),
[project workflows](https://github.com/SomosPollo/watermelon/blob/main/docs/WORKSPACES.md),
and [configuration reference](https://github.com/SomosPollo/watermelon/blob/main/docs/CONFIG_SPEC.md).
