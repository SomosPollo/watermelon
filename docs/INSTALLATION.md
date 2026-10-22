# Installation

[Documentation](README.md) · [Quick start](../README.md#quick-start)

## Requirements

| Requirement | macOS | Linux |
|-------------|-------|-------|
| Host | macOS 13 or newer, Apple Silicon or Intel | ARM64 or x86-64 |
| Lima | Stable 2.0.0 or newer; uses Apple's VZ backend | Stable 2.0.0 or newer; uses QEMU |
| Other tools | `curl` and an OpenSSH client | `curl`, an OpenSSH client, and `qemu-system-aarch64` or `qemu-system-x86_64` for your architecture |

Run Watermelon as your ordinary user. Go is needed only for a Go installation
or a source build. Linux KVM acceleration is recommended; without it, QEMU can
fall back to slower TCG emulation.

## Install a release

Install Lima first. On macOS with Homebrew:

```bash
brew install lima
```

On Linux, install Lima and the architecture-appropriate QEMU system emulator
using your distribution's packages or an upstream Lima package.

Then install Watermelon and check readiness:

```bash
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh | sh
watermelon doctor
```

The installer checks prerequisites before downloading. `doctor` can run from
any directory without creating or modifying a VM. Resolve any `FAIL` checks
before starting a project; see [installation troubleshooting](TROUBLESHOOTING.md#installation-issues).

### Install without root

Choose a writable directory and add it to `PATH`:

```bash
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh \
  | WATERMELON_INSTALL_DIR="$HOME/.local/bin" sh
export PATH="$HOME/.local/bin:$PATH"
```

Add that `export` line to your shell's startup file to keep it across sessions.

### Use a custom QEMU executable on Linux

Preflight resolves and runs `qemu-system-aarch64 --version` or
`qemu-system-x86_64 --version` for Lima's reported host architecture. If Lima
uses a non-default executable, set `QEMU_SYSTEM_AARCH64` or
`QEMU_SYSTEM_X86_64` using Lima's shell-word syntax before running the
installer or `doctor`. Quote a path containing spaces inside the value:

```bash
export QEMU_SYSTEM_AARCH64='"/opt/QEMU tools/qemu-system-aarch64"'
```

## Install with Go

```bash
go install github.com/saeta-eth/watermelon/cmd/watermelon@latest
watermelon doctor
```

Use Go 1.27.3 or newer and make sure the Go binary installation directory is
on `PATH`. This installs only the host CLI. See the next section if you use
interactive `ask` enforcement.

## The ask-mode network interceptor

`watermelon-nfqd` is a Linux sidecar that runs inside the Lima guest only for
`security.enforcement = "ask"`. The `fail`, `silent`, and `log` modes do not
use it.

The shell installer downloads both the host CLI and the matching sidecar for
Lima's default guest architecture. This remains correct when the installer
runs through Rosetta.

For a tagged Go installation, Watermelon derives its release version from the
build information embedded by the Go toolchain. Each new or recreated
`ask`-mode VM downloads the exact same-version
`watermelon-nfqd-linux-<arch>` release asset. Watermelon checks the download
against the SHA-256 digest in GitHub's release metadata, then verifies the
sidecar's embedded Go command, module version, Linux architecture, and
static-release build settings before installing it. Each VM creation download
requires access to GitHub.

Unreleased, locally modified, or custom builds are not matched to a release
asset automatically. For `ask` mode, place a matching regular
`watermelon-nfqd-linux-<Lima-arch>` (or `watermelon-nfqd`) binary beside the
`watermelon` executable, or set `WATERMELON_NFQD_BINARY` to its path.

From a trusted Watermelon source checkout, build that explicit sidecar for
Lima's default guest architecture:

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

## Upgrade existing sandboxes

After upgrading the CLI, check each project's recorded policy:

```bash
watermelon status
```

VMs whose applied-policy snapshot predates the current schema are treated as
unverified. This includes snapshots from before strict defaults, guest UID,
VM mount/workdir settings, or exact provision-script bytes were recorded.
`run`, `exec`, and `code` refuse those VMs until they are recreated.

Preserve any needed VM-local files, then run the exact recreation command
printed by Watermelon from the owning project root. For the default VM name:

```bash
watermelon destroy --force
watermelon run
```

Recreation removes VM-local system state. Mounted host files and managed
workspace disks are retained; `--delete-workspace` is the separate option for
permanently deleting a managed disk. Status compares configuration with a
host-side record; it does not inspect the live firewall.

Continue with the [quick start](../README.md#quick-start) or
[project workflows](WORKSPACES.md).
