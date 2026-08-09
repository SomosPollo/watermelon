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
│  /project/  ← your files (r/w)          │
│  Network: strict allowlist policy       │
│  (managed DNS + loopback + VM DHCPv4)   │
│  Host filesystem: ISOLATED              │
└─────────────────────────────────────────┘
```

## Quick Start

Before the first run—and after changes—review `.watermelon.toml` as trusted host policy, not sandboxed project code. It controls enforcement and allow rules, host mounts, container images, provisioning, and forwarded ports. Watermelon refuses applied-config drift for an existing VM, but that check does not make an attacker-authored configuration safe.

```bash
# Install dependency: limactl from Lima
# macOS: brew install lima
# Linux: install Lima with your distro package manager or upstream package
curl -fsSL https://raw.githubusercontent.com/saeta-eth/watermelon/main/install.sh | sh

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

### Upgrading existing sandboxes

VMs created before strict-by-default policy snapshots are deliberately treated as unverified. `watermelon run`, `exec`, and `code` require a one-time recreation:

```bash
watermelon destroy --force && watermelon run
```

This deletes VM-local state but preserves the host project. `watermelon status` compares the configured policy with the host-side versioned policy record written after VM creation; it does not probe the live firewall.

## Commands

| Command | Description |
|---------|-------------|
| `watermelon init` | Create `.watermelon.toml` config |
| `watermelon run` | Enter sandbox (creates VM if needed) |
| `watermelon code` | Open IDE connected to sandbox via SSH |
| `watermelon exec <cmd>` | Run command without interactive shell |
| `watermelon stop` | Stop VM (preserves state) |
| `watermelon destroy` | Delete VM and all state |
| `watermelon status` | Show VM status |
| `watermelon list` | List all watermelon VMs |
| `watermelon logs` | Show network policy events |

See [docs/COMMANDS.md](./docs/COMMANDS.md) for detailed usage.

## Configuration

Create `.watermelon.toml` in your project root:

```toml
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
command = "code"  # or "cursor", "codium"
```

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

**Does not protect against:** malicious code inside the VM, attacks on mounted project files, or exfiltration through destinations permitted by policy. Strict mode is a constrained network boundary, not a general-purpose containment guarantee.

Interactive shells and `watermelon exec` commands run as the ordinary VM user. Watermelon uses root privileges for system provisioning, then removes that user's general passwordless `sudo` access. Configured per-process network rules use narrowly authorized, root-owned launcher helpers; they do not grant arbitrary root access.

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
