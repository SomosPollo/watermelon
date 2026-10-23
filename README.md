# Watermelon

**Run development commands in a Linux VM.** Watermelon uses Lima to keep
third-party code away from unmounted host files and to limit outbound network
access. Use it to install dependencies, build, test, and run development servers.

## Quick start

### 1. Install and check your host

You need macOS 13 or newer, or Linux; stable Lima 2.0.0 or newer; `curl`; and an
OpenSSH client. Linux also needs an architecture-appropriate QEMU system emulator.
See [installation](docs/INSTALLATION.md) for Linux prerequisites, installation
without root, Go installations, and upgrades.

On macOS with Homebrew:

```bash
brew install lima
```

After installing the prerequisites on either platform:

```bash
curl -fsSL https://raw.githubusercontent.com/SomosPollo/watermelon/main/install.sh | sh
watermelon doctor
```

Resolve any `FAIL` checks from `doctor` before continuing. First-time VM
creation downloads images and can take several minutes.

### 2. Choose where your source lives

| Workflow | Source location | Host project access from the VM |
|----------|-----------------|---------------------------------|
| **Clone a public GitHub repository** | Managed guest disk at `/workspace/project` | No writable host mounts |
| **Use an existing local project** | Host directory, mounted at `/project` | Read-write access to the entire project root |

#### Clone a public project

Replace the URL below with your repository. The destination directory must not
already exist.

```bash
watermelon clone https://github.com/owner/project
cd project
watermelon run
```

`clone` checks out source inside the VM, displays a setup plan, and asks before
installing dependencies. Automatic setup supports Node, Python, Go, Rust,
JVM, .NET, Ruby, PHP, and C/C++, including mixed-language projects.

The host `project` directory holds Watermelon settings. **Your source and
edits live inside the guest**, on a separate disk retained across VM rebuilds.
Use `--no-setup` to inspect source before installing dependencies. Private
repositories require manual configuration.

#### Use an existing local project

Run these commands in your project directory on the host:

```bash
cd your-project
watermelon init
# Review the generated .watermelon.toml before continuing
watermelon config validate
watermelon up
watermelon run
```

`init` detects supported languages and writes settings. `up` displays a setup
plan and installs dependencies after approval. Review `.watermelon.toml` as
trusted host policy: it controls network access, mounts, images, and provisioning.
The default mount lets guest commands change project files, including any
secrets stored there.

### 3. Work in the sandbox

After `watermelon run`, you are in the guest shell. Run your project's commands
there, then use `exit` to return to the host. Exiting the shell leaves the VM
running.

From the host, you can also run individual commands or open a remote editor:

```bash
watermelon exec -- npm test                  # Use your project's test command
watermelon exec --cwd backend python -m pytest  # Select a project subdirectory
watermelon code                             # Open a VS Code-compatible IDE
watermelon stop                             # Stop the VM and keep its files
```

`code` needs the editor's CLI and Remote-SSH support. It stays in the foreground
until the remote editor window closes. See [editor setup](docs/COMMANDS.md#watermelon-code).
For a dev server, [forward its port](docs/WORKSPACES.md#run-a-development-server)
before opening it on the host.

## Everyday commands

Run project commands from the settings directory or a descendant. Watermelon
finds the nearest `.watermelon.toml` within its
[project discovery boundaries](docs/CONFIG_SPEC.md#file-location).

| Goal | Command |
|------|---------|
| Inspect the VM and recorded policy | `watermelon status` |
| View blocked network traffic in the default mode | `watermelon logs` |
| Recheck dependencies after changing manifests | `watermelon up` |
| Reinstall dependencies when manifests are unchanged | `watermelon up --refresh` |
| Apply VM or policy changes | `watermelon rebuild` |
| List sandboxes across projects | `watermelon list` |
| Stop the VM, keeping its state | `watermelon stop` |
| Delete the VM, keeping host files and managed workspace disks | `watermelon destroy` |

`rebuild` replaces VM-local system state while retaining mounted host files and
managed workspace disks. Preserve needed files stored elsewhere in the VM
before rebuilding. Only `destroy --delete-workspace` also permanently deletes
a managed disk, including source and uncommitted edits. See
[storage and recovery](docs/WORKSPACES.md#inspect-recover-and-delete).

## Use with Codex or Claude Code

These integration commands were added after v0.5.0. If your installed CLI does
not recognize `agents install`, follow the
[version and source build guidance](docs/CODING_ASSISTANTS.md#check-your-watermelon-version).

Install the bundled Watermelon skill once for detected local assistants:

```bash
watermelon agents install
# Or select assistants explicitly, even before their first launch:
watermelon agents install --agent codex,claude
```

For a new local project, `watermelon init` also adds execution guidance to
`AGENTS.md` (Codex) and `CLAUDE.md` (Claude Code), preserving existing text.
For a project that already has `.watermelon.toml`, add the guidance with:

```bash
watermelon init --instructions-only
```

Start a new assistant session in that local project and give it a normal
development task. The instructions direct project installs, builds, tests,
scripts and dev servers through `watermelon exec`; the assistant edits source
files normally. Review the initial `watermelon up` plan before setup.

This integration guides the assistant; it does not intercept its host shell.
Only commands routed through Watermelon run in the VM, and the default local
project mount remains writable. These instructions target local mounted
projects; managed clones keep source inside the guest. See
[assistant setup](docs/CODING_ASSISTANTS.md) for the complete workflow and
installation troubleshooting.

## Network policy and limits

The default `security.enforcement = "fail"` blocks connections outside
`[network].allow` and records rate-limited policy events. Managed DNS answers
only policy names. Loopback, established response traffic, and scoped VM-control
DHCPv4 traffic remain available. IPv6 is disabled in blocking modes.

| Mode | Traffic outside the allowlist |
|------|-------------------------------|
| `fail` (default) | Block and record policy events |
| `silent` | Block without policy events |
| `log` | Allow for discovery; record IPv4 events only |
| `ask` | Prompt for TCP connections while a foreground controller is active; reject other traffic |

Allowed destinations can carry data out of the sandbox. The default local
project mount lets code modify host project files. Watermelon relies on the
VM boundary and does not protect against VM escapes or malicious changes to
files you expose. See the [security model](docs/SECURITY.md) and
[full enforcement rules](docs/CONFIG_SPEC.md#security).

## Documentation

| You want to… | Read |
|--------------|------|
| Install or upgrade Watermelon | [Installation](docs/INSTALLATION.md) |
| Select runtimes, edit, serve, export, or recover a project | [Project workflows](docs/WORKSPACES.md) |
| Look up commands and flags | [Command reference](docs/COMMANDS.md) |
| Configure network rules, tools, mounts, and resources | [Configuration reference](docs/CONFIG_SPEC.md) |
| Start from a project-specific configuration | [Examples](docs/examples/README.md) |
| Fix a failure | [Troubleshooting](docs/TROUBLESHOOTING.md) |
| Understand isolation and its limits | [Security model](docs/SECURITY.md) |
| Build and test Watermelon | [Development](docs/DEVELOPMENT.md) |
| Check release changes | [Changelog](CHANGELOG.md) |

## License

MIT
