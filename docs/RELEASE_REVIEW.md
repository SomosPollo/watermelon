# Managed workspace release review

Review date: 2026-10-21.

The implementation is a release candidate. Publication still requires passing
the repository's Linux real-VM, Lima compatibility, release-artifact identity,
and installer matrix gates on the final release commit. Local macOS validation
does not replace those gates. No release has been published by this review.

## Scope reviewed

- Public GitHub acquisition, guest workspace ownership, retained disks, and
  permanent deletion.
- Configuration validation, approval, policy drift, VM identity, lifecycle
  locks, cancellation, and setup recovery.
- Manifest inspection, nested projects, shared toolchains, package-manager
  pins, dependency commands, runtime launchers, and guest caches.
- Network defaults, host mounts and credentials, local port forwards, logging,
  status, command output, documentation, and release workflows.

## Fixes made during this review

- Removed writable host logging mounts from managed VMs. Logs now live on the
  persistent guest disk, within its storage limit. Log reads have a 4 MiB limit
  and a 10-second timeout; read/clear reject linked and non-regular files.
  Clearing preserves the logger's inode. Older development VM policies require
  rebuilding before workload reuse.
- Explicitly disabled host SSH-agent/X11 forwarding and extra SSH public keys
  for managed workspaces and automatic profiles.
- Bound setup startup to the inspected configuration digest and selected VM,
  including captured provision-script bytes. Settings changed during approval
  or before provisioning are rejected.
- Fixed runtime replacement with explicit VM names. Invocation-only overrides
  no longer become saved default names. Profile changes work before the first
  local VM exists.
- Bound rebuild confirmation to its original VM. Manual workspaces rebuild
  without forced automatic manifest detection or dependency installation.
- Removed obsolete default registry grants when languages/managers disappear,
  preserving custom grants outside profile defaults.
- Preserved Corepack integrity suffixes for pnpm/Yarn and recognized exact
  `devEngines.packageManager` pins. Arbitrary manager URLs remain rejected.
- Escaped repository-controlled command arguments in setup approval output.
  Internal VM removal now respects the command's output stream.
- Corrected spaced and multiple Ruby version constraints and zero-major caret
  bounds, rejected overflowing constraints, caught unsupported .NET targets
  earlier, and stopped treating every nested Gradle build as a member.
- Updated builds to Go 1.27.3 and `golang.org/x/mod` 0.40.0. The older local
  compiler produced reachable standard-library advisory findings; the patched
  build passes `govulncheck`.

The supported Corepack integrity format follows the
[Corepack documentation](https://github.com/nodejs/corepack#when-authoring-packages).
The compiler version was checked against the
[official Go downloads](https://go.dev/dl/).

## Validation

Local platform: macOS arm64 with Lima/VZ Linux guests.

- Unit tests, race detector, vet, CLI short E2E tests, and build pass.
- `govulncheck` reports no vulnerabilities for the patched build.
- All six Darwin/Linux amd64/arm64 CLI and Linux sidecar binaries cross-build
  with the patched compiler. The native macOS installer smoke test passes.
- Generated Lima configuration, shell helpers, runtime shim behavior, workflow
  YAML, and diff whitespace checks pass.
- Public GitHub/Node workflows pass with a deliberately unusable GitHub answer
  in Lima's host resolver, strict policy, dependency installation, restart,
  rebuild, retained edits, command output, file export, and port forwarding.
- Fresh extended-language VM verification passes (934 seconds), including
  initial setup, offline builds/imports, strict networking, restart, rebuild,
  and retained edits.
- Mixed Node/Python/Go/Rust VM verification passes (527 seconds), including
  shared hooks, offline builds, nested metadata refresh, restart, and rebuild.
- The final public Node workflow passes with the guest logging boundary
  (371 seconds). The dedicated guest-log fixture passes (124 seconds), proving
  no writable host state mount, actual policy events, log retention across a
  manual/no-profile rebuild, and safe clearing.

The extended fixture covers Java/Maven, Kotlin/Gradle, .NET 8/10, Ruby,
PHP/native extensions, CMake, pnpm, Yarn, uv, and Poetry in a shared sandbox.
The final logging and rebuild changes were then verified with the Node and
dedicated guest-log fixtures. Disposable test VMs, disks, and ownership fixtures
were removed after validation.

## Publication gates and supported boundaries

The versioned notes are under `0.5.0` in `CHANGELOG.md`. Before tagging, run CI
against the final commit with the manual `release_version=v0.5.0` input. This
builds exact-version artifacts using a runner-local tag and `--skip=publish`,
checks all four native installers, and never pushes a tag or creates a release. The release workflow requires real VM
security tests, Lima 2.0/current compatibility, six exact build identities, and
native installer smoke tests on Linux/macOS and amd64/arm64 before publication.

Private repository authentication, corporate proxy/split DNS integration,
unsupported platform SDKs, and custom system-library provisioning remain
explicit manual workflows. Local mounted projects expose their selected host
files read-write. Rebuilding a managed VM retains source, dependencies, caches,
and guest logs; it does not remove malicious files from that disk. These
boundaries are documented in [Project workflows](WORKSPACES.md) and
[Security model](SECURITY.md).
