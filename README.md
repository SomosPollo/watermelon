# Watermelon

**Sandbox for development.** Runs third-party code in a Linux VM, away from unmounted host files and services.

## Why?

Modern development runs third-party code constantly — installing packages, running dev servers, building, testing. This code executes with your full user privileges: it can read your SSH keys, access your cloud credentials, browse your filesystem, and make network requests anywhere.

You can't audit it. A typical project has hundreds of dependencies, each with their own dependencies. The code changes with every update. Even if you could read it all, malicious code is designed to hide.

The practical defense is isolation: run third-party code away from host credentials and system files, and constrain the outbound paths it can use.

Watermelon provides a Linux VM where your project runs normally, the unmounted host filesystem is inaccessible, and strict network policy blocks non-allowlisted outbound traffic. Workload DNS is routed through a managed resolver; loopback, established response traffic, and scoped DHCPv4 lease traffic required by VM control networking remain available. The DHCP exception is not general external UDP access. Configured mounts and allowed destinations also remain reachable.

## How It Works

```
┌─────────────────────────────────────────┐
│          Host (macOS/Linux)             │
│  ~/project/.watermelon.toml             │
└──────────────────┬──────────────────────┘
                   │ Lima mount
                   ▼
┌─────────────────────────────────────────┐
│            VM (Linux)                   │
│  /project/  ← your files (r/w, default) │
│  Network: strict allowlist policy       │
│  (managed DNS + loopback + VM DHCPv4)   │
│  Host filesystem: ISOLATED              │
└─────────────────────────────────────────┘
```

The project mount is enabled by default. Set `vm.mount_project = false` when the guest should have no access to the host project; shells and containerized-tool wrappers then use the configured guest workdir, or the guest's current directory when no workdir is configured. Watermelon does not create an arbitrary configured workdir, so create it during provisioning with ownership suitable for the `watermelon` guest user before commands try to enter it.

## Quick Start

Before the first run—and after changes—review `.watermelon.toml` as trusted host policy, not sandboxed project code. It controls enforcement and allow rules, host mounts, container images, provisioning, and forwarded ports. Watermelon refuses applied-config drift for an existing VM, but that check does not make an attacker-authored configuration safe.

```bash
# Install dependency: limactl from Lima
# macOS: brew install lima
# Linux: install Lima with your distro package manager or upstream package
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh | sh

cd your-project
watermelon init                      # Create .watermelon.toml
# Edit config: uncomment the Node tool and allow registry.npmjs.org
# security.enforcement = "fail" is the strict default

watermelon run                       # Enter sandbox
npm install                          # Runs inside the VM
exit
```

**Alternative:** install with Go directly:
```bash
go install github.com/saeta-eth/watermelon/cmd/watermelon@latest
```

To install without root, choose a writable directory and add it to `PATH`:

```bash
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh \
  | WATERMELON_INSTALL_DIR="$HOME/.local/bin" sh
export PATH="$HOME/.local/bin:$PATH"
```

### Upgrading existing sandboxes

VMs whose applied-policy snapshot predates the current schema—including VMs created before strict-by-default snapshots, guest UID and VM mount/workdir settings, or exact provision-script bytes were recorded—are deliberately treated as unverified. `watermelon run`, `exec`, and `code` require a one-time recreation:

```bash
watermelon destroy --force && watermelon run
```

This deletes VM-local state but preserves the host project. `watermelon status` compares the configured policy with the host-side versioned policy record written after VM creation; it does not probe the live firewall.

## Commands

| Command | Description |
|---------|-------------|
| `watermelon init` | Create `.watermelon.toml` config |
| `watermelon run` | Enter sandbox (creates VM if needed) |
| `watermelon code` | Open an IDE and remain foreground until its remote window exits |
| `watermelon exec <cmd>` | Run command without interactive shell |
| `watermelon stop` | Stop VM (preserves state) |
| `watermelon destroy` | Delete VM and all state |
| `watermelon status` | Show VM status |
| `watermelon list` | List all watermelon VMs |
| `watermelon logs` | Show network policy events |
| `watermelon copy <src> <dst>` | Copy files between the host and a VM |

See [docs/COMMANDS.md](./docs/COMMANDS.md) for detailed usage.

## Configuration

Create `.watermelon.toml` in your project root:

```toml
[vm]
image = "ubuntu-22.04"  # ubuntu-24.04 is also supported
# name = "my-project-vm"  # Optional fixed, project-owned Lima name
# mount_project = true
# workdir = "/project"    # Common run/exec workdir

[network]
allow = ["registry.npmjs.org", "github.com"]

[tools]
"node:20-slim" = ["node", "npm", "npx"]

[ports]
forward = [3000]

[resources]
memory = "4GB"
cpus = 2

[security]
enforcement = "fail"  # Strict: block and record non-allowlisted traffic

[ide]
command = "code"  # Must support VS Code-compatible --remote and --wait
# workdir = "/project"  # Optional IDE-only override
```

Fixed names use Watermelon's lowercase, filesystem-safe subset of Lima's instance-name rules and are limited to 76 bytes. Watermelon binds a custom-named VM to the project that created it and rejects unmanaged-name collisions or lifecycle and log commands from a different project. For normal use, `--name` overrides `vm.name` but does not supply a configless default sandbox. `stop` may still stop a securely verified project-owned VM after a config error, and `destroy --name` can recover or remove stale host state using the durable ownership record.

Configured provision scripts run as root in the VM and remain host-side policy inputs after creation. Keep them present, readable, and unchanged while the VM is in use: Watermelon rereads their exact bytes for status and before policy-checked commands, and refuses or fail-closed stops a VM when that verification cannot be completed.

Interactive `ask` enforcement needs one foreground host prompt controller, so `watermelon run --no-shell` is rejected and only one ask-mode `run`, `exec`, or `code` controller can be active for a VM at a time. Keep interactive `run` open; `exec` prompts for the command's duration; and `code` passes `--wait` and stays foreground until the IDE exits. On Linux, verdicts use the foreground controlling terminal independently of guest stdin; guest input forwarding pauses while a prompt is visible, and redirected `exec` stdin continues only to the guest. Without a foreground controlling terminal, non-allowlisted requests block by default. A direct SSH connection neither hosts prompts nor holds Watermelon's session lease. Outside that ask-mode controller limit, Watermelon shell, command, IDE, and copy clients share the VM without blocking non-destructive commands. Both `stop` and `destroy` stop a running VM immediately, ending or interrupting those clients; `destroy` then waits for them to detach before deleting VM state, protecting later name reuse.

In an `ask` prompt, the observed process is informational. **Always Allow and Save** immediately permits the displayed TCP host and port for every process in the current VM runtime, then adds the bare host to the global `[network].allow` list. That saved host-only rule has no process, protocol, or port scope; managed DNS redirection still applies. The broader rule requires VM recreation. After the current session, preserve any needed VM-local state and run the exact, VM-pinned recreation command printed by Watermelon from the project root; recreation removes VM-local state. Otherwise, the next `run`, `exec`, or `code` detects the stale applied configuration and stops a securely bound running VM before returning the recreation instruction.

See [docs/CONFIG_SPEC.md](./docs/CONFIG_SPEC.md) for full reference.

## Examples

Ready-to-use configs in [`docs/examples/`](./docs/examples/):

| Example | Use Case |
|---------|----------|
| [react-app](./docs/examples/react-app/) | React/Vite |
| [nextjs](./docs/examples/nextjs/) | Next.js |
| [python-django](./docs/examples/python-django/) | Django |
| [python-ml](./docs/examples/python-ml/) | PyTorch/TensorFlow |
| [foundry](./docs/examples/foundry/) | Ethereum (Foundry) |
| [monorepo](./docs/examples/monorepo/) | Node + Python |

```bash
cp docs/examples/react-app/.watermelon.toml ~/my-project/
```

## Security Model

**Reduces exposure to:** host credential theft, arbitrary outbound connections, host persistence, and host resource exhaustion.

**Does not protect against:** malicious code inside the VM, attacks on project files when the default read-write project mount is enabled, or exfiltration through destinations permitted by policy. Strict mode is a constrained network boundary, not a general-purpose containment guarantee.

Interactive shells and `watermelon exec` commands run as the ordinary VM user. Watermelon uses root privileges for system provisioning—including configured `[provision].scripts`—then removes that user's general passwordless `sudo` access. Configured per-process network rules use narrowly authorized, root-owned launcher helpers; they do not grant arbitrary root access.

See [docs/SECURITY.md](./docs/SECURITY.md) for details.

## Troubleshooting

See [docs/TROUBLESHOOTING.md](./docs/TROUBLESHOOTING.md) for common issues.

## Development

```bash
go build -o watermelon ./cmd/watermelon
go test ./...
go test -tags=e2e ./test/...  # CLI e2e; Linux full VM lifecycle requires usable /dev/kvm
```

Set `WATERMELON_E2E_ALLOW_TCG=1` to try the full Linux VM lifecycle under slow QEMU TCG when KVM is unavailable.

## License

MIT
