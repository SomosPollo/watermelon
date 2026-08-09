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

**Check Lima status:**
```bash
limactl list
```

**If Lima has issues:**
```bash
limactl stop watermelon-*  # Stop all watermelon VMs
limactl delete watermelon-* # Delete and recreate
```

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
watermelon destroy --force && watermelon run --no-shell
```

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

Network policy changes require VM reprovisioning: `watermelon destroy --force && watermelon run --no-shell`. This removes VM-local state but preserves project files on the host.

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
