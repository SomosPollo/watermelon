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
```

**Behavior:**
- Creates the VM on first run (may take a few minutes)
- Starts the VM if it was stopped
- Opens a bash shell with your project mounted at `/project`
- The VM persists after you exit (installed packages survive)

**VM naming:**
VMs are named `watermelon-{project}-{hash}` based on the project directory path, ensuring consistent naming across sessions.

---

## `watermelon exec`

Runs a single command inside the VM without an interactive shell.

```bash
watermelon exec "<command>"
```

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

## `watermelon violations`

Shows network requests that were blocked by the firewall.

```bash
watermelon violations          # Show all violations
watermelon violations --clear  # Clear the log
```

**Example output:**
```
2024-01-15 10:30:45  BLOCKED  evil-domain.com:443
2024-01-15 10:30:46  BLOCKED  tracker.example.org:80
```

**Behavior:**
- Reads from `.watermelon/violations.log` in the project directory
- Useful for discovering which domains a package needs
- Add legitimate domains to `[network].allow` in your config

**Workflow for discovering needed domains:**
1. Set `on_violation = "log"` in config
2. Run your command: `watermelon exec "npm install"`
3. Check violations: `watermelon violations`
4. Add needed domains to config
5. Retry
