# Installation

## Requirements

| | macOS | Linux |
|-|-------|-------|
| Host | macOS 13+, Apple Silicon or Intel | ARM64 or x86-64 |
| Lima | Stable 2.0.0+, Apple VZ backend | Stable 2.0.0+, QEMU backend |
| Tools | `curl`, OpenSSH | `curl`, OpenSSH, architecture-matching QEMU system emulator |

Run as your ordinary user. Linux KVM acceleration is recommended; QEMU can
fall back to slower emulation. Go is needed only for Go/source installations.

## Install a release

Install Lima first. On macOS:

```bash
brew install lima
```

On Linux, install Lima and `qemu-system-aarch64` or `qemu-system-x86_64` for your
architecture using distro or upstream packages. Then, on either platform:

```bash
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh | sh
watermelon doctor
```

Resolve `FAIL` checks before creating a sandbox. The installer also checks
prerequisites before downloads. Continue with the [quick start](../README.md#quick-start).

### Install without root

```bash
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh \
  | WATERMELON_INSTALL_DIR="$HOME/.local/bin" sh
export PATH="$HOME/.local/bin:$PATH"
```

Keep the `export` in your shell startup file.

### Custom QEMU executable

On Linux, set `QEMU_SYSTEM_AARCH64` or `QEMU_SYSTEM_X86_64` before the installer
or `doctor`. Values use Lima's shell-word syntax; quote paths with spaces
inside the value:

```bash
export QEMU_SYSTEM_AARCH64='"/opt/QEMU tools/qemu-system-aarch64"'
```

## Install with Go

```bash
go install github.com/saeta-eth/watermelon/cmd/watermelon@latest
watermelon doctor
```

Requires Go 1.27.3+ and its binary installation directory on `PATH`. This
installs the host CLI; `ask` enforcement also needs the sidecar below.

## The ask-mode network interceptor

Only `ask` uses the Linux guest sidecar, `watermelon-nfqd`. The shell installer
includes the matching Lima-architecture binary, including under Rosetta.
Tagged Go builds download their exact matching release sidecar when creating
an ask-mode VM, verifying GitHub's SHA-256 metadata and embedded build identity.
This requires GitHub access.

Unreleased/modified builds need an explicit matching regular binary beside the
CLI (`watermelon-nfqd-linux-<arch>` or `watermelon-nfqd`), or at
`WATERMELON_NFQD_BINARY`. From a trusted source checkout:

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

After upgrading, run `watermelon status` in each project. Old or incompatible
policy records are unverified; checked execution refuses them until recreation.
Preserve needed VM-local system files and follow the exact VM-pinned command
printed by Watermelon. Normal recreation retains host files and managed disks;
`--delete-workspace` separately deletes managed source and edits.

For assistant integrations, rerun `agents install` after preserving any
conflicting installed skill, then start a new assistant session. See
[troubleshooting](TROUBLESHOOTING.md) if installation or readiness fails.
