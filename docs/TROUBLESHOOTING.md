# Troubleshooting

[Documentation](README.md) · [Command reference](COMMANDS.md)

Run these checks on the host, from the directory containing your project's
`.watermelon.toml` or a descendant:

```bash
watermelon doctor
watermelon status
watermelon logs
```

`doctor` also works without a project. Managed workspace logs need a running
VM; `logs` does not start it. In the default `fail` mode, events show blocked
traffic. An empty log does not prove that a command has network access.

| Symptom | Start here |
|---------|------------|
| Installation or host readiness fails | [Installation issues](#installation-issues) |
| VM fails to start, runs out of disk, or has an old policy | [VM issues](#vm-issues) |
| Settings cannot be found or a tool is unavailable | [Command issues](#command-issues) |
| Dependency downloads or external requests fail | [Network issues](#network-issues) |
| App runs but cannot be reached from the host | [Port issues](#port-issues) |
| File access or resource use is slow | [Performance issues](#performance-issues) |

Before rebuilding or destroying a VM, preserve needed VM-local files. Mounted
host project files and managed workspace disks survive normal recreation.
`destroy --delete-workspace` permanently deletes the managed source disk too.

## Installation Issues

### curl not found

The release installer requires `curl` before it can download Watermelon.

```bash
# macOS (curl is normally included)
brew install curl

# Debian/Ubuntu
sudo apt-get install curl
```

### Lima not found

**Error:** `limactl: command not found`

**Solution:**
```bash
# macOS
brew install lima

# Linux
# Install Lima and its QEMU backend with your distro package manager or
# an upstream Lima package.
```

Watermelon requires a stable Lima 2.0.0 or newer release. Upgrade Lima when
`watermelon doctor` reports an older or malformed version. On macOS,
Watermelon uses VZ and requires macOS 13 or newer. On Linux, it uses QEMU and
requires `qemu-system-aarch64` or `qemu-system-x86_64` to match Lima's reported
host architecture. If Lima uses a non-default executable, set
`QEMU_SYSTEM_AARCH64` or `QEMU_SYSTEM_X86_64` using Lima's shell-word syntax
before running the installer or doctor. The executable is the first word; put
quotes inside the value when its path contains spaces. Unavailable KVM
acceleration is reported as a warning because Lima can fall back to slower TCG
emulation.

The installer and `watermelon doctor` read the macOS product version with
`sw_vers -productVersion`. If that command fails or reports malformed output,
run it directly to diagnose the host, then upgrade to macOS 13 or newer before
retrying. Unsupported macOS versions are rejected before installer downloads.

The installer also requires an OpenSSH client before downloading Watermelon.
macOS normally provides `/usr/bin/ssh`; on Linux, install your distribution's
OpenSSH client package (commonly `openssh-client`) if `ssh` is absent.

Run the read-only preflight from any directory after installing or upgrading:

```bash
watermelon doctor
watermelon doctor --json  # stable output for support tooling and automation
```

### Go not installed

**Error:** `go: command not found`

Go is required only when installing with `go install` or building Watermelon
from source. The shell installer uses prebuilt release binaries for both the
host CLI and the Linux `watermelon-nfqd` sidecar.

**Solution:**
```bash
# macOS
brew install go
```

Or download from [go.dev](https://go.dev/dl/).

### Go-installed `ask` mode cannot obtain the network interceptor

A tagged `go install` build downloads its exact matching Linux
`watermelon-nfqd` sidecar for each new or recreated `ask`-mode VM. If that
download fails, check access to GitHub and retry. You can instead install
Watermelon with the shell installer, place the matching
`watermelon-nfqd-linux-<Lima-arch>` (or `watermelon-nfqd`) binary beside the CLI,
or set `WATERMELON_NFQD_BINARY` to a matching regular binary. Unreleased,
locally modified, and custom builds require one of those explicit sidecar
paths. The `fail`, `silent`, and `log` modes do not use this sidecar. The
source-build command is in the
[installation guide](INSTALLATION.md#the-ask-mode-network-interceptor).

---

## VM Issues

### VM won't start

**Check Watermelon and Lima status:**
```bash
watermelon status
watermelon list
limactl list
```

If Watermelon reports `sandbox provisioning is not complete`, Lima's ready
signal did not prove that Watermelon's provisioning stages succeeded. Watermelon stops the
owned VM rather than allowing commands into that partial environment. Check
Ubuntu mirror access, host DNS/VPN settings, and hostname overrides in Lima's
`_config/default.yaml`; on VZ, an override can also disrupt unrelated lookups.
Correct the network settings before recreating the VM. Normal recreation
retains managed workspace disks.

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
watermelon rebuild
```

### Existing VM policy is unverified

VMs created before versioned policy records have only a legacy project digest, which does not identify the recorded enforcement mode. For safety, `watermelon run`, `exec`, and `code` require a one-time recreation:

```bash
watermelon destroy --force && watermelon run
```

This removes VM-local state but preserves project files on the host. `watermelon status` compares the configured policy with the host-side record written after VM creation; it does not inspect the live firewall.

---

## Command Issues

### Configuration is missing or the wrong project is selected

Run `watermelon status` and check its `Project:` path. Watermelon searches for
the nearest `.watermelon.toml` from the current directory, stopping at Git,
filesystem, or untrusted-directory boundaries. A nearer invalid configuration
returns an error; it does not fall back to a parent configuration.

For a new local project, run `watermelon init` from the intended root. For a
managed clone, run commands from the host settings directory created by
`clone`; the source checkout is inside the guest. Do not run `init` inside a
subdirectory just to reconnect to an existing sandbox: it creates a separate
configuration. See [project discovery](CONFIG_SPEC.md#file-location).

### "Command not found" inside VM

First check whether the project uses automatic `[setup]` or manual `[tools]`.
For automatic setup, run `watermelon up` to check the detected toolchains and
dependencies. Use `watermelon up --refresh` if dependency directories were
removed but manifests are unchanged. Do not add global `[provision]` package
lists to an automatic profile; those settings cannot be combined.

For manual configurations, the tool may be missing from `[tools]`, or an ad-hoc
package installation may not have created a guest command wrapper.

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
2. The guest program is waiting for input; use that program's documented noninteractive option for automation
3. An `ask`-mode request has no foreground prompt controller; keep `watermelon run` open for that session

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

Apply reviewed network changes with `watermelon rebuild`. This removes
VM-local system state while retaining mounted host files and managed workspace
disks. For a manual `ask` configuration, use the exact recreation command
printed by Watermelon, then keep `watermelon run` open as the sole foreground
prompt controller. Automatic `up` dependency setup does not support `ask`.

### Common domains by package manager

Add required names to `[network].allow`, preserving the project's other rules:

| Package manager | Default public download destinations |
|-----------------|--------------------------------------|
| npm | `registry.npmjs.org` |
| pip | `pypi.org`, `files.pythonhosted.org` |
| Cargo, using the sparse registry | `index.crates.io`, `static.crates.io` |
| Go, using the public proxy | `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` |

These are starting points for standard registries. Git dependencies, alternate
registries, and install hooks may need additional reviewed destinations.

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

Check that the app's listening port is forwarded in the host settings:
```toml
[ports]
forward = [3000]
```

If you changed the port list, apply it with `watermelon rebuild`. Keep the app
running, then use another host terminal to check it inside the VM:

```bash
watermelon exec -- curl -I http://127.0.0.1:3000
```

In `ask` mode, run `curl` from the existing guest shell instead; a second
`exec` cannot start while that VM's prompt controller is active.

Then check the forwarded port from the host:

```bash
curl -I http://127.0.0.1:3000
```

If the guest check fails, check the app's logs, listening address, and port.
If only the host check fails, review `[ports].forward` and rebuild after any
change. See [run a development server](WORKSPACES.md#run-a-development-server).

### Port already in use

Stop the host process using the port, or choose a different port for both the
app and its forward:

```toml
[ports]
forward = [3001]  # Use alternative port
```

Configure the app to listen on `3001`, then rebuild and restart it. Forwarding
`3001` does not remap an app that still listens on `3000`.

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

1. Run the host preflight: `watermelon doctor`
2. Check logs: `watermelon logs`
3. Run `watermelon status` and note its `Project:` path, or the selected config
   path in an error
4. Review that project's `.watermelon.toml`

If issues persist, [open an issue](https://github.com/SomosPollo/watermelon/issues) with:

- Your `.watermelon.toml`
- Output of `watermelon doctor`
- Output of `watermelon status`
- The error message

Remove credentials, tokens, and other secrets from settings and output before sharing.
