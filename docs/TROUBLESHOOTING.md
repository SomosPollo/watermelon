# Troubleshooting

Common issues and solutions.

## Installation Issues

### Lima not found

**Error:** `limactl: command not found`

**Solution:**
```bash
brew install lima
```

### Go not installed

**Error:** `go: command not found`

**Solution:**
```bash
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

---

## Command Issues

### "Command not found" inside VM

**Cause:** Tool not configured in `.watermelon.toml`

**Solution:** Add the tool:
```toml
[tools]
"node:20-slim" = ["node", "npm", "npx"]
```

### Command hangs

**Possible causes:**
1. Network request to blocked domain
2. Waiting for input (use `watermelon exec` for non-interactive)

**Check logs:**
```bash
watermelon logs
```

---

## Network Issues

### Package installation fails

**Check what's being blocked:**
```bash
watermelon logs
```

**Add the domain to config:**
```toml
[network]
allow = [
    "registry.npmjs.org",
    "blocked-domain.com",  # Add this
]
```

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
