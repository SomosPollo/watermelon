# Troubleshooting

Common issues and solutions.

## Installation Issues

### Lima not found

**Error:** `limactl: command not found`

**Solution:**
```bash
# macOS
brew install lima

# Linux
# Install Lima with your distro package manager or upstream package.
```

### Go not installed

**Error:** `go: command not found`

**Solution:**
```bash
# macOS
brew install go
```

Or download from [go.dev](https://go.dev/dl/).

---

## VM Issues

### VM won't start

**Check Watermelon and Lima status:**
```bash
watermelon status
watermelon list
limactl list
```

If recreation is necessary, first preserve any needed VM-local data. Then stop and recreate the current configured project through Watermelon so ownership records, applied-policy state, and active clients are handled together:

```bash
watermelon stop
watermelon destroy --force
watermelon run
```

For a fixed name when the local configuration is missing or invalid, run the ownership-verified recovery commands from its owning project directory:

```bash
watermelon stop --name my-project-vm
watermelon destroy --name my-project-vm --force
```

Do not use `limactl stop/delete watermelon-*` as a recovery shortcut. The shell wildcard does not enumerate Lima instance names, fixed names need not have the `watermelon-` prefix, and direct Lima deletion bypasses Watermelon's ownership checks, session coordination, and host identity cleanup. A raw deletion can leave a registered name reserved and unable to be recreated until its stale Watermelon state is cleaned up.

### VM creation is slow

First-time VM creation downloads the Ubuntu image and sets up the environment. This can take 2-5 minutes. Subsequent starts are faster.

### VM out of disk space

Increase disk size in config and recreate:

```toml
[resources]
disk = "30GB"
```

Then:
```bash
watermelon destroy --force
watermelon run
```

### Existing VM policy is unverified

VMs created before versioned policy records have only a legacy project digest, which does not identify the recorded enforcement mode. For safety, `watermelon run`, `exec`, and `code` require a one-time recreation:

```bash
watermelon destroy --force && watermelon run
```

This removes VM-local state but preserves project files on the host. `watermelon status` compares the configured policy with the host-side record written after VM creation; it does not inspect the live firewall.

---

## Command Issues

### "Command not found" inside VM

**Cause:** The tool is not configured in `.watermelon.toml`, or its package was installed ad hoc and no guest command wrapper was created.

**Solution:** Add the base command under `[tools]`:
```toml
[tools]
"node:20-slim" = ["node", "npm", "npx"]
```

For a global CLI package, declare it under `[provision]`, then recreate the VM so Watermelon can create its wrapper reliably:

```toml
[provision]
npm = ["typescript"]
```

```bash
# fail, log, or silent enforcement
watermelon destroy --force && watermelon run --no-shell

# ask enforcement: keep the resulting shell and prompt controller open
watermelon destroy --force && watermelon run
```

### Configured guest workdir does not exist

Watermelon validates an explicit `vm.workdir`, `ide.workdir`, or `run --workdir` path but does not create it. In no-mount mode, create a persistent configured workdir during provisioning and give it to the ordinary guest user:

```toml
[vm]
mount_project = false
workdir = "/home/watermelon/work"

[provision]
scripts = ["./vm/setup.sh"]
```

```bash
#!/bin/sh
set -eu
install -d -o watermelon -g watermelon -m 0755 /home/watermelon/work
```

Recreate the VM after adding the provisioning script. A one-off `run --workdir` path must be created in the guest before it is selected.

### Provision script cannot be read

Provision scripts remain host-side policy inputs after VM creation. `status`, `run`, `exec`, and `code` reread their exact bytes; moving, deleting, changing ownership of, or otherwise invalidating a configured script prevents verification. Restore the expected current-user-owned file to its configured project-relative path, or update the config and recreate the VM. Policy-checked execution refuses an unverifiable VM and may stop a securely bound running instance; `stop` and explicit-name `destroy` remain available for recovery.

### Sandbox provisioning is not complete

Lima can report a VM as running even when one of its provision scripts returned an error. Watermelon independently requires a root-owned completion marker covering its built-in setup, every configured `[provision].scripts` entry, and the generated workdir setup. If any stage fails, Watermelon does not record an applied-policy snapshot or enter the VM, and it stops a securely bound running instance.

Review the provisioning error printed during `watermelon run` and fix the failing script or configuration. For a transient failure, retry the exact `stop` and `run` commands printed with the error. If it persists, use the printed `destroy --force` and `run` commands to recreate the immutable VM; preserve any needed VM-local data first. In `ask` mode, keep the retrying `watermelon run` session in the foreground so its prompt controller remains available.

### `sudo` is denied inside the VM

This is intentional. Watermelon removes the VM user's general passwordless `sudo` access after system provisioning; interactive shells and `watermelon exec` are unprivileged. Express tools and global packages through `[tools]` and `[provision]`. Per-process command wrappers invoke only their narrowly authorized, root-owned launchers automatically.

### Command hangs

**Possible causes:**
1. A non-allowlisted request was blocked in `fail`, `silent`, or `ask` mode
2. Waiting for input (use `watermelon exec` for non-interactive)

**Check logs:**
```bash
watermelon logs
```

---

## Network Issues

### Ask-mode Always Allow made the configuration stale

This is expected. The observed process in an ask prompt is informational. **Always Allow and Save** permits the displayed TCP host and port for every process in the current VM runtime, then saves the bare host as a global `[network].allow` rule with no process, protocol, or port scope. Managed DNS redirection still applies.

The broader saved rule requires VM recreation. Finish the current Watermelon session, preserve any needed VM-local state, and run the exact, VM-pinned recreation command printed by Watermelon from the project root; recreation removes VM-local state. If that step is deferred, `watermelon status` reports the stale policy, and the next `run`, `exec`, or `code` stops a securely bound running VM before returning the recreation instruction. Stopping and starting the VM is not enough to apply the saved rule.

### Package installation fails

**Inspect network policy events:**
```bash
watermelon logs
```

In the default `fail` mode, these events represent blocked traffic. In discovery mode (`enforcement = "log"`), they represent IPv4 traffic that was allowed, so the installation failure has another cause. Policy-event logging is rate-limited; log-mode IPv6 traffic is allowed but not currently captured. `silent` does not record events, while `ask` reports decisions through its prompt.

**Add the domain to config:**
```toml
[network]
allow = [
    "registry.npmjs.org",
    "required-domain.com",  # Add only if trusted and required
]

[security]
enforcement = "fail"
```

Network policy changes require VM reprovisioning. In `fail`, `log`, or `silent`, run `watermelon destroy --force && watermelon run --no-shell`. In `ask`, omit `--no-shell` and keep the resulting interactive Watermelon shell open as the sole foreground prompt controller. Reprovisioning removes VM-local state but preserves project files on the host.

### Common domains by package manager

**npm:**
```toml
allow = ["registry.npmjs.org"]
```

**pip:**
```toml
allow = ["pypi.org", "files.pythonhosted.org"]
```

**cargo:**
```toml
allow = ["crates.io", "static.crates.io"]
```

**go:**
```toml
allow = ["proxy.golang.org", "sum.golang.org"]
```

### Git operations fail

```toml
[network]
allow = [
    "github.com",
    "*.githubusercontent.com",
]
```

---

## Port Issues

### Port not accessible on host

**Check port is forwarded:**
```toml
[ports]
forward = [3000]
```

**Verify inside VM:**
```bash
watermelon run
curl localhost:3000  # Should work inside VM
```

**On host:**
```bash
curl localhost:3000  # Should also work
```

### Port already in use

Stop other processes using the port, or use a different port:

```toml
[ports]
forward = [3001]  # Use alternative port
```

---

## Performance Issues

### Slow file operations

virtiofs has some overhead. For large `node_modules`:

1. Increase resources:
   ```toml
   [resources]
   memory = "4GB"
   cpus = 2
   ```

2. Consider using the VM's local filesystem for dependencies when possible

### High memory usage

Check if your workload needs more memory:

```toml
[resources]
memory = "8GB"  # Increase if needed
```

---

## Getting Help

1. Check logs: `watermelon logs`
2. Check VM status: `watermelon status`
3. Check Lima: `limactl list`
4. Review config: `cat .watermelon.toml`

If issues persist, [open an issue](https://github.com/saeta/watermelon/issues) with:
- Your `.watermelon.toml`
- Output of `watermelon status`
- Output of `limactl list`
- The error message
