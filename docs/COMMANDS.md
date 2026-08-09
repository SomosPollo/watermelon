# Command Reference

Detailed documentation for all watermelon commands.

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
watermelon code
```

**Behavior:**
- Requires the VM to exist (run `watermelon run` first)
- Starts the VM if it was stopped
- Automatically configures `~/.ssh/config` for Lima VMs (one-time setup)
- Launches your configured IDE with Remote-SSH to the VM
- Opens directly to `/project` directory

**IDE Configuration:**

By default, uses VS Code (`code`). Configure in `.watermelon.toml`:

```toml
[ide]
command = "cursor"  # Or "code", "codium", "code-insiders", etc.
```

**Supported IDEs:**
- VS Code (`code`)
- Cursor (`cursor`)
- VSCodium (`codium`)
- Any editor supporting `--remote ssh-remote+<host>` syntax

**Manual SSH Connection:**

If you prefer to connect manually:
```bash
ssh lima-watermelon-myapp-a1b2c3d4
```

The SSH host is printed when you run `watermelon run`.

---

## `watermelon run`

Enters an interactive shell inside the sandbox VM.

```bash
watermelon run
watermelon run --no-shell
```

**Behavior:**
- Creates the VM on first run (may take a few minutes)
- Starts the VM if it was stopped
- Opens a bash shell with your project mounted at `/project`
- The VM persists after you exit, so project dependencies and VM-local state survive
- With `--no-shell`, creates or starts the VM and exits without opening a shell

The shell runs as the ordinary VM user. General passwordless `sudo` is removed after system provisioning. For reproducible global CLIs and command wrappers, declare packages under `[provision]` and recreate the VM instead of relying on ad-hoc system changes.

**VM naming:**
VMs are named `watermelon-{project}-{hash}` based on the project directory path, ensuring consistent naming across sessions.

---

## `watermelon exec`

Runs a single command inside the VM without an interactive shell.

```bash
watermelon exec "<command>"
watermelon exec <command> [args...]
```

**Examples:**
```bash
watermelon exec "npm install"
watermelon exec "npm test"
watermelon exec "python -m pytest"
watermelon exec "npm install && npm run build"
watermelon exec npm install
```

**Behavior:**
- Requires the VM to already exist (run `watermelon run` first)
- Starts the VM if it was stopped
- Passes multi-argument commands directly to `limactl shell`
- Runs a single string containing spaces or shell operators through `sh -lc` inside the VM
- Returns the command's exit code
- Useful for CI/CD pipelines and scripts

---

## `watermelon stop`

Stops the VM while preserving all state.

```bash
watermelon stop
```

**Behavior:**
- Gracefully shuts down the VM
- All installed packages and files are preserved
- VM can be restarted with `watermelon run`

---

## `watermelon destroy`

Permanently deletes the VM and all its state.

```bash
watermelon destroy
watermelon destroy --force  # Skip confirmation
watermelon destroy -f       # Short form
```

**Behavior:**
- Prompts for confirmation (unless `--force`)
- Deletes the VM completely
- All installed packages are lost
- Project files on host are not affected

---

## `watermelon status`

Shows the status of the VM for the current project.

```bash
watermelon status
```

**Example output:**
```
Project:  /Users/dev/myapp
VM Name:  watermelon-myapp-a1b2c3d4
Status:   Running
Configured Policy: fail (blocks and logs connections outside the allowlist)
Applied Policy:    fail (blocks and logs connections outside the allowlist) (recorded, current)
```

`Configured Policy` comes from the current `.watermelon.toml`; `Applied Policy` comes from a host-side versioned record written after successful VM creation. “Current” means that record matches the current VM-affecting configuration; status does not inspect the live firewall. If the record is stale, missing, invalid, or from the legacy digest format, status reports it as unverified and prints the required recreation command.

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
NAME                          STATUS
watermelon-myapp-a1b2c3d4     Running
watermelon-other-e5f6g7h8     Stopped
```

---

## `watermelon logs`

Shows rate-limited IPv4 network policy events recorded by the firewall.

```bash
watermelon logs          # Show all logs
watermelon logs --clear  # Clear the log
```

**Example output:**
```
2024-01-15T10:30:45 kernel: watermelon-net ... DST=203.0.113.10 ... DPT=443
2024-01-15T10:30:46 kernel: watermelon-net ... DST=198.51.100.20 ... DPT=80
```

**Behavior:**
- Reads from `.watermelon/logs.log` in the project directory
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
