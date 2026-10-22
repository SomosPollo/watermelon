# Watermelon

**Sandbox for development.** Runs third-party code in a Linux VM, away from unmounted host files and services.

## Why?

Modern development runs third-party code constantly — installing packages, running dev servers, building, testing. This code executes with your full user privileges: it can read your SSH keys, access your cloud credentials, browse your filesystem, and make network requests anywhere.

You can't audit it. A typical project has hundreds of dependencies, each with their own dependencies. The code changes with every update. Even if you could read it all, malicious code is designed to hide.

The practical defense is isolation: run third-party code away from host credentials and system files, and constrain the outbound paths it can use.

Watermelon provides a Linux VM where your project runs normally, the unmounted host filesystem is inaccessible, and strict network policy blocks non-allowlisted outbound traffic. Workload DNS is routed through a managed resolver; loopback, established response traffic, and scoped DHCPv4 lease traffic required by VM control networking remain available. The DHCP exception is not general external UDP access. Configured mounts and allowed destinations also remain reachable.

## How It Works

Managed clones keep their source and dependencies on a guest disk. Automatic
setup detects Node, Python, Go, Rust, JVM, .NET, Ruby, PHP, and C/C++ projects,
including monorepos, and prepares their toolchains and dependencies together in one sandbox. Existing
local projects use the mounted-directory workflow shown below; that workflow
exposes the host project read-write by default.

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

Install Lima and Watermelon, then check host readiness:

```bash
# Requirements: curl, an OpenSSH client, and stable Lima 2.0.0 or newer
# macOS 13 or newer: brew install lima
# Linux: install Lima plus the architecture-appropriate QEMU system emulator
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh | sh
watermelon doctor
```

To try an untrusted public GitHub project, clone it directly into a managed
Linux workspace:

```bash
watermelon clone https://github.com/owner/project
cd project
watermelon exec npm run dev    # Or the project's documented command
watermelon code
```

`clone` checks out source inside the VM, detects supported languages and package
managers (including mixed-language projects), and
shows a setup plan before installing dependencies. The host `project` directory
contains settings; source code, dependencies, and edits stay on a separate guest
disk. VM rebuilds preserve that disk. Use `--no-setup` to inspect source first,
or `--yes` to approve the displayed setup plan in automation. Private repositories
and unsupported toolchains require manual configuration in this release.

See [managed workspace workflows](docs/WORKSPACES.md) for runtime selection,
editing, forwarding ports, exporting files, recovery, and permanent deletion.

For an existing local project, the host project remains mounted read-write:

Before the first run—and after changes—review `.watermelon.toml` as trusted host policy, not sandboxed project code. It controls enforcement and allow rules, host mounts, container images, provisioning, and forwarded ports. Watermelon refuses applied-config drift for an existing VM, but that check does not make an attacker-authored configuration safe.

```bash
cd your-project
watermelon init                      # Detect supported languages and managers
# Review the generated .watermelon.toml
# security.enforcement = "fail" is the strict default

watermelon up                        # Review setup and install dependencies
watermelon run                       # Enter the project runtime
exit
```

The shell installer downloads both the host CLI and the matching Linux
`watermelon-nfqd` sidecar for Lima's default guest architecture. This remains
correct when the installer itself runs through Rosetta. The sidecar runs inside
the Lima guest only when `security.enforcement = "ask"`; `fail`, `silent`, and
`log` do not use it.

On Linux, preflight also resolves and runs `qemu-system-aarch64 --version` or
`qemu-system-x86_64 --version` for Lima's reported host architecture. If Lima
uses a non-default executable, set `QEMU_SYSTEM_AARCH64` or
`QEMU_SYSTEM_X86_64` using Lima's shell-word syntax before running the
installer. Quote a path containing spaces inside the value, for example
`QEMU_SYSTEM_AARCH64='"/opt/QEMU tools/qemu-system-aarch64"'`.

**Alternative:** install with Go directly:

```bash
go install github.com/saeta-eth/watermelon/cmd/watermelon@latest
```

For a tagged Go installation, Watermelon derives its release version from the
build information embedded by the Go toolchain. Because `go install` installs
only the host CLI, each new or recreated `ask`-mode VM downloads the exact
same-version `watermelon-nfqd-linux-<arch>` release asset. Watermelon checks the
download against the SHA-256 digest in GitHub's release metadata, then verifies
the sidecar's embedded Go command, module version, Linux architecture, and
static-release build settings before installing it. Each VM creation download
requires access to GitHub; non-`ask` modes do not download or require the
sidecar.

Unreleased, locally modified, or custom builds are not matched to a release
asset automatically. For `ask` mode, place a matching regular
`watermelon-nfqd-linux-<Lima-arch>` (or `watermelon-nfqd`) binary beside the
`watermelon` executable, or set `WATERMELON_NFQD_BINARY` to its path.

From a trusted Watermelon source checkout, build that explicit sidecar for
Lima's default guest architecture with:

```bash
case "$(limactl info --yq '.hostArch')" in
  aarch64|arm64) nfqd_arch=arm64 ;;
  x86_64|amd64)  nfqd_arch=amd64 ;;
  *) echo "unsupported Lima architecture" >&2; exit 1 ;;
esac
CGO_ENABLED=0 GOOS=linux GOARCH="$nfqd_arch" \
  go build -o "watermelon-nfqd-linux-$nfqd_arch" ./cmd/watermelon-nfqd
export WATERMELON_NFQD_BINARY="$PWD/watermelon-nfqd-linux-$nfqd_arch"
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
| `watermelon clone <github-url>` | Clone and prepare a managed guest workspace |
| `watermelon up` | Create/start the environment and prepare approved dependencies |
| `watermelon rebuild` | Replace the VM while retaining managed workspace data |
| `watermelon config validate` | Validate host settings without changing the VM |
| `watermelon doctor` | Check host and Lima readiness; add `--json` for automation |
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

Project-scoped commands can then run from that directory or a descendant that
can reach it without crossing a Git, filesystem, or untrusted-directory
boundary (see [File Location](./docs/CONFIG_SPEC.md#file-location)). Watermelon
discovers the nearest ancestor config and treats its canonical directory as the
project root. The entire resolved root—not only the directory where the command
was invoked—is mounted at `/project` by default, and the default guest workdir
remains `/project` rather than mirroring the host invocation subdirectory.
`watermelon status` prints the resolved path as `Project:`.

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

## Release History

See [CHANGELOG.md](./CHANGELOG.md) for every Watermelon release and its upgrade
notes.

## Development

Development requires Go 1.27.3 or newer (including current standard-library security fixes).

```bash
go build -o watermelon ./cmd/watermelon
go test ./...
go test -race ./...
go vet ./...
WATERMELON_TEST_REAL_LIMA=1 go test -count=1 ./internal/lima  # Installed-Lima contracts; no VM boot
go test -tags=e2e -short -count=1 ./test/...  # CLI workflow without a VM; requires Lima
go test -tags=e2e ./test/...  # CLI and full VM e2e; Linux normally requires usable /dev/kvm
```

The package suite checks config and project detection, generated policy, host
state, authenticated verdicts, and CLI lifecycle orchestration. CLI and Lima
tests that replace package-level command seams or change process environment
must remain serial; restore overrides with `t.Cleanup`.

Real Lima parsing and templating checks are opt-in, so installing Lima does
not change ordinary unit-test behavior. The CI Lima compatibility matrix
enables them for every supported boundary version, and an explicit opt-in
fails if Lima is missing. `WATERMELON_TEST_REAL_LIMA_INFO=1` remains a legacy
alias. Full VM E2E tests verify the actual filesystem, privilege, networking,
and persistence boundaries; generated-script assertions alone cannot prove
those boundaries.

The Lima configuration template lives in
`internal/lima/templates/lima.yaml.tmpl`. It and the helpers under
`internal/lima/runtime/` are embedded at build time; edit these source assets
directly without a separate generation step.

Managed-workspace coverage can also be run separately:

```bash
go test -tags=e2e -run '^TestE2EManagedWorkspaceProjects$' -v ./test/...
go test -tags=e2e -run '^TestE2EManagedWorkspaceRuntime$' -v ./test/...
go test -tags=e2e -run '^TestE2EManagedWorkspaceMultiLanguage$' -v ./test/...
go test -tags=e2e -run '^TestE2EExtendedLanguages$' -timeout=65m -v ./test/...
```

The first tests live public GitHub acquisition. The second uses controlled
guest-only source fixtures to isolate runtime, policy, rebuild, forwarding,
and export checks from GitHub transport availability. The third verifies all
four languages together, cross-language hooks, nested dependencies, restart
and rebuild persistence. The extended test adds JVM, .NET, Ruby, PHP, C/C++,
pnpm/Yarn, and uv/Poetry in one VM. All require real VMs
and network access for trusted images and package registries.

Set `WATERMELON_E2E_ALLOW_TCG=1` to try the full Linux VM lifecycle under slow QEMU TCG when KVM is unavailable.
CI sets `WATERMELON_E2E_REQUIRE_REAL_VM=1` so missing prerequisites fail instead of silently skipping the security scenarios.

## License

MIT
