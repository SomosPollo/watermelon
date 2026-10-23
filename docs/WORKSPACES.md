# Project workflows

Run Watermelon commands on the host. `watermelon run` opens a guest shell;
use `exit` to return. For installation and first setup, use the
[quick start](../README.md#quick-start).

## Clone a public project

```bash
watermelon clone https://github.com/owner/project
cd project
```

The destination must not exist. The host directory holds settings; source and
edits live at `/workspace/project` on a separate guest disk. Managed workspaces
have no writable host mounts. Their source, dependencies, and logs survive
normal VM rebuilds and deletion.

`clone` acquires source, reads manifests without executing project code, and
asks before preparing dependencies. At that prompt, use `c` to customize
runtimes, resources, forwarded ports, and additional network destinations, or
`e` to edit the full TOML in your editor. Watermelon shows the revised plan
before asking for approval. Use `s` to defer setup; source remains in the guest
and `watermelon up` offers the review again. Draft customizations are saved
only when you approve setup.

The acquisition-only GitHub network grant
is removed from the runtime policy. To inspect before setup:

```bash
watermelon clone --no-setup https://github.com/owner/project
cd project
watermelon run
# Inspect source in the guest, then exit
watermelon up
```

Only credential-free public GitHub HTTPS URLs are supported. Host credentials,
Git hooks, automatic submodules, and LFS filters are not inherited. The repo's
own `.watermelon.toml` stays guest content; the host settings remain policy.
Private repositories and corporate proxies need manual configuration.
If cloning fails, the error identifies the retained settings directory and
retry step; do not discard source just to retry setup.

## Prepare a local project

From the local project root, run `watermelon init`, review `.watermelon.toml`,
then run `watermelon config validate` and `watermelon up`. The entire project
is mounted read-write at `/project`, including secrets stored there.

`init` detects supported manifests at the root and in subdirectories without
running project code. It also adds assistant guidance to `AGENTS.md` and
`CLAUDE.md`, preserving existing instructions. See [assistant setup](CODING_ASSISTANTS.md).
Existing configs are never overwritten.

Use `init --template node` (or another profile below) to restrict detection.
Use `init --template manual` for custom tools and policies; start manual VMs
with `watermelon run` and install dependencies in the guest.

## Change sandbox settings

From a local project or a clone's host settings directory, open the host policy:

```bash
watermelon config edit
```

Use it to adjust allowed destinations, ports, resources, or editor preferences.
Watermelon validates a draft before saving and reports whether the change needs
a VM rebuild. Setup updates retain comments and custom settings as runtimes or
project layouts change. A cloned repository's own config remains guest content;
the host policy controls the sandbox.

Apply VM-affecting edits with `watermelon rebuild`; preserve needed VM-local
system files first. Managed source and mounted project files are retained.
IDE-only and comment-only edits need no rebuild. For editor selection, draft
recovery, and `ask`-mode changes, see [`config edit`](COMMANDS.md#watermelon-config-edit).

## Runtime and dependency support

Automatic setup selects one compatible runtime per language and prepares each
project in its own directory. The setup plan shows versions, dependency
commands, network destinations, and mounts before approval.

| Profile | Runtime selection and default | Dependency preparation |
|---------|-------------------------------|------------------------|
| `node` | `.nvmrc`, `.node-version`, or `engines.node`; Node 24 | npm, pnpm, or Yarn; frozen/immutable install when locked |
| `python` | `.python-version` or `requires-python`; Python 3.13 | pip, uv, or Poetry in a project `.venv` |
| `go` | `.go-version`, effective `toolchain`, then declared minimum; Go 1.27 | `go mod download all` |
| `rust` | Numeric toolchain pin, checked against `rust-version`; Rust 1.99 for `stable` or no pin | `cargo fetch --locked`, or `cargo fetch` to create a missing lockfile |
| `java` | `.java-version`, Maven compiler settings, or literal Gradle toolchain; JDK 21 | Maven `dependency:go-offline test-compile` or Gradle `classes testClasses` |
| `dotnet` | `global.json`; .NET 10 SDK | `dotnet restore`, locked when a lockfile exists |
| `ruby` | `.ruby-version` or literal Gemfile requirement; Ruby 3.4 | Bundler, frozen when locked |
| `php` | Composer PHP requirement; PHP 8.4 | `composer install` with platform checks |
| `cpp` | GCC 12; selectors 12–15 | CMake configures `.watermelon-build`; Clang, Ninja, Make, and pkg-config included |

Dependency preparation can execute project hooks and compile JVM classes.
It happens inside the sandbox after network enforcement. Run builds and tests
separately with `exec`.

### Choose versions and package managers

```bash
watermelon clone --template node --runtime 22 https://github.com/owner/project
watermelon up --runtime node=22,python=3.13
```

Unspecified languages keep their approved versions. Conflicting or unsupported
requirements stop setup rather than silently selecting an incompatible runtime.
Node 22/24 and Python 3.12/3.13/3.14 accept numeric minor/patch pins; Go and Rust
accept stable `1.minor` or `1.minor.patch`. JVM supports JDK 17/21/25; .NET
supports SDK 8.0/9.0/10.0 and numeric SDK patch pins. Runtime tags may receive
patch updates on rebuild; use exact versions or manual digest-pinned images
when reproducibility requires them.

Node recognizes exact `packageManager` or `devEngines.packageManager` pins,
including Corepack integrity suffixes, and workspace lockfiles. Defaults for
unpinned lockfiles are pnpm 10.20.0, Yarn classic 1.22.22, or Yarn modern 4.10.3;
Corepack is 0.34.4. A pinned npm version must match its runtime image.
Ambiguous lockfiles and URL-based managers are rejected.

Python recognizes uv/Poetry configuration and lockfiles. Defaults are uv 0.12.0
and Poetry 2.5.1 unless a supported exact manager requirement selects another
version. Managers use the selected image interpreter; automatic Python
downloads are disabled. uv workspace members share an environment.

### Monorepos and environments

npm, Go, Cargo, JVM, and Python workspace roots cover their members. Version
files inherit from the nearest ancestor; requirements must agree on a shared
runtime per language. All selected toolchains are available to install hooks.

```bash
watermelon exec --cwd frontend -- npm test
watermelon exec --cwd backend -- python -m pytest
watermelon exec --cwd native -- cmake --build .watermelon-build
```

`--cwd` selects a guest subdirectory and its nearest Python environment.
Independent Python projects have separate `.venv` directories. With exactly
one Python project, its environment is available across project directories;
with several, select one explicitly. A running shell keeps its initial
environment after `cd`.

Discovery skips hidden/generated directories and never follows directory
symlinks. It stops on malformed metadata or limits: 8 levels, 20,000 entries,
2,048 directories, 64 projects, 8 MiB per metadata file, and 24 MiB total.

### Refresh and custom setup

Run `watermelon up` after adding languages or changing manifests. Unchanged
inputs skip installation. Use `up --refresh` after removing dependency
directories or changing indirect inputs such as included requirements, local
path dependencies, or Cargo configuration. Refresh recreates pip/Poetry
environments and reinstalls uv dependencies.

Changed toolchains or project layouts require a new approval plan and VM
replacement. Caches live in the guest tool home; managed workspace rebuilds
retain them. Rust outputs remain in `target`. Combined profiles default to a
20 GB system disk; extended profiles use 40 GB.

Use manual tools for Bun, private/VCS dependencies, alternate registries,
custom native libraries, Android, Rust nightly/custom components, or
cross-compilation. Windows and legacy .NET Framework targets are incompatible
with the Linux VM. Go automatic setup disables
direct VCS fallback and toolchain downloads; Rust uses the bundled toolchain
and sparse crates.io. PHP includes common database, intl, zip, and bcmath
extensions. CMake download hosts still need explicit network rules.

Automatic profiles cannot combine with `[network.process]` or global
`[provision]` package lists. For custom tools in a managed `--no-setup` clone,
edit the host config and recreate with `destroy` then foreground `run`, keeping
the workspace settings. See [manual tools](CONFIG_SPEC.md#tools).

## Work, edit and serve

```bash
watermelon exec -- npm test
watermelon run
watermelon code
```

Automatic profiles ensure approved setup is ready before entering a workload;
manual `exec` and `code` need an existing VM. `code` requires the editor's CLI
and Remote-SSH support and stays foreground until the remote window closes.
See [editor options](COMMANDS.md#watermelon-code).

Edit mounted projects on the host. Edit managed source through guest commands
or a remote IDE. To export a file, read the VM name from `watermelon status`:

```bash
watermelon copy VM_NAME:/workspace/project/result.txt ./result.txt
```

Automatic preparation supports `fail`, `silent`, or explicitly selected `log`
enforcement. For `ask`, use one foreground `watermelon run` session and install
dependencies interactively. See [prompt behavior](CONFIG_SPEC.md#security).

### Run a development server

For Vite, add the port to the host `.watermelon.toml`:

```toml
[ports]
forward = [5173]
```

If a VM already exists, apply the change with `watermelon rebuild`. Preserve
needed VM-local system files first. Start the app from the host:

```bash
watermelon exec -- npm run dev -- --host 0.0.0.0 --port 5173
```

Keep it running and open `http://127.0.0.1:5173`. Forwards bind to host loopback
and use the same host/guest port. The app must listen on that port and on
`0.0.0.0`; forwarding does not grant guest access to host services.

## Inspect, recover and delete

| Command | VM system files | Host source / managed disk |
|---------|-----------------|----------------------------|
| `stop` | Kept; VM stops | Kept |
| `rebuild` | Replaced | Kept |
| `destroy` | Deleted | Kept |
| `destroy --delete-workspace` | Deleted | Managed source, edits, and dependencies permanently deleted |

These actions interrupt active work. Save changes and preserve files outside
retained storage first. Rebuilding keeps existing source, including any
malicious files; it does not reset the checkout. Increasing `[resources].disk`
changes the system disk, not the separately retained workspace disk.

Use `status` for the recorded policy and setup stage, `logs` for network
events, and `up --dry-run` to inspect settings without VM operations.
`status --json` and `config validate --json` use `schema_version: 1`.
Status compares records, rather than auditing the live firewall or dependencies.
Managed logs need a running VM.

Keep the host settings directory and private Watermelon state to reuse a
retained disk; moving or copying settings does not transfer ownership. After
permanent workspace deletion, clone into a new destination. See
[lifecycle flags and recovery](COMMANDS.md#project-and-vm-selection).
