# Changelog

All notable changes to Watermelon are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and Watermelon follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Clone setup customization for runtime versions, resources, forwarded ports,
  and network destinations, with full TOML editing and deferred preparation.
- `watermelon config edit` opens a validated draft in `VISUAL` or `EDITOR`,
  preserves unsuccessful edits for recovery, and reports rebuild requirements.
- `watermelon agents install` detects local Codex and Claude Code installations
  and installs the bundled Watermelon skill, with explicit assistant selection
  and preservation of conflicting custom skills.
- `watermelon init` creates or appends project execution guidance in `AGENTS.md`
  and `CLAUDE.md` without replacing existing instructions. Existing local
  projects can adopt it with `init --instructions-only`.

### Fixed

- Preserve comments, unrelated TOML formatting, and custom settings when
  automatic setup updates a project's configuration.

## [0.5.0] - 2026-10-21

Watermelon v0.5.0 introduces managed guest workspaces: clone a public GitHub
project, review its setup, and run its detected languages and dependencies in
one isolated Linux sandbox. Source and dependencies stay on the guest disk.

### Upgrading

Run `watermelon doctor` after installing the update. Existing manual mounted
projects remain supported. Development VMs created with automatic profiles or
managed workspaces must be rebuilt when Watermelon reports a policy or runtime
change:

```bash
watermelon rebuild
```

Run this from the project's settings directory, with the same explicit
`--name` if one was used. A managed rebuild retains source, dependencies,
caches and guest logs. Permanent deletion requires `destroy --delete-workspace`.
Review the setup plan before approving dependency scripts from an untrusted
repository. Private repositories and custom platform SDKs still need manual
configuration.

### Added

- Automatic JVM (Java/Kotlin), .NET, Ruby, PHP and C/C++ setup, composed official
  toolchains, persistent ecosystem caches, and Linux compatibility checks.
- pnpm/Yarn and uv/Poetry detection, numeric manager pins, lockfile preservation,
  workspace-aware installation and guest-only manager bootstrapping.
- Managed guest workspaces through `clone`, with public GitHub HTTPS acquisition,
  separate persistent disks, reviewed Node/npm, Python/pip, Go modules and
  Rust/Cargo setup, and explicit
  permanent workspace deletion.
- Guided `init`, repeatable `up`, VM `rebuild`, `config validate`, and versioned
  JSON status. Profile-backed shell, command, and IDE workflows prepare their
  approved environment automatically.
- Rootless project runtime launchers with persistent project dependencies,
  Python virtual environments, subdirectory support, and no lazy image pulls.
- Automatic multi-language and nested-project discovery, compatible shared
  toolchains in one runtime image, per-project dependency setup, and `exec --cwd`.
- Automatic Go/Rust version selection, module/crate fetching, retained caches,
  lockfile checks, and real-VM build/server coverage.
- Regression and real-VM tests for workspace ownership, manifest inspection,
  setup approval/recovery, dependency persistence, and retained edits.

### Changed

- Require Go 1.27.3 for builds and use the patched Go module parser.
- Validate unpublished version candidates with exact build metadata, checksums,
  native installers on all four platforms, and independent language VM gates.
- Explicit development-port forwards bind to host loopback.
- Managed workspaces bootstrap DNS through the managed resolver's upstreams,
  avoiding unreachable GitHub addresses returned through Lima's host resolver.
- Managed-workspace VM deletion retains project data unless
  `--delete-workspace` is explicitly requested. Existing mounted-project
  workflows and unchanged legacy configuration snapshots retain their behavior.

### Fixed

- Keep managed workspace logs on the guest disk, eliminating writable host log
  mounts; provide bounded log reads and safe clearing through the running VM.
- Bind setup approval and provisioning to the same settings and selected VM;
  preserve configured VM names when using an invocation-only override.
- Allow approved profile updates before a local project's first VM is created,
  and route internal VM-removal progress through the command's output stream.
- Remove obsolete profile registry grants and retain Corepack integrity pins.
- Escape untrusted setup command arguments before requesting approval.
- Handle spaced/multiple Ruby version constraints, reject unsupported .NET
  targets before provisioning, and avoid silently skipping unlisted Gradle builds.

## [0.4.2] - 2026-08-17

### Changed

- Migrated tagged builds and GitHub release creation to GoReleaser, including
  checksums and draft publication only after the cross-platform installer gates
  pass.

## [0.4.1] - 2026-08-17

Watermelon v0.4.1 is the first published v0.4 release. It contains the product
changes originally tagged as v0.4.0 together with the corrected release
validation needed to publish the artifacts.

### Action required when upgrading

Recreate existing Watermelon VMs after upgrading. This release changes
guest-side networking, provisioning, and logging components that cannot be
replaced safely in an already-created VM. This is especially important for VMs
using `security.enforcement = "ask"`, whose in-guest network interceptor is
installed during VM creation. Watermelon treats snapshots written by v0.3.x as
unverified and returns a recreation instruction instead of reusing those VMs.

Preserve any VM-local files you need, then run this from each project using its
default or configured VM name:

```bash
watermelon destroy --force
watermelon run
```

Destroying the VM deletes VM-local state. It does not delete the host project
or other host paths mounted by the project. If the VM was selected with an
explicit CLI `--name`, pass the same `--name` to both commands.

Watermelon now requires stable Lima 2.0.0 or newer. On macOS, Watermelon
requires macOS 13 or newer. Run `watermelon doctor` before recreating VMs to
check the host, Lima backend, SSH client, architecture, and QEMU availability.

### Added

- Added the project-independent `watermelon doctor` command, including a
  versioned JSON report for automated host-readiness checks.
- Added trusted project-root discovery from subdirectories for consistent VM
  identity, mounts, policy state, logs, and provisioning.
- Added secure matching-release downloads of the Linux network interceptor for
  tagged `go install` builds.

### Changed

- Hardened the shell installer with host preflight checks, absolute custom
  install directories, non-`sudo` installation support, Lima guest-architecture
  detection, and exact installed-binary verification.
- Preserved guest exit and signal statuses across CLI handoffs.
- Centralized CLI error rendering so errors print once, with usage limited to
  invocation mistakes.
- Rejected positional arguments on commands that do not accept them before
  resolving or mutating VM state.

### Fixed

- Isolated Linux ask-mode prompts from guest terminal input and made headless
  decisions fail closed.
- Made **Always Allow and Save** distinguish the immediate endpoint decision
  from the broader host rule persisted to `.watermelon.toml`, while preserving
  unrelated file formatting and permissions.
- Hardened TCP and DNS parsing, hostname attribution, packet replay, decision
  caches, authenticated verdict transport, and NFQUEUE failure propagation.
- Corrected KVM permissions and static sidecar construction in the tagged
  release workflow so exact-version VM checks and publication succeed.

### Security

- Rejected stale or mismatched network-interceptor binaries unless their digest
  and Go build identity match the CLI release.
- Expanded configuration validation for ambiguous tools, unsafe or duplicate
  ports and mounts, malformed resources, excessive process rules, and internal
  image or command collisions.
- Hardened provisioning argument handling and exact trusted-script recording.
- Strengthened SSH include management, rollback, VM identity paths, policy-log
  access, atomic publication, ownership checks, and bounded-file handling.
- Added unit, race, vet, vulnerability, Lima compatibility, cross-platform
  artifact, installer, and real-VM security gates to release CI.

## [0.4.0] - 2026-08-17 [YANKED]

The v0.4.0 tag was created, but its release-only real-VM checks failed before
the build and publication jobs. No GitHub release or artifacts were published.
The product changes were published unchanged in v0.4.1 together with the
release-pipeline corrections.

## [0.3.0] - 2026-08-11

Watermelon v0.3.0 changed the default network policy to strict enforcement and
expanded the sandbox lifecycle across macOS and Linux.

### Action required when upgrading

Recreate every existing Watermelon VM before using it with v0.3.0. Watermelon
cannot prove the effective policy of VMs created before versioned policy
snapshots, so `run`, `exec`, and `code` refuse to reuse them.

Preserve any VM-local files you need, update legacy mount targets as described
below, then run:

```bash
watermelon destroy --force
watermelon run
```

Destroying the VM deletes VM-local state. It does not delete the host project
or other host paths mounted by the project.

Omitting `security.enforcement` now selects `fail`, which blocks and records new
external connections not covered by the allowlist. Older generated configs
normally contain an explicit `log`; change that value to `fail` if you want the
strict behavior, review the allowlist, and then recreate the VM.

Targets in `[mounts]` must now be `/mnt/watermelon` or one of its descendants.
For example, migrate:

```toml
[mounts]
"~/.gitconfig" = { target = "/home/dev/.gitconfig" }
```

to:

```toml
[mounts]
"~/.gitconfig" = { target = "/mnt/watermelon/gitconfig" }
```

Applications do not automatically discover configuration at the new target;
point the relevant application or environment variable at the mounted path.

### Added

- Added Linux host builds and lifecycle support alongside macOS.
- Added configurable, project-owned named VMs with collision and ownership
  checks.
- Added optional project mounts, explicit guest workdirs, host-to-VM copying,
  and safer lifecycle recovery for named instances.
- Added the Linux `watermelon-nfqd` release sidecar used by ask mode.
- Added real-VM lifecycle and named no-mount ask-mode end-to-end coverage.

### Changed

- Made `security.enforcement = "fail"` the default network policy.
- Versioned the applied-policy snapshot and refused reuse when the effective VM
  policy cannot be verified.
- Restricted additional host mount targets to `/mnt/watermelon`.
- Improved CLI status reporting, prompts, configuration documentation, and
  installer verification across supported host architectures.

### Security

- Hardened sandbox isolation, managed DNS, allowlist enforcement, VM ownership,
  mount boundaries, provisioning, and authenticated ask-mode communication.
- Gated releases on unit, race, vet, short end-to-end, artifact, and native
  installer smoke tests.

## [0.2.0] - 2026-04-23

### Added

- Added interactive `ask` network enforcement with a host verdict server and a
  guest NFQUEUE interceptor.
- Added process identification, DNS-query attribution, wildcard allowlist
  domains, deterministic subnets, and global wildcard process chains.
- Added decision caching and safe persistence of allow decisions to
  `.watermelon.toml`.
- Added the Watermelon skill for AI coding agents.
- Added broader tests for network interception, prompts, configuration,
  lifecycle operations, logging, and Lima execution.
- Added Renovate configuration and Discord pull-request notifications.

### Changed

- Renamed the `violations` command and package to `logs`.
- Renamed the `on_violation` configuration field to `enforcement` and added
  `ask` as an enforcement mode.

### Fixed

- Validated network allowlist domains before generating VM policy.
- Scoped ask-mode cache entries by domain and port and made configuration writes
  atomic.
- Hardened NFQUEUE DNS parsing, including empty-input handling.

## [0.1.8] - 2026-02-16

### Fixed

- Reworked per-process network namespaces for rootful containerd so container
  workloads receive the intended process-specific network policy.

## [0.1.7] - 2026-02-14

### Fixed

- Patched `nerdctl --network=host` to use the configured process-specific
  network namespace.

## [0.1.6] - 2026-02-14

### Fixed

- Prevented the VM boot script from hanging when a process-specific network
  policy requires `dnsmasq`.

## [0.1.5] - 2026-02-13

### Fixed

- Corrected Lima YAML generation for `network.process` heredocs.
- Improved TOML configuration examples.

## [0.1.4] - 2026-02-13

### Fixed

- Corrected provisioning wrappers and added pnpm and Bun examples.

## [0.1.3] - 2026-02-13

### Added

- Embedded the tagged release version at build time and added the global
  `--version` flag.
- Generalized sandbox-aware command wrappers across npm, pip, Cargo, Go, and
  RubyGems package managers.

## [0.1.2] - 2026-02-13

### Fixed

- Built persistent custom Lima images for provisioned packages so installed
  tools survive VM startup and reuse.

## [0.1.1] - 2026-02-12

### Added

- Added the tagged GitHub Release workflow and curl-based installer.

### Changed

- Made the curl installer the primary installation method in the README.

## [0.1.0] - 2026-02-12

Initial Watermelon release.

### Added

- Added `.watermelon.toml` parsing, defaults, and validation.
- Added Lima VM generation and lifecycle management.
- Added the `init`, `run`, `exec`, `stop`, `destroy`, `status`, `list`,
  `violations`, and `code` commands.
- Added project mounting, guest workdir selection, IDE/SSH integration,
  provisioning, resource limits, port forwarding, and per-process network
  policies.
- Added network-policy event logging, end-to-end tests, security guidance,
  troubleshooting documentation, and example configurations.
- Disabled Lima's automatic port forwarding to preserve the sandbox boundary.

[Unreleased]: https://github.com/SomosPollo/watermelon/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/SomosPollo/watermelon/compare/v0.4.2...v0.5.0
[0.4.2]: https://github.com/SomosPollo/watermelon/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/SomosPollo/watermelon/compare/v0.3.0...v0.4.1
[0.4.0]: https://github.com/SomosPollo/watermelon/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/SomosPollo/watermelon/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/SomosPollo/watermelon/compare/v0.1.8...v0.2.0
[0.1.8]: https://github.com/SomosPollo/watermelon/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/SomosPollo/watermelon/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/SomosPollo/watermelon/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/SomosPollo/watermelon/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/SomosPollo/watermelon/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/SomosPollo/watermelon/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/SomosPollo/watermelon/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/SomosPollo/watermelon/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/SomosPollo/watermelon/tree/v0.1.0
