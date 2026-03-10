# Watermelon

**Sandbox for development.** Isolates third-party code in a Linux VM so it can't touch your macOS host.

## Why?

Modern development runs third-party code constantly — installing packages, running dev servers, building, testing. This code executes with your full user privileges: it can read your SSH keys, access your cloud credentials, browse your filesystem, and make network requests anywhere.

You can't audit it. A typical project has hundreds of dependencies, each with their own dependencies. The code changes with every update. Even if you could read it all, malicious code is designed to hide.

The only solution is isolation. Run untrusted code in an environment where it physically cannot access your sensitive data or exfiltrate to arbitrary servers.

Watermelon provides this: a Linux VM where your project runs normally, but the host filesystem is inaccessible and network access is limited to domains you explicitly allow.

## How It Works

```
┌─────────────────────────────────────────┐
│            Host (macOS)                 │
│  ~/project/.watermelon.toml             │
└──────────────────┬──────────────────────┘
                   │ virtiofs mount
                   ▼
┌─────────────────────────────────────────┐
│            VM (Linux)                   │
│  /project/  ← your files (r/w)          │
│  Network: allowlist only                │
│  Host filesystem: ISOLATED              │
└─────────────────────────────────────────┘
```

## Quick Start

```bash
brew install lima                    # Install dependency
curl -fsSL https://raw.githubusercontent.com/saeta-eth/watermelon/main/install.sh | sh

cd your-project
watermelon init                      # Create .watermelon.toml
# Edit config: add network.allow = ["registry.npmjs.org"]

watermelon run                       # Enter sandbox
npm install                          # Safe!
exit
```

**Alternative:** install with Go directly:
```bash
go install github.com/saeta-eth/watermelon/cmd/watermelon@latest
```

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
| `watermelon logs` | Show blocked network requests |

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

**Protects against:** credential theft, data exfiltration, persistent backdoors, resource exhaustion.

**Does not protect against:** malicious code inside the VM, attacks on mounted project files.

See [docs/SECURITY.md](./docs/SECURITY.md) for details.

## Troubleshooting

See [docs/TROUBLESHOOTING.md](./docs/TROUBLESHOOTING.md) for common issues.

## Development

```bash
go build -o watermelon ./cmd/watermelon
go test ./...
go test -tags=e2e ./test/...  # Requires Lima
```

## License

MIT
