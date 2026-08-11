# Remaining UX and Distribution Backlog

This document is a handoff for continuing the Watermelon UX audit in a later
session. It records only issues that were still present on `main` at commit
`fac3608` on 2026-08-10, after PRs #9, #10, and #11 were merged.

Before starting work, fetch `main` and verify that each item is still current.
Release state and external links can change independently of the repository.

## Recommended order

1. Cut and validate a release containing the strict-by-default implementation.
2. Fix destructive-command argument validation and guest exit-code propagation.
3. Fix ask-mode prompt semantics and terminal ownership.
4. Improve installation and dependency diagnostics.
5. Add project-root discovery and centralized config validation.
6. Improve onboarding, examples, runtime-state hygiene, and remaining diagnostics.

## P0: Release the hardened implementation

- [x] Publish a release from current `main`.

As of 2026-08-10, GitHub's latest release is `v0.2.0`. That tag still defaults
to permissive `enforcement = "log"` and contains only these assets:

- `watermelon-darwin-amd64`
- `watermelon-darwin-arm64`

Consequences:

- The documented macOS installer installs the old permissive behavior.
- Linux installation through `install.sh` cannot find a Linux CLI asset.
- No release contains the Linux nfqd sidecar required by `ask` mode.

The current release workflow builds Darwin and Linux CLI binaries plus both
Linux nfqd architectures. Before tagging, make the release path run the full
test suite so a tag cannot publish untested artifacts.

Evidence:

- `README.md`: documented latest-release and `go install` paths
- `.github/workflows/release.yml`: asset build and publication workflow
- tag `v0.2.0`: `internal/config/types.go` and `internal/cli/init.go` default to
  `log`

Acceptance criteria:

- The latest release defaults omitted and initialized enforcement to `fail`.
- Darwin/Linux amd64/arm64 CLI assets are present.
- Linux amd64/arm64 nfqd assets are present.
- Installer smoke tests pass on supported OS/architecture combinations.
- Release notes clearly describe VM recreation and mount-target migration.

## P1: CLI correctness and safety

### Reject positional arguments on no-argument commands

- [x] Add `cobra.NoArgs` to `run`, `init`, `stop`, `destroy`, `status`, `list`,
  `logs`, and `code`.

Only `exec` and `copy` currently validate positional arguments. A particularly
dangerous typo is:

```bash
watermelon destroy some-name --force
```

`some-name` is ignored, so the command still targets the current project or
`--name` selection.

Evidence: `internal/cli/destroy.go` and each command constructor.

Acceptance criteria:

- Every command has an explicit positional-argument contract.
- Destructive commands fail before resolving or mutating a VM when extra
  arguments are supplied.
- Regression tests cover `destroy`, `stop`, and representative read commands.

### Preserve guest exit codes

- [x] Make `watermelon exec` exit with the guest command's exit code.

`internal/lima.Exec` returns `*exec.ExitError`, but `cmd/watermelon/main.go`
maps every command error to process exit code 1. This makes Watermelon unreliable
as a wrapper in scripts and CI.

Acceptance criteria:

- Guest exit codes from 1 through 255 are propagated unchanged where possible.
- Watermelon-owned setup/validation failures retain a documented CLI error code.
- Signal termination behavior is documented and tested.
- The command-reference claim about exit propagation matches implementation.

### Print errors once, without runtime usage noise

- [x] Configure Cobra error and usage behavior centrally.

The root command currently lets Cobra print an error and often command usage,
then `main` prints the returned error again.

Acceptance criteria:

- Each failure is printed once.
- Usage is printed for invocation/syntax errors, not runtime or policy failures.
- Error formatting is consistent across commands.

## P1: Ask-mode UX and correctness

### Give prompts exclusive access to a controlling terminal

- [x] Stop the verdict prompt and guest process from racing over `os.Stdin`.

On Linux, `ask.ShowTerminalPrompt` reads global stdin while `limactl shell` and
`limactl exec` attach that same stream to the guest. Prompt answers can reach
the guest, and guest input can be consumed as a verdict. Non-TTY/piped input
cannot display a prompt and blocks by default.

Evidence:

- `internal/ask/dialog.go`: terminal prompt reads `os.Stdin`
- `internal/lima/lifecycle.go`: shell and exec attach `os.Stdin`

Acceptance criteria:

- Prompts use the controlling TTY independently of guest stdin.
- Interactive guest input cannot be consumed as a verdict.
- Piped exec behavior is explicit and documented.
- Tests cover simultaneous prompt and guest input.

### Make "Always Allow" match the displayed decision

- [x] Preserve the intended process and port scope, or clearly present a global
  host-only decision.
- [x] Avoid destructive TOML re-encoding.
- [x] Define how a saved decision becomes active without surprising stale-policy
  shutdown on the next command.

Resolved by labeling the observed process as informational and presenting the
saved decision as a global bare-host rule with no process, protocol, or port
scope. The writer now edits only `[network].allow`, preserving unrelated bytes
and comments. After a save, Watermelon distinguishes the current TCP decision
from the broader persisted rule and prints a VM-pinned recreation command before
the next policy-checked command can encounter the stale applied snapshot.

Evidence:

- `internal/ask/server.go`
- `internal/ask/tomlwriter.go`
- applied-policy checks in `internal/cli/run.go`

Acceptance criteria:

- The UI states the exact persisted scope.
- Persistence preserves unrelated TOML bytes/comments where practical.
- The user receives an immediate, actionable explanation of whether recreation
  is required.

## P1: Installation and dependency diagnostics

### Make `go install` truthful and complete

- [x] Install or otherwise obtain the matching nfqd sidecar for `ask` mode.
- [x] Derive versions from Go build information when ldflags are absent.
- [x] Document any intentional capability differences.

Resolved by deriving tagged Go-install versions from embedded build information
and lazily downloading the exact matching nfqd release asset when an `ask` VM
is first created. The download is checked against GitHub's release digest and
its embedded Go build identity. Shell installs bundle nfqd eagerly, non-ask
modes do not require it, and unreleased or custom builds require an explicit
sibling sidecar or `WATERMELON_NFQD_BINARY`.

Evidence:

- `README.md`
- `cmd/watermelon/main.go`
- nfqd discovery in `internal/cli/run.go` and verified release retrieval in
  `internal/cli/nfqd_release.go`

### Improve the shell installer and add a doctor/preflight path

- [ ] Support a configurable or user-local install directory.
- [ ] Do not assume `sudo` exists.
- [ ] Check `curl`, Lima availability, and a supported Lima version.
- [ ] Execute the installed binary by its resolved path for verification.
- [ ] Add `watermelon doctor` or equally actionable early diagnostics.

`install.sh` hard-codes `/usr/local/bin`, assumes that an unwritable directory
can be handled with `sudo`, and does not preflight `limactl`. A missing Lima
binary later appears only as an unknown VM state.

Acceptance criteria:

- Installation works without root into a documented user directory.
- Missing dependencies fail early with platform-specific installation guidance.
- `watermelon doctor` is useful in support tickets and automation.

## P2: Project and configuration UX

### Discover the project root from subdirectories

- [ ] Walk parent directories for `.watermelon.toml`.

Target resolution currently treats the exact current directory as the project
root. Running from `project/src` does not discover `project/.watermelon.toml`.

Design constraints:

- Stop at a filesystem or VCS boundary.
- Resolve one canonical root and use it consistently for VM identity, mounts,
  policy records, logs, provision scripts, and workdirs.
- Print or expose the resolved root when useful for diagnostics.

Evidence: `internal/cli/target.go` and `loadProjectConfig` in
`internal/cli/run.go`.

### Centralize resource and port validation

- [ ] Validate memory and disk syntax/positivity in `config.Validate`.
- [ ] Validate forwarded-port range and duplicates in `config.Validate`.
- [ ] Return precise field paths.

Today memory/disk are checked only for nonempty strings and then transformed by
a simple `GB` to `GiB` replacement. Forwarded ports are checked only during
Lima generation. Consequently `status` can call malformed values valid before
creation fails later.

Acceptance criteria:

- Supported size units and normalization are defined in one place.
- Zero, negative, malformed, overflow, and duplicate values are rejected early.
- `status`, generation, and any future validation command share the same rules.

### Validate provisioning by exposed commands, not image-name substrings

- [ ] Replace `hasToolImage` image-name heuristics with command-based checks.

Config validation guesses package-manager support from image names, while Lima
generation checks commands exposed in `[tools]`. A custom JavaScript image that
exposes npm can be rejected, while an image containing `node` in its name but
not exposing npm can pass early validation and fail later.

Evidence:

- `hasToolImage` in `internal/config/validate.go`
- `findImageForCommand` in `internal/lima/generate.go`

### Add automation-friendly config and status interfaces

- [ ] Add `watermelon config validate`.
- [ ] Add JSON or another stable machine-readable status mode.
- [ ] Document exit semantics for missing config, missing VM, invalid config,
  stale policy, and runtime failures.
- [ ] Consider aggregating and sorting semantic validation errors.

The current `status` output is human prose. Missing config/VM is reported with
exit success, requiring automation to parse text.

## P2: Onboarding and examples

### Add useful init presets or project detection

- [ ] Provide presets such as `watermelon init --template node` and `python`, or
  safely detect common project manifests.
- [ ] Keep a minimal/manual mode.

Quick Start is now accurate, but `init` still produces an empty allowlist and no
active tool. Users must manually uncomment both the tool and required domains
before the advertised `npm install` flow works.

### Make the init template and docs agree

- [ ] Include or link every supported option.
- [ ] Add `[network.process]` and package-provision examples.
- [ ] Print a concise next step.
- [ ] Correct `docs/COMMANDS.md` if the template intentionally remains minimal.

The template includes `[provision]` but only shows `scripts`; it omits `npm`,
`pip`, `cargo`, `go`, `gem`, and `[network.process]`. Documentation claims it
contains all options and shows a next-step line that the command does not print.

### Make examples available to installed-binary users

- [ ] Embed supported examples as init presets, or document stable raw-download
  commands.

Release assets contain binaries only, while the docs tell users to copy paths
from a local Watermelon checkout.

## P2: Runtime compatibility and state hygiene

### Stop hard-coding Google DNS upstreams

- [ ] Discover and safely preserve usable host/Lima upstream resolvers.
- [ ] Support VPN and split-horizon DNS.
- [ ] Consider an explicit resolver configuration override.

Both global and per-process dnsmasq configurations use only `8.8.8.8` and
`8.8.4.4`, and the firewall admits only those upstreams. Networks that block
public DNS therefore break log/ask resolution and strict wildcard rules.

Evidence: `internal/lima/generate.go`.

### Supervise per-process resolvers

- [ ] Run per-process dnsmasq instances as supervised services or include them
  in runtime policy health verification.

They are currently launched directly as daemons. A crash leaves the configured
process without DNS until policy reprovision or VM restart.

### Keep runtime artifacts out of project status

- [ ] Move default runtime state to trusted host state, or generate a complete
  `.watermelon/.gitignore`.
- [ ] Correct documentation that calls project-local logs legacy while fresh
  default path-derived VMs still use them.

Fresh default VMs create `.watermelon/logs.log`; ask mode can also create local
state. `init` does not update or advise about the user's ignore rules.

## P3: Documentation cleanup

- [ ] Fix the support link in `docs/TROUBLESHOOTING.md`.

It currently points to `https://github.com/saeta/watermelon/issues`, which is
obsolete. Other `saeta-eth` links currently redirect to the `SomosPollo`
repository and should be normalized to avoid relying on redirects.

## Resolved items from the original audit

Do not reopen these without new evidence:

- The default and generated config now use strict `fail` enforcement.
- Explicit `log` mode is labeled as permissive discovery.
- Unknown TOML keys are rejected.
- Guest flags after `watermelon exec <command>` are passed through correctly.
- Existing VMs are not silently reused after VM-affecting config changes.
- Versioned host-side applied-policy records and project/VM identity binding are
  enforced.
- Strict bootstrap pre-pulls configured tool images before lockdown.
- Managed DNS mediates direct workload DNS; enforcing modes disable IPv6.
- Reboot and provisioning failure paths install and verify fail-closed policy.
- Lima command failures and transitional states are no longer treated as a
  missing VM, although diagnostics still collapse them to `Unknown`.
- `ask` mode rejects `run --no-shell`, retains its server through `exec`, and
  keeps `code` in the foreground until the IDE closes.

## Verification baseline

The strict-networking work previously passed:

```bash
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go test -tags=e2e -short -count=1 ./test/...
```

Generated configurations also passed `limactl validate`, `bash -n`, helper
syntax checks, and `visudo -cf`. The real VM lifecycle test was skipped on the
audit host because `/dev/kvm` was unavailable.

For each backlog item, add focused regression tests and rerun the full baseline.
