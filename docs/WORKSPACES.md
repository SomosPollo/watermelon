# Project workflows

[Documentation](README.md) · [Command reference](COMMANDS.md)

Watermelon supports managed guest workspaces for untrusted repositories and
mounted host projects for existing local development. Both retain strict
`fail` network enforcement by default.

Run the commands in this guide on the host. `watermelon run` opens a guest
shell; use `exit` to return to the host before running another Watermelon command.

| Goal | Section |
|------|---------|
| Keep a public repository's source inside the VM | [Clone a public project](#clone-a-public-project) |
| Use files already on your host | [Prepare a local project](#prepare-a-local-project) |
| Choose languages, runtimes, or package managers | [Runtime and dependency support](#runtime-and-dependency-support) |
| Run commands, edit, or export files | [Work, edit and serve](#work-edit-and-serve) |
| Open an app in your host browser | [Run a development server](#run-a-development-server) |
| Understand what survives stop, rebuild, or deletion | [Inspect, recover and delete](#inspect-recover-and-delete) |

## Clone a public project

```bash
watermelon doctor
watermelon clone https://github.com/owner/project
cd project
watermelon run
```

The destination must not already exist. It is a host directory containing
`.watermelon.toml`; it is **not the source checkout**. Source files are at
`/workspace/project` inside the guest. That directory lives on an independently
owned Lima disk, which is retained when the VM is destroyed or rebuilt.

Managed VMs have no writable host mounts. Their network logs also live on the
guest data disk and survive rebuilds. Use `watermelon logs` or `logs --clear`
while the VM is running; the CLI retrieves only a bounded tail. Clearing removes
records already written; events still in flight can appear afterward. Logs from an
earlier development build's host logging mount are not migrated automatically;
rebuild that VM when prompted so the stricter boundary takes effect.

Cloning has two stages. An acquisition VM checks out the public repository
under a strict GitHub HTTPS rule. Watermelon reads bounded manifests without
executing project code, then displays the runtime, dependency commands, network
destinations, and mount behavior. After approval, Watermelon replaces that VM
with the selected runtime, retaining the source disk, and installs dependencies
under the approved policy. The acquisition-only GitHub grant is removed from
the generated runtime policy.

The repository's `.watermelon.toml` is source content inside the guest. It is
never copied over the host's settings or executed as privileged provisioning.
SSH agents, host Git configuration, credential helpers, automatic submodule
checkout, and Git LFS filters are not inherited. Only credential-free public
`https://github.com/owner/repository` URLs are supported.

Managed workspaces and automatic profiles do not inherit host proxy environment
variables, which may contain credentials. Corporate proxy environments need
explicit manual configuration. Private-repository authentication is outside
automatic onboarding in this release.

Trusted VM bootstrap uses Lima's network DNS defaults. After bootstrap,
Watermelon's managed resolver uses `8.8.8.8` and `8.8.4.4` for permitted
forwarded lookups. Exact allowed names are captured during bootstrap; in strict
mode, workload DNS answers only approved names.
Host-side Lima downloads still use the host's network and DNS configuration.
If an existing VM repeatedly stalls during GitHub TLS negotiation, compare its
captured address with current DNS and use `watermelon rebuild` to refresh the VM
and its DNS snapshot while retaining the managed workspace disk.

```bash
watermelon clone --no-setup https://github.com/owner/project
watermelon clone --template node --runtime 22 https://github.com/owner/project
watermelon clone --yes https://github.com/owner/project another-workspace
```

`--no-setup` performs only acquisition. Use `watermelon run` or `exec` to
inspect the base Linux workspace, and `watermelon up` when ready to prepare it.
On failure, settings and source are retained; the error names the failed stage
and the directory from which to retry `watermelon up`. A completed checkout can
be recognized after interrupted host-state publication; an incomplete Git
checkout is not automatically discarded.

## Prepare a local project

```bash
cd existing-project
watermelon init
# Review the generated .watermelon.toml
watermelon config validate
watermelon up
```

`init` inspects manifests and writes an active tool profile and registry
allowlist. It also appends Watermelon execution guidance to `AGENTS.md` and
`CLAUDE.md`, preserving existing instructions. Use `watermelon agents install`
once to install the shared skill for local Codex or Claude Code sessions;
existing projects can add guidance with `watermelon init --instructions-only`.
See [assistant setup](COMMANDS.md#watermelon-agents-install).
It does not execute project code or create a VM. Unlike managed
clones, local projects retain the existing writable host mount at `/project`.
Dependencies and project edits in that mount affect host project files.

Automatic setup detects every supported language, including projects in subdirectories.
Use `init --template node`, `python`, `go`, `rust`, `java`, `dotnet`, `ruby`, `php`, or `cpp` to restrict setup
to one language. Use
`init --template manual` for the commented configuration and custom tool
workflows. Manifest symlinks, special files, oversized metadata, and malformed
metadata fail explicitly. Existing configurations are never overwritten.

## Runtime and dependency support

Automatic setup composes these supported toolchains in one sandbox:

| Profile | Runtime selection | Dependency setup |
|---------|-------------------|------------------|
| Node/npm | Numeric `.nvmrc`/`.node-version`, or supported `engines.node`; Node 24 otherwise | `npm ci` with a lockfile, `npm install` otherwise; engine checks enabled |
| Python/pip | Numeric `.python-version`, or supported `requires-python`; Python 3.13 otherwise | A project `.venv`, then `pip install -r requirements.txt` or editable installation from `pyproject.toml` |
| Go modules/workspaces | `.go-version`, then the effective `toolchain` directive; Go 1.27 or a newer declared `go` minimum otherwise | `go mod download all` using the public module proxy and checksum database |
| Rust/Cargo | Numeric `rust-toolchain.toml`/`rust-toolchain` pin; Rust 1.99 for `stable` or no pin; validates `rust-version` minimums | `cargo fetch --locked` with `Cargo.lock`; `cargo fetch` generates a missing lockfile |
| Java/Kotlin | Numeric `.java-version`, Maven compiler properties, literal Gradle JVM toolchain declarations; JDK 21 otherwise | Maven `dependency:go-offline test-compile` without tests, or Gradle `classes testClasses`; Maven/Gradle wrappers run inside the sandbox when present |
| C#/.NET | Numeric SDK version from `global.json`; .NET 10 SDK otherwise | `dotnet restore` for detected project files; locked mode when `packages.lock.json` exists |
| Ruby | `.ruby-version` or literal Gemfile Ruby requirement; Ruby 3.4 otherwise | Bundler, with frozen dependencies when `Gemfile.lock` exists |
| PHP | Composer's PHP requirement; PHP 8.4 otherwise | `composer install`, retaining `composer.lock` versions and platform checks |
| C/C++ | GCC 12 by default; GCC 12–15 selectors available | CMake configures `.watermelon-build`; GCC, Clang, Ninja, Make and pkg-config are available |

### Multi-language projects and monorepos

`clone` and `init` discover manifests at the root and in subdirectories, select
one compatible runtime per language, and install each project's dependencies
in its own directory. npm, Go, and Cargo workspace roots install dependencies
for their detected members; member version requirements are also checked.
Version files are inherited from the nearest ancestor. Conflicting requirements
stop setup with an explanation before dependency installation. Use
`up --runtime language=version,...` to review a different shared version for an
existing workspace; unspecified languages keep their approved version.

All selected toolchains are available in the same runtime image. A Node install
hook can invoke Python, Go, or Rust. Trusted bootstrap assembles official Debian
toolchain images using fixed copy commands without exposing project files.
Extended language profiles use a Debian Bookworm runtime with JVM/.NET trees
copied from their official images and fixed system prerequisites.
Compiled-language dependency preparation, Bundler/Composer, and Python
environments run before Node package-manager hooks.
The generated allowlist includes the registries for every selected language.
Combined images default to a 20 GB compute disk; managed source storage remains
separate and survives rebuilds. Extended language profiles use a 40 GB compute
disk for their SDK images.

```bash
watermelon clone --runtime node=22,python=3.13 https://github.com/owner/project
watermelon up --runtime node=22,go=1.27
watermelon exec --cwd frontend npm test
watermelon exec --cwd backend python -m pytest
watermelon exec --cwd service go test ./...
watermelon exec --cwd native cargo test --locked
```

Each Python project gets its own `.venv`. `exec --cwd` selects the environment
at that directory or its nearest ancestor. With exactly one Python project,
its environment is also available from other project directories, including
npm hooks. With multiple Python projects, use `--cwd` or explicitly activate the
intended environment; conflicting Python dependencies are kept separate.
A running shell retains its initial environment after `cd`.

Discovery skips hidden directories and `node_modules`, `vendor`, `target`,
`venv`, `dist`, `build`, `bin`, `obj`, `out`, `CMakeFiles`, and `__pycache__`. It never follows directory symlinks.
Limits are eight nested directory levels, 20,000 entries, 2,048 directories,
64 detected projects, 8 MiB per metadata file and 24 MiB total metadata.
Exceeding a limit stops setup rather than silently installing an incomplete plan.

`up` discovers added languages and projects and displays an updated approval
plan. Applying a changed toolchain/project layout replaces the VM and retains
the managed source disk or local project mount. An explicit `--template` keeps
setup restricted to that language; the default and `--template all` include all.

Node 22/24 and Python 3.12/3.13/3.14 profiles accept numeric minor/patch pins.
Common numeric comparisons, `^`, `~`, and alternatives are supported for
runtime requirements. Unsupported declarations produce an error rather than
silently choosing an incompatible tool. A pinned npm version must match the
version bundled in the selected runtime image. Go and Rust accept numeric stable
`1.minor` or `1.minor.patch` versions. Go workspace declarations take precedence
over module toolchain suggestions; workspace modules must be inside the project.
Rust toolchain file shorthand such as `1.85` selects `1.85.0`, following rustup's
pin semantics. Rust nightly/beta/custom toolchains, extra components beyond
the bundled compiler/Cargo/rustdoc/rustfmt/clippy, and cross-compilation targets
require manual tools. Rustfmt and clippy are installed into the guest image by
fixed trusted bootstrap commands before workload enforcement. Project files and
mounts are not exposed to that bootstrap container. Bun and languages outside
the listed profiles require manual configuration.

### Additional package managers

Node setup honors numeric `packageManager` pins for pnpm and Yarn, including
Corepack `+sha224`, `+sha256`, `+sha384`, and `+sha512` integrity suffixes. Hashes
are retained in the approved configuration and checked by Corepack. Exact
`devEngines.packageManager` pins are also recognized when `packageManager` is
absent. URL-based managers remain unsupported. Setup detects their
lockfiles, and uses frozen/immutable installation when a lockfile exists.
pnpm workspace manifests and npm/Yarn workspace declarations cover member
installation. Unpinned pnpm lockfiles default to pnpm 10.20.0; Yarn classic
lockfiles use 1.22.22 and modern lockfiles use 4.10.3. Ambiguous lockfiles fail
with an explanation. Pinned managers run through Corepack 0.34.4, installed in
the persistent guest home under the approved registry policy.

When removing a language or changing managers, the proposed plan removes that
profile's default registry grants. Custom rules outside profile defaults remain
in the proposal. Review the complete displayed allowlist before approving it.

Python setup recognizes `uv.lock`, `[tool.uv]`, `poetry.lock`, and `[tool.poetry]`.
It uses uv 0.12.0 or Poetry 2.5.1 unless a supported exact manager requirement
selects a different version. Existing lockfiles are preserved through locked
uv sync or Poetry sync. uv workspace members share the workspace environment;
independent projects keep separate environments. Automatic Python downloads are
disabled so both managers use the selected image interpreter. Their own tool
environments are separate from project `.venv` directories. `up --refresh`
reinstalls uv dependencies and recreates Poetry project environments.

### JVM, .NET, Ruby, PHP and native projects

The JVM profile supports JDK 17, 21 and 25. Maven module roots and Gradle
settings roots handle member dependency preparation. Maven/Gradle/Gemfile and
MSBuild code is never evaluated on the host. Maven and Gradle preparation compile normal
JVM classes so artifacts are available for later offline builds. Android SDKs,
native mobile targets, and computed toolchain declarations may require manual
configuration. Wrapper downloads run under the reviewed workload policy.

.NET SDK selectors support 8.0, 9.0 and 10.0, including numeric SDK patch pins.
Newer SDK environments also contain older supported shared runtimes, allowing
net8/net9 applications to run alongside net10 applications. Windows and legacy
.NET Framework targets are rejected as incompatible with the Linux VM.

Ruby uses Bundler's project resolution with a retained gem store. PHP includes
common `intl`, `zip`, `bcmath`, MySQL and PostgreSQL extensions. Composer keeps
platform requirements enabled and uses IPv4, matching strict network enforcement.
Other native extensions or libraries fail explicitly and require manual setup.
Composer's GitHub archive endpoints and JVM wrapper/plugin services appear in
the setup approval plan alongside the language registries.

C/C++ support starts with CMake configuration using project defaults. Build with
`watermelon exec --cwd native cmake --build .watermelon-build`. Custom presets,
Conan/vcpkg provisioning, additional system libraries, and cross-compilation
require manual configuration. FetchContent download destinations need explicit
network approval; native setup does not implicitly grant arbitrary internet access.

Go profiles allow HTTPS to `proxy.golang.org`, `sum.golang.org` and
`storage.googleapis.com` for module archives. `GOTOOLCHAIN=local` keeps the
reviewed image toolchain; `GOVCS=off` and a proxy without direct fallback keep
dependency resolution on the approved module service. Rust profiles allow
`index.crates.io` and `static.crates.io`, use the sparse registry protocol, and
select the image's bundled Rust version without rustup downloads. Private/VCS
dependencies or alternate registries need explicit manual configuration.

Both profiles fetch dependencies during preparation. Build and test commands
run separately inside the same sandbox, for example `watermelon exec go test
./...` or `watermelon exec cargo test --locked`. Go module/build caches and
Cargo downloads/installed binaries live in the persistent guest tool home;
Rust build outputs remain in the project's `target` directory. Caches survive
commands, restarts and managed workspace rebuilds.

Use `init --template manual` and `run` for an existing local project. For a
managed `--no-setup` checkout using custom tools, edit the host settings and
apply them with `destroy --force` followed by `run`. Automatic `up`/`rebuild`
dependency preparation supports the profiles listed above.

Assembled tool images provide a consistent runtime for setup, `run`, `exec`,
and IDE tool wrappers. Containers run rootless inside the guest, share the
project filesystem and a persistent guest tool home outside the source checkout,
and never pull images lazily
during workload execution. Python commands and installed console scripts use
the project virtual environment. Subdirectory changes inside the runtime are
preserved. Restarts reuse prepared SDK images; rebuilds refresh them.
Runtime image tags may receive patch updates on rebuild; use an
exact numeric version or manual digest-pinned tools when that matters.

An unlocked npm installation creates a lockfile. Unpinned Python requirements
remain unpinned; Watermelon does not claim otherwise. Setup records are stored
in private host state outside guest mounts. Unchanged manifests skip repeated
installation. Use `up --refresh` after editing an indirectly included Python
requirements file, local path dependency sources, Cargo configuration, removing
dependency directories, or changing other inputs outside the inspected manifests.
Detected nested manifests and lockfiles automatically invalidate readiness. Refresh recreates Python's virtual
environment from declared requirements.

Project setup may execute install/build hooks. These run inside the sandbox
after policy enforcement; they are never converted into root provision
scripts. Supported profiles cannot be combined with `[network.process]` or
global `[provision]` package lists. Manual tools retain those capabilities.

## Work, edit and serve

```bash
watermelon exec npm test
watermelon exec python -m pytest
watermelon exec --cwd packages/web npm test
watermelon run
watermelon code
```

With a setup profile, `run`, `exec`, and `code` ensure approved setup is ready.
An initial setup decision needs interactive approval, or an explicit
`watermelon up --yes` before noninteractive commands. Existing manual projects
retain their original command behavior. Setup output preceding `exec` is sent
to stderr so guest command stdout can be piped.

`code` opens the guest project through the configured VS Code-compatible
remote IDE. Project tool wrappers are available in the VM; an IDE may need an
explicit interpreter path or a configured tool wrapper. Export to the host is
an explicit transfer:

```bash
watermelon status                    # Read the VM Name
watermelon copy VM_NAME:/workspace/project/result.txt ./result.txt
```

Package downloads from Git, alternate registries, and external build assets
may require reviewed allowlist additions and a rebuild. Permitted destinations
remain potential data-exfiltration paths.

Automatic setup supports noninteractive `fail`, `silent`, or explicitly chosen
`log` policies. `ask` requires a foreground controller: use `watermelon run`
and install dependencies interactively, keeping that session open. `up` does
not detach an ask-mode controller or silently change enforcement.

### Run a development server

1. Set the app's listening port in the host `.watermelon.toml`. For Vite:

   ```toml
   [ports]
   forward = [5173]
   ```

2. Apply the change from the host:

   ```bash
   watermelon rebuild
   ```

   Rebuild removes VM-local system state; mounted host files and managed
   workspace disks remain. Preserve any needed files stored elsewhere in the VM.

3. Start the server from the host, using your project's command. For Vite:

   ```bash
   watermelon exec -- npm run dev -- --host 0.0.0.0 --port 5173
   ```

4. Keep that command running and open `http://127.0.0.1:5173` on the host.

Explicit forwards bind to host `127.0.0.1` and use the same host and guest port.
They expose guest services to the host; they do not grant guest access to host
services. Changing the forwarded port also requires changing the server's
listening port. Network changes never silently enable permissive discovery mode.

## Inspect, recover and delete

Choose one lifecycle action; these are alternatives:

| Command | VM system state | Mounted host project | Managed workspace disk |
|---------|-----------------|----------------------|------------------------|
| `stop` | Kept; VM stops | Kept | Kept |
| `rebuild` | Replaced | Kept | Kept |
| `destroy` | Deleted | Kept | Kept |
| `destroy --delete-workspace` | Deleted | Not applicable | Permanently deleted, including source and edits |

`stop`, `rebuild`, and `destroy` interrupt active guest work. Finish commands
and save changes before using them. The commands below inspect current state:

```bash
watermelon up --dry-run               # Inspect settings without VM operations
watermelon config validate --json
watermelon status --json
watermelon logs
```

`status --json` and config validation use `schema_version: 1`. Status describes
recorded applied policy and setup state; it is not a live firewall or dependency
integrity audit. `up` checks manifests before using its readiness record.
Watermelon preparation/validation failures exit 1. Guest commands retain their
numeric exit status; status preserves its existing successful missing-VM
semantics, with the missing state exposed in JSON.

`stop` preserves both VM and workspace. `rebuild` replaces VM-local system
state and rechecks setup while retaining the managed disk or mounted host
project. Existing guest files remain untrusted after rebuilding. Unmounting
the project is not a migration of local files into a managed workspace.

For noninteractive rebuilds after changing policy, use `rebuild --force --yes`:
the flags separately confirm VM replacement and approval of the changed setup
plan. Checkout has a two-minute deadline; interrupted or stalled acquisition
stops its verified VM and retains files for review/retry.

`destroy` retains managed disks by default. Only `--delete-workspace`
permanently removes the owned disk, including uncommitted edits and project
dependencies. The confirmation describes that loss; `--force` skips the
confirmation only. Disk deletion refuses an active setup operation and never
force-deletes an attached disk. After permanent deletion, clone into a new
destination. Preserve host settings and private Watermelon state if you want
to continue using a retained disk; moving/copying the settings directory does
not transfer its ownership.
