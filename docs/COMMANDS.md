# Command reference

Run these commands on the host. Use `watermelon <command> --help` for built-in
usage. Most commands select the nearest `.watermelon.toml`; see
[project and VM selection](#project-and-vm-selection).

| Task | Commands |
|------|----------|
| Prepare | `doctor`, `init`, `clone`, `up` |
| Work | `exec`, `run`, `code`, `copy` |
| Inspect | `status`, `logs`, `list`, `config validate` |
| Manage | `rebuild`, `stop`, `destroy` |
| Integrate assistants | `agents install` |

## `watermelon doctor`

Checks host OS, architecture, Lima, SSH, backend readiness, and Linux QEMU/KVM
without changing a VM. Works outside a project.

```bash
watermelon doctor [--json]
```

`--json` emits ordered, versioned checks. `PASS` and `WARN` exit 0; any `FAIL`
exits 1. Resolve failures before setup. See [requirements](INSTALLATION.md#requirements).

## `watermelon init`

Creates `.watermelon.toml` in the current directory without running code or
creating a VM. Detects nested/mixed-language projects and adds marked guidance
to `AGENTS.md` and `CLAUDE.md`, preserving existing text and Watermelon blocks.

```bash
watermelon init [--template PROFILE] [--runtime VERSION]
watermelon init --instructions-only
```

| Flag | Meaning |
|------|---------|
| `--template` | `all` (default), `node`, `python`, `go`, `rust`, `java`, `dotnet`, `ruby`, `php`, `cpp`, or `manual` |
| `--runtime` | Numeric version for one language, or `node=22,python=3.13` |
| `--instructions-only` | Add guidance to an existing local config; cannot combine with template/runtime |

Existing configs are refused except with `--instructions-only`. With no
supported manifests, `init` writes a commented manual template. Instruction
files are checked before config creation; unsafe files or malformed markers
are refused. Automatic profiles use `up`; manual profiles use `run`.

## `watermelon agents install`

Installs the bundled skill for local Codex/Claude Code, without downloads or
API credentials. Works outside a project.

```bash
watermelon agents install
watermelon agents install --agent codex,claude
```

`--agent` accepts `codex`, `claude`, `all`, comma-separated names, or repeated
flags. Without it, detection uses `PATH` and existing assistant configuration
directories (`CODEX_HOME`/`CLAUDE_CONFIG_DIR` overrides supported). Explicit
selection works before an assistant's first launch.

| Assistant | Skill location |
|-----------|----------------|
| Codex | `~/.agents/skills/watermelon/SKILL.md`, even with `CODEX_HOME` set |
| Claude Code | `~/.claude/skills/watermelon/SKILL.md`, or `$CLAUDE_CONFIG_DIR/skills/watermelon/SKILL.md` |

Custom config directories must be absolute. Identical files are unchanged;
differing content is refused after preflighting all selected destinations.
Preserve conflicting skills before moving them aside. Symlinks, hard links,
and special files are refused. Restart the assistant session afterward.
Updating the CLI does not update installed skills.

The integration guides execution; it does not intercept commands or change
assistant permissions. See [assistant setup](CODING_ASSISTANTS.md). To remove
it, delete only the `watermelon` skill directory and the project's marked
`watermelon:begin` through `watermelon:end` blocks, including markers.

## `watermelon clone`

Acquires a public GitHub repository into `/workspace/project` on a retained
guest disk. The new host directory contains settings. See [clone workflow](WORKSPACES.md#clone-a-public-project).

```bash
watermelon clone [flags] https://github.com/owner/project [destination]
```

| Flag | Meaning |
|------|---------|
| `--template PROFILE` | `all` detected languages, or one automatic profile listed under `init` |
| `--runtime VERSION` | Select one runtime or `language=version,...` overrides |
| `--no-setup` | Acquire source without installing dependencies |
| `--yes`, `-y` | Approve the displayed setup plan without prompting |

The destination must not exist. Only credential-free public GitHub HTTPS URLs
are supported. Default setup displays a plan and asks before installation.

## `watermelon up`

Creates/starts the environment and prepares approved dependencies. Unchanged
manifests skip installation; changed runtimes/layouts require approval before
VM replacement.

```bash
watermelon up [--refresh] [--dry-run] [--runtime VERSION] [--yes] [--name NAME]
```

| Flag | Meaning |
|------|---------|
| `--refresh` | Reinstall despite unchanged inspected manifests |
| `--dry-run` | Inspect settings without VM operations; inspecting managed source requires live `up` |
| `--runtime VERSION` | Propose new runtime versions; unspecified languages keep approved versions |
| `--yes`, `-y` | Approve the displayed setup plan without prompting |
| `--name NAME` | Select a VM for a local mounted project |

Supports `fail`, `silent`, and explicitly selected `log`. For `ask`, use a
foreground `run` session and install interactively. See [runtime support](WORKSPACES.md#runtime-and-dependency-support).

## `watermelon rebuild`

Replaces VM system state and reapplies configuration, retaining host source
and managed disks. Interrupts work; preserve needed files elsewhere in the VM
first. Automatic profiles recheck dependencies; manual profiles stay manual.

```bash
watermelon rebuild [--name NAME] [--force] [--yes]
```

`--force`/`-f` confirms replacement. `--yes`/`-y` separately approves a changed
setup plan. Use both for an approved noninteractive rebuild. Rebuild does not
implicitly expand network or mount permissions. For manual `ask` mode, follow
the foreground recreation instructions printed by the CLI.

## `watermelon config validate`

```bash
watermelon config validate [--json]
```

Validates settings and referenced provision scripts without changing a VM.
Exits 0 for valid settings, 1 otherwise. JSON uses `schema_version: 1`.
Use `status` separately to compare recorded applied settings.

## `watermelon run`

Creates/starts the VM and opens an unprivileged guest shell. Automatic profiles
ensure approved setup is ready. Exiting leaves the VM running.

```bash
watermelon run [--name NAME] [--workdir PATH] [--no-shell]
```

`--workdir` selects an existing absolute guest directory for this shell.
Otherwise it uses `vm.workdir`, `/project` when mounted, or the guest login
directory. `--no-shell` starts without entering a shell and is unavailable in
`ask` mode. General passwordless `sudo` is removed; declare global tools in
manual `[provision]` instead.

In `ask`, keep the shell open to serve prompts. Each VM permits one foreground
`run`, `exec`, or `code` prompt controller at a time.

## `watermelon exec`

Runs a guest command, starting a stopped VM. Automatic profiles ensure setup
is ready; manual profiles need an existing VM created with `run`.

```bash
watermelon exec [--name NAME] [--cwd DIRECTORY] -- command [args...]
watermelon exec -- npm test
watermelon exec --cwd backend -- python -m pytest
watermelon exec -- sh -c 'npm install && npm run build'
```

Put Watermelon flags before the guest command. `--cwd` selects an existing
project-relative directory and Python environment; the host invocation
subdirectory is not mirrored. Manual tool wrappers use their configured
workdir; use tool options such as `npm --prefix frontend test` for them.
A single string containing spaces/operators runs through guest `sh -lc`;
multiple arguments are passed directly.

Redirected stdin goes to the guest. In Linux `ask`, verdicts use the foreground
controlling terminal independently; without one, requests block. macOS uses
native dialogs. The prompt controller stays active until the command exits.

Guest numeric exit codes 0–255 are preserved; signals use `128+N` (SIGINT 130).
Watermelon preparation/attachment/lease-release failures return 1. Status 255
can mean a guest result or a Lima/SSH transport failure. Setup output goes to
stderr so command stdout can be piped.

## `watermelon code`

Opens a remote editor and stays foreground until its remote window closes.
Automatic profiles ensure setup; manual profiles need an existing VM.

```bash
watermelon code [--name NAME]
```

Install the host editor CLI and Remote-SSH support. The CLI must implement
`--remote` and `--wait`; compatible examples include `code`, `cursor`, `codium`,
and `code-insiders`. Watermelon configures Lima entries in `~/.ssh/config`.

```toml
[ide]
command = "cursor"
# workdir = "/workspace/app"  # Optional existing guest directory
```

Directory precedence: `ide.workdir`, `vm.workdir`, `/project` when mounted,
then the editor's guest login default. IDE-only workdir changes do not affect
`run` or `exec`. In `ask`, this foreground process also serves prompts.
Manual SSH starts no prompt server and holds no Watermelon usage lease.

## `watermelon stop`

```bash
watermelon stop [--name NAME]
```

Stops immediately, terminating active sessions/transfers while preserving
VM state. Restart with `run` or `exec`. Through an invalid config, it attempts
to stop only a verifiably owned VM and still returns the config error.

## `watermelon destroy`

```bash
watermelon destroy [--name NAME] [--force] [--delete-workspace]
```

Deletes VM system state after confirmation. Host files and managed disks are
retained. **`--delete-workspace` also permanently deletes managed source,
uncommitted edits, and dependencies**; it requires valid ownership, refuses
active setup, and never force-deletes an attached disk.

`--force`/`-f` skips confirmation only. A running VM stops immediately; destroy
then waits for terminated Watermelon clients to detach before cleanup/name
reuse. Verified stale identity/policy state can be cleaned even if Lima has
already lost the VM. See recovery below.

## `watermelon status`

```bash
watermelon status [--name NAME] [--json]
```

Shows project path, VM name, running/stopped/missing state, and configured vs
recorded applied policy. Managed output includes workspace/setup state when
available. Missing VMs return success. JSON uses `schema_version: 1`.

“Current” means the host record matches configuration, including exact provision
script bytes; it is not a live firewall audit. Stale/unverified policy prints
recreation instructions. Keep configured scripts readable for this comparison.

## `watermelon list`

```bash
watermelon list
```

Lists VMs across projects: historical `watermelon-` names and registered custom
names. Registered ownership supplies project attribution, including no-mount
VMs. Unverified identities fail rather than guessing; absent VMs are omitted.

## `watermelon copy`

```bash
watermelon copy [--recursive | -r] source destination
watermelon copy ./file.txt VM_NAME:/tmp/
watermelon copy VM_NAME:/workspace/project/result.txt ./result.txt
```

Exactly one operand must use `vm-name:path`. No config discovery, `--name`, or
Watermelon ownership check occurs; target only a VM you own. Transfers hold a
usage lease, but stop/destroy can interrupt them and leave partial files.
Prefix local filenames containing colons with `./`.

## `watermelon logs`

```bash
watermelon logs [--name NAME] [--clear]
```

Reads a bounded tail of rate-limited IPv4 policy events. `--clear` removes
written records; in-flight events may appear afterward. Managed logs live on
the retained guest disk and require a running VM; `logs` does not start it.
Other registered VMs use per-VM host state; legacy mounted VMs use
`.watermelon/logs.log`. Use the command to resolve the correct location.

In `fail`, events were blocked; in `log`, they were allowed. `silent` records
none; `ask` uses prompts. See [network troubleshooting](TROUBLESHOOTING.md#network-issues).

## Ask-mode saved decisions

**Always Allow and Save** permits the literal IP/TCP port across all processes
for the current runtime, then saves the displayed bare host as a broader global
allow rule. Process/hostname labels are informational. After the session, use
the exact VM-pinned recreation command printed by Watermelon. Until then,
policy is stale and the next checked execution can stop the VM. See
[ask-mode limits](CONFIG_SPEC.md#security).

## Project and VM selection

Commands use the nearest config within [discovery boundaries](CONFIG_SPEC.md#file-location).
The config directory owns the VM, mounts, scripts, and policy state. `init`
writes only in the current directory; `doctor`, `agents install`, `list`, and
`copy` work outside a project.

Name precedence is `--name`, `vm.name`, then `watermelon-{project}-{hash}` from
the canonical project path. A name is project-owned, never a shared alias.
Normal `--name` use still requires valid settings and verified ownership.
Lifecycle decisions are serialized; workloads and transfers hold shared leases.
Stop interrupts immediately; destroy waits for client detachment after stopping.

For missing/invalid settings, `stop --name NAME` and `destroy --name NAME` can
recover only durably verified ownership. With missing config, run from the
owning root. Follow the VM-pinned command printed by Watermelon. Use Watermelon
recovery rather than deleting Lima instances directly.

Workload entry runs host/Lima/backend preflight. Stop/destroy remain available
with an older functional Lima for recovery. Config/preparation failures exit 1.
