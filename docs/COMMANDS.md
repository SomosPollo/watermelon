# Command Reference

Detailed documentation for all watermelon commands.

For normal operation, commands that accept `--name` resolve and validate `.watermelon.toml` in the current project. The flag overrides `[vm].name`; it never adopts a VM owned by another project or creates one from default configuration. Two fail-closed recovery paths are deliberately narrower: `stop` can stop a securely verified project-owned VM while returning the config error, and `destroy --name` can use its durable ownership record when the local config is missing or invalid.

## `watermelon init`

Creates a `.watermelon.toml` configuration file in the current directory.

```bash
watermelon init
```

**Behavior:**
- Creates a commented template with all available options
- Fails if `.watermelon.toml` already exists
- Does not create or modify the VM

**Example output:**
```
Created .watermelon.toml
Edit this file to configure your sandbox, then run 'watermelon run'
```

---

## `watermelon code`

Opens your IDE connected to the sandbox VM via SSH.

```bash
watermelon code [--name <vm-name>]
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Override `[vm].name` and the path-derived name for this project |

**Behavior:**
- Requires the VM to exist (run `watermelon run` first)
- Starts the VM if it was stopped
- Automatically configures `~/.ssh/config` for Lima VMs (one-time setup)
- Launches your configured IDE with Remote-SSH to the VM and passes `--wait`
- Remains in the foreground until the IDE's remote window exits; keep this terminal open for the duration of the IDE session
- Opens `[ide].workdir`, then `[vm].workdir`, or `/project` when the project mount uses its default workdir
- Omits the remote directory argument when the project is not mounted and neither workdir is configured, allowing the IDE to use the guest login directory
- In `ask` mode, keeps the host prompt server active for the entire foreground IDE session; no other ask-mode `run`, `exec`, or `code` prompt controller can be active for that VM at the same time

**IDE Configuration:**

By default, uses VS Code (`code`). Configure in `.watermelon.toml`:

```toml
[ide]
command = "cursor"  # Or "code", "codium", "code-insiders", etc.
workdir = "/workspace/app"  # Optional IDE-only remote directory
```

`ide.workdir` affects only `watermelon code`. Interactive shells and `watermelon exec` use `vm.workdir`.

The configured command must implement the VS Code CLI-compatible `--remote ssh-remote+<host>` and `--wait` options. Watermelon relies on `--wait` to keep the prompt server and VM usage lease alive until the remote IDE window closes. An editor command that ignores or rejects `--wait` is not compatible with `watermelon code`.

**Supported IDEs:**
- VS Code (`code`)
- Cursor (`cursor`)
- VSCodium (`codium`)
- VS Code Insiders (`code-insiders`)
- Other VS Code-compatible CLIs supporting both `--remote` and `--wait`

**Manual SSH Connection:**

If you prefer to connect manually:
```bash
ssh lima-watermelon-myapp-a1b2c3d4
```

The SSH host is printed when you run `watermelon run`.

A manual SSH process is outside Watermelon's session coordination: it neither starts an `ask`-mode prompt server nor holds the usage lease that delays deletion. In `ask` mode, keep a Watermelon foreground prompt controller active separately if the manually connected workload needs prompted network access. A concurrent `watermelon stop` or `destroy` can terminate the manual connection immediately.

---

## `watermelon run`

Enters an interactive shell inside the sandbox VM.

```bash
watermelon run [--name <vm-name>] [--workdir <guest-path>]
watermelon run --no-shell
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Override `[vm].name` and the path-derived name for this project |
| `--workdir <path>` | Override the configured guest working directory for this shell; the directory must already exist |
| `--no-shell` | Create or start the VM without opening a shell; unavailable with `ask` enforcement |

**Behavior:**
- Creates the VM on first run (may take a few minutes)
- Starts the VM if it was stopped
- Opens a bash shell at `--workdir`, `[vm].workdir`, `/project` for the default mounted-project configuration, or the guest login directory in no-mount mode
- The VM persists after you exit, so project dependencies and VM-local state survive
- With `--no-shell`, creates or starts the VM and exits without opening a shell, except when enforcement is `ask`

The shell runs as the ordinary VM user. General passwordless `sudo` is removed after system provisioning. For reproducible global CLIs and command wrappers, declare packages under `[provision]` and recreate the VM instead of relying on ad-hoc system changes.

`ask` is deliberately foreground-only. `watermelon run --no-shell` is rejected because no host process would remain to display network prompts. Run without `--no-shell` and keep Watermelon and its interactive shell open; the prompt server remains available until that shell exits. One saved host port connects the VM to one foreground prompt controller, so wait for that controller to exit before starting another ask-mode `run`, `exec`, or `code` command for the same VM.

**VM naming:**

The VM name is selected in this order:

1. `--name`
2. `[vm].name`
3. `watermelon-{project}-{hash}`, derived from the canonical project path

Custom names use lowercase letters, digits, and single `.`, `_`, or `-` separators, are at most 76 bytes, and are owned by their creating project. The lowercase restriction prevents case-variant names from aliasing one Lima directory on common macOS filesystems. Watermelon records and verifies that identity, rejects a name already used by another project or by an unmanaged Lima instance, and refuses name-selecting lifecycle and log commands from a different project.

Watermelon serializes short lifecycle decisions for the same VM name, then interactive shells, `exec` commands, foreground IDE sessions, and `copy` transfers hold a shared usage lease. Subject to ask mode's single prompt-controller limit, shared leases allow other non-destructive Watermelon commands and sessions to proceed. `watermelon stop` remains an immediate interrupt: it does not wait for clients, and stopping the VM ends their connections or transfers. `watermelon destroy` also stops a running VM immediately, then waits for the terminated Watermelon clients to release their usage leases before it deletes VM state. The wait protects cleanup and later name reuse; it does not let guest work finish before shutdown.

**No-mount mode:**

```toml
[vm]
mount_project = false
# workdir = "/home/watermelon/work"  # Optional
```

The host project is not mounted and `/project` is not assumed. With an explicit `vm.workdir`, shells, exec commands, and tool containers use it, but Watermelon does not create the directory. Create it during provisioning with ownership suitable for the ordinary `watermelon` guest user before the first command enters it. The same pre-existing-directory requirement applies to `run --workdir` and `ide.workdir`. Without a configured workdir, the shell begins in the guest login directory and tool wrappers bind the guest's current directory into their container at the same path.

---

## `watermelon exec`

Runs a single command inside the VM without an interactive shell.

```bash
watermelon exec [--name <vm-name>] -- <command> [args...]
watermelon exec [--name <vm-name>] "<shell command>"
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Override `[vm].name` and the path-derived name for this project |

Watermelon flags must appear before the guest command. Use `--` to make the boundary explicit, especially when the guest command has flags of its own.

**Examples:**
```bash
watermelon exec "npm install"
watermelon exec "npm test"
watermelon exec "python -m pytest"
watermelon exec "npm install && npm run build"
watermelon exec -- npm install
watermelon exec --name dev -- docker run --name web nginx
```

**Behavior:**
- Requires the VM to already exist (run `watermelon run` first)
- Starts the VM if it was stopped
- Passes multi-argument commands directly to `limactl shell`
- Runs a single string containing spaces or shell operators through `sh -lc` inside the VM
- Runs in `[vm].workdir`, `/project` by default when mounted, or the guest login directory when no workdir is configured; an explicit workdir must already exist in the guest
- Preserves flags after the guest command starts; for example, Docker's `--name` is not interpreted as Watermelon's flag
- In `ask` mode, runs the host prompt server for the command's full duration, then shuts it down when the command exits; another foreground ask prompt controller for that VM prevents this command from starting
- Returns the command's exit code
- Useful for CI/CD pipelines and scripts

---

## `watermelon stop`

Stops the VM while preserving all state.

```bash
watermelon stop [--name <vm-name>]
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Override `[vm].name` and the path-derived name for this project |

**Behavior:**
- Gracefully shuts down the VM
- All installed packages and files are preserved
- VM can be restarted with `watermelon run`
- Stops immediately even when interactive shells, commands, or IDE sessions are active; those sessions terminate as the VM shuts down
- If a present config is malformed or unreadable, returns that error but first attempts to stop running VMs whose project binding or durable ownership can still be verified; with explicit `--name`, the same recovery also applies when the config is missing

---

## `watermelon destroy`

Permanently deletes the VM and all its state.

```bash
watermelon destroy [--name <vm-name>]
watermelon destroy --force  # Skip confirmation
watermelon destroy -f       # Short form
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Override `[vm].name` and the path-derived name for this project |
| `--force`, `-f` | Skip the confirmation prompt |

**Behavior:**
- Prompts for confirmation (unless `--force`)
- Deletes the VM completely
- All installed packages are lost
- Project files on host are not affected
- If the VM is running, stops it immediately, terminating active interactive shells, commands, and IDE connections
- After shutdown, waits for terminated Watermelon client processes to detach and release their usage leases before deleting the VM; `--force` skips confirmation, not this cleanup-safety wait
- Revalidates project ownership before deletion and removes the corresponding host identity and policy state
- With an explicit `--name`, can recover a custom-named VM when the local config is missing or invalid, but only when its durable identity proves that the current project owns it
- If Lima no longer has that verified VM, removes its stale Watermelon identity and policy state without asking Lima to delete another instance

---

## `watermelon status`

Shows the status of the VM for the current project.

```bash
watermelon status [--name <vm-name>]
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Override `[vm].name` and the path-derived name for this project |

**Example output:**
```
Project:  /Users/dev/myapp
VM Name:  watermelon-myapp-a1b2c3d4
Status:   Running
Configured Policy: fail (blocks and logs connections outside the allowlist)
Applied Policy:    fail (blocks and logs connections outside the allowlist) (recorded, current)
```

`Configured Policy` comes from the current `.watermelon.toml`; `Applied Policy` comes from a host-side versioned record written after successful VM creation. “Current” means that record matches the current VM-affecting configuration; status does not inspect the live firewall. For an existing VM, status labels a changed record as stale and missing, invalid, or legacy records as unverified, then prints the required recreation command. The comparison rereads the exact bytes of every provision script. Keep those host files present and readable: editing a script makes the configuration stale, while a missing, unreadable, or newly invalid script prevents status from completing the comparison.

**Status values:**
- `Running` - VM is active
- `Stopped` - VM exists but is not running
- `Not found` - No VM exists for this project

---

## `watermelon list`

Lists all watermelon VMs across all projects.

```bash
watermelon list
```

**Example output:**
```
NAME                          STATUS    PROJECT
watermelon-myapp-a1b2c3d4     Running   /Users/dev/myapp
fixed-build-vm                Stopped   /Users/dev/build
```

The list includes existing Lima VMs with Watermelon's historical `watermelon-` name prefix and existing registered custom-named VMs. A legacy row derives `PROJECT` from its `/project` mount when available. For a registered name, `PROJECT` comes from Watermelon's secure ownership record, so no-mount VMs remain attributable. Arbitrary unregistered custom names are excluded. If a present registered VM's identity or read-only identity mount cannot be verified, listing fails rather than guessing its owner. A stale identity whose Lima VM is already gone is not shown; use `watermelon destroy --name <name>` from its owning project to remove that host state.

---

## `watermelon copy`

Copies files between the host and one Lima VM.

```bash
watermelon copy [--recursive] <src> <dst>
```

Exactly one operand must use `vm-name:path` syntax:

```bash
# Host to VM
watermelon copy ./file.txt my-project-vm:/tmp/

# VM to host
watermelon copy my-project-vm:/tmp/output.log ./

# Directory copy
watermelon copy -r ./scripts/ my-project-vm:/home/watermelon/scripts/
```

| Flag | Description |
|------|-------------|
| `--recursive`, `-r` | Copy directories recursively |

The VM name is explicit; `copy` does not accept `--name` or infer a VM from `.watermelon.toml`. It is a low-level Lima copy operation and validates the operand syntax, not Watermelon project ownership, so target only a VM you own. Watermelon does coordinate the selected name and holds a shared usage lease for the full transfer: `destroy` waits for the copy client to detach before deleting state or reusing the name. `stop` and `destroy` still stop a running VM immediately, so either can interrupt a transfer and leave a partial destination. If a local filename contains a colon, prefix it with `./` so it is unambiguously local, for example `./report:2026.txt`.

---

## `watermelon logs`

Shows rate-limited IPv4 network policy events recorded by the firewall.

```bash
watermelon logs [--name <vm-name>]  # Show all logs
watermelon logs --clear              # Clear the log
watermelon logs --name build --clear # Clear a named VM's log
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Override `[vm].name` and select that project's registered VM log |
| `--clear` | Clear the selected log |

**Example output:**
```
2024-01-15T10:30:45 kernel: watermelon-net ... DST=203.0.113.10 ... DPT=443
2024-01-15T10:30:46 kernel: watermelon-net ... DST=198.51.100.20 ... DPT=80
```

**Behavior:**
- Reads the registered per-VM host log for custom-named, no-mount, and `ask` VMs; legacy path-derived mounted VMs use `.watermelon/logs.log` in the project
- Refuses a custom name whose secure identity record is missing or owned by another project
- In `fail`, each event represents traffic that was blocked
- In `log`, each event represents IPv4 traffic that was observed and allowed; IPv6 is allowed but is not currently captured
- `silent` does not record policy events; `ask` reports decisions through its prompt instead
- Useful for discovering which destinations a package attempts to reach
- Add legitimate domains to `[network].allow` in your config

**Workflow for discovering needed domains:**
1. Set `enforcement = "log"` in config, understanding that this temporarily allows non-allowlisted traffic
2. Recreate the VM to apply discovery mode: `watermelon destroy --force && watermelon run --no-shell`
3. Run your command: `watermelon exec "npm install"`
4. Inspect policy events: `watermelon logs`
5. Add needed domains and restore strict mode with `enforcement = "fail"`
6. Recreate the VM to apply the strict policy: `watermelon destroy --force && watermelon run --no-shell`
7. Retry

Destroying the VM removes its VM-local state; project files on the host remain.
