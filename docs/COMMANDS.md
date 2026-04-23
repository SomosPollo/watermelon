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
watermelon code [--name <vm-name>]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific VM name instead of deriving from config or directory |

**Behavior:**
- Requires the VM to exist (run `watermelon run` first)
- Starts the VM if it was stopped
- Automatically configures `~/.ssh/config` for Lima VMs (one-time setup)
- Launches your configured IDE with Remote-SSH to the VM
- Opens directly to the project workdir (default: `/project`)

**IDE Configuration:**

By default, uses VS Code (`code`). Configure in `.watermelon.toml`:

```toml
[ide]
command = "cursor"  # Or "code", "codium", "code-insiders", etc.
workdir = "/home/user/project"  # Override the remote directory (optional)
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
watermelon run [--name <vm-name>] [--workdir <path>]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific VM name instead of deriving from config or directory |
| `--workdir <path>` | Override the starting directory inside the VM |

**Behavior:**
- Creates the VM on first run (may take a few minutes)
- Starts the VM if it was stopped
- Opens a bash shell at the configured workdir (default: `/project` when project is mounted)
- The VM persists after you exit (installed packages survive)

**VM naming:**

VM names are determined in the following order:
1. `--name` flag
2. `[vm] name` in `.watermelon.toml`
3. Auto-derived: `watermelon-{project}-{hash}` based on the project directory path

**No-mount mode:**

When `mount_project = false` in your config, the project directory is not mounted into the VM and no default workdir is set:

```toml
[vm]
mount_project = false
```

This is useful when the VM itself is the development environment (e.g. a shared Docker host VM).

---

## `watermelon exec`

Runs a single command inside the VM without an interactive shell.

```bash
watermelon exec [--name <vm-name>] "<command>"
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific VM name instead of deriving from config or directory |

**Examples:**
```bash
watermelon exec "npm install"
watermelon exec "npm test"
watermelon exec "python -m pytest"
watermelon exec "npm install && npm run build"
```

**Behavior:**
- Requires the VM to already exist (run `watermelon run` first)
- Starts the VM if it was stopped
- Returns the command's exit code
- Useful for CI/CD pipelines and scripts

---

## `watermelon stop`

Stops the VM while preserving all state.

```bash
watermelon stop [--name <vm-name>]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific VM name instead of deriving from config or directory |

**Behavior:**
- Gracefully shuts down the VM
- All installed packages and files are preserved
- VM can be restarted with `watermelon run`

---

## `watermelon destroy`

Permanently deletes the VM and all its state.

```bash
watermelon destroy [--name <vm-name>] [--force]
watermelon destroy --force  # Skip confirmation
watermelon destroy -f       # Short form
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific VM name instead of deriving from config or directory |
| `--force`, `-f` | Skip confirmation prompt |

**Behavior:**
- Prompts for confirmation (unless `--force`)
- Deletes the VM completely
- All installed packages are lost
- Project files on host are not affected

---

## `watermelon status`

Shows the status of the VM for the current project.

```bash
watermelon status [--name <vm-name>]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific VM name instead of deriving from config or directory |

**Example output:**
```
Project: /Users/dev/myapp
VM:      watermelon-myapp-a1b2c3d4
Status:  Running
```

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

## `watermelon copy`

Copies files between the host and a VM.

```bash
watermelon copy [--recursive] <src> <dst>
watermelon copy -r <src> <dst>  # Short form
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--recursive`, `-r` | Copy directories recursively |

**Arguments:**

Exactly one of `<src>` or `<dst>` must contain a colon (`:`) to specify the VM side. The format is `<vm-name>:<path>`.

**Examples:**
```bash
# Host → VM
watermelon copy ./file.txt somospollo-vm:/tmp/

# VM → Host
watermelon copy somospollo-vm:/tmp/output.log ./

# Copy a directory recursively to the VM
watermelon copy -r ./scripts/ somospollo-vm:/home/user/scripts/

# Copy a directory from the VM to the host
watermelon copy -r somospollo-vm:/home/user/logs/ ./logs/
```

**Behavior:**
- Wraps `limactl copy` (which uses `scp` under the hood)
- Use `-r` for directories
- VM name must match exactly — use `watermelon list` to confirm the name

---

## `watermelon logs`

Shows network requests that were blocked by the firewall.

```bash
watermelon logs [--name <vm-name>]
watermelon logs --clear       # Clear the log
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific VM name (currently a no-op; logs are per project directory) |
| `--clear` | Clear the log file |

**Example output:**
```
2024-01-15 10:30:45  BLOCKED  evil-domain.com:443
2024-01-15 10:30:46  BLOCKED  tracker.example.org:80
```

**Behavior:**
- Reads from `.watermelon/logs.log` in the project directory
- Useful for discovering which domains a package needs
- Add legitimate domains to `[network].allow` in your config

**Workflow for discovering needed domains:**
1. Set `enforcement = "log"` in config
2. Run your command: `watermelon exec "npm install"`
3. Check logs: `watermelon logs`
4. Add needed domains to config
5. Retry
