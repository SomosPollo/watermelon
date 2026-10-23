# Watermelon

**Run development commands in a Linux VM.** Install dependencies, build, test,
and serve your app with control over the files and network destinations it can
reach. Watermelon manages the sandbox through Lima on macOS and Linux.

## Quick start

### 1. Install

You need macOS 13+ or Linux, stable Lima 2.0.0+, `curl`, and OpenSSH. Linux
also needs QEMU for your architecture. See [installation](docs/INSTALLATION.md)
for Linux setup, other install methods, and upgrades.

```bash
# macOS
brew install lima

# Either platform, after installing prerequisites
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh | sh
watermelon doctor
```

Resolve any `FAIL` checks before continuing. The first VM takes several minutes
to download and prepare.

### 2. Prepare a project

**Existing local project:** run from its root on the host.

```bash
watermelon init
# Optional: customize the detected settings; saving validates them
watermelon config edit
watermelon up
```

`init` detects your languages; `up` shows a plan and asks before installing
dependencies. Your entire project is mounted read-write at `/project`, so guest
commands can change those files and read any secrets stored there.

**Public GitHub repository:** keep source and dependencies inside the VM.

```bash
watermelon clone https://github.com/owner/project
cd project
```

At the setup prompt, continue with the detected settings, choose `c` to
customize runtimes, resources, ports, and network destinations, or `e` to edit
the TOML. The revised plan is shown before installation. Choose `s` to keep
the source and prepare it later with `watermelon up`.

Replace the URL; the destination must not exist. `clone` asks before dependency
setup. Use `--no-setup` to inspect first, then `watermelon up` when ready.
The host directory contains settings; source lives on a retained guest disk at
`/workspace/project`, with no writable host mounts. Private repositories need
manual configuration.

### 3. Work

Run these on the host, using your project's commands:

```bash
watermelon exec -- npm test
watermelon exec --cwd backend -- python -m pytest
watermelon run    # Guest shell; exit returns to the host
watermelon code   # Remote editor; requires a VS Code-compatible CLI and Remote-SSH
watermelon stop   # Stop the VM and keep its files
```

Exiting a shell leaves the VM running. For dev servers, [forward the app's
port](docs/WORKSPACES.md#run-a-development-server) and bind it to `0.0.0.0`.

## What to know

- **Network:** the default `fail` policy blocks destinations outside the
  allowlist. Use `watermelon logs` to inspect blocked traffic and review any
  additions. [Network settings](docs/CONFIG_SPEC.md#network) also support quiet
  blocking, discovery, prompts, and per-process rules.
- **Changes:** run `watermelon up` after manifest changes, `up --refresh` to
  reinstall unchanged dependencies, and `rebuild` after VM or policy changes.
- **Storage:** `rebuild` and `destroy` retain host source and managed workspace
  disks, but remove VM-local system files. Only `destroy --delete-workspace`
  also permanently deletes managed source and edits.
- **Limits:** exposed files can be read or modified, and allowed destinations
  can carry data out. Watermelon relies on the VM boundary; see the
  [security model](docs/SECURITY.md).

## Use with Codex or Claude Code

```bash
watermelon agents install
# In a project that already has .watermelon.toml:
watermelon init --instructions-only
```

Start a new assistant session. Its instructions route project execution through
`watermelon exec` while it edits local files normally. They guide behavior;
they do not intercept the assistant's host shell. See [assistant setup](docs/CODING_ASSISTANTS.md).

## Documentation

[Project workflows](docs/WORKSPACES.md) · [Examples](docs/examples/README.md) ·
[Commands](docs/COMMANDS.md) · [Configuration](docs/CONFIG_SPEC.md) ·
[Troubleshooting](docs/TROUBLESHOOTING.md) · [All documentation](docs/README.md)

## License

MIT
