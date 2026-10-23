# Security model

Watermelon reduces host exposure by running development commands inside a
Lima-managed Linux VM. Review what you expose before running code.

| Boundary | What it means |
|----------|---------------|
| Files | Unmounted host credentials, home files, and other projects stay outside the VM |
| Network | Default `fail` blocks destinations outside the allowlist |
| Privileges | Workloads run as an ordinary guest user, without general passwordless `sudo` |
| Resources | VM CPU, memory, and system disk limits apply; writable host mounts still consume host storage |

## What remains exposed

The default local project mount is read-write. Code can read secrets, delete
files, or introduce malicious changes there. Read-only mounts still expose
contents. Allowed services and DNS names can carry data out. Allowing a package
registry does not make its packages safe.

Watermelon relies on Lima and the VM backend (Apple VZ or QEMU); it does not
protect against VM escapes. It is a developer sandbox, not a hostile
multi-tenant execution service. Rebuilding retains managed source and any
malicious files already on that disk.

## Configuration trust boundary

Treat the host `.watermelon.toml` as trusted policy. It controls mounts, network
access, images, provisioning, and ports. Review it before first use and after
changes. Drift detection refuses an out-of-date VM; it does not make an
attacker-authored configuration safe.

For managed `clone` workspaces, host settings stay separate from source at
`/workspace/project`. The repository's own config cannot become host policy.
Cloning accepts only credential-free public GitHub HTTPS URLs and uses a
sanitized Git environment without inherited hooks, templates, credentials,
automatic submodules, or LFS filters.

Manifest inspection is bounded and does not execute project code on the host.
Dependency setup runs after approval and network enforcement in rootless guest
containers. Maven, Gradle, MSBuild, Gemfile, Composer, CMake, and package hooks
run there. Private host approval records stay outside guest mounts. Setup
refuses settings that change after inspection/approval.

Automatic runtimes use official toolchain images and fixed assembly commands
without project/host mounts. Go disables direct VCS fallback and automatic
toolchain downloads; Rust uses its bundled toolchain and sparse registry.
Corepack integrity pins are retained; uv cannot fetch an undeclared interpreter;
Composer platform and TLS checks remain enabled.

Configured provision scripts have root authority inside the guest. They remain
host policy inputs: exact bytes are reread for status and execution. Invalid
or missing scripts prevent verification and may stop a bound VM. See
[provision requirements](CONFIG_SPEC.md#provision).

## Network isolation

An IPv4 firewall and managed resolver enforce policy inside the guest. Workload
TCP/UDP DNS is redirected to that resolver. `fail`/`silent` resolve only policy
names; `log`/`ask` resolve arbitrary names. Exact domain addresses are captured
at bootstrap; wildcard subdomains resolve dynamically and exclude the apex.
Rebuild to refresh exact-name addresses.

Loopback, established/related responses, scoped VM-control DHCPv4, and configured
allow rules remain available. An empty strict allowlist blocks new external
connections; the DHCP exception is not general external UDP access.
IPv6 is disabled in `fail`, `silent`, and `ask`. In permissive `log` mode,
IPv6 remains available and is outside event capture.

Use the [enforcement table](CONFIG_SPEC.md#security) to choose behavior.
`logs` shows rate-limited IPv4 events, not a complete traffic audit. The writer
truncates its verified log inode at 16 MiB; the CLI reads a bounded tail.

### Interactive prompts

`ask` needs one foreground controller per VM. Requests use a per-instance
root-only key, fresh nonces, and HMAC authentication. The host server listens
on loopback; malformed/unauthenticated requests cannot authorize access.
Linux verdict input comes from the controlling terminal, separately from guest
stdin; no foreground terminal means requests block. macOS uses native dialogs.

Prompt process and hostname labels are informational. Hostnames come from
recent intercepted DNS responses, which are not correlated with outstanding
queries. CNAME attribution expires at its shortest bounded TTL; missing or
ambiguous attribution shows the literal IP. Labels are not proof of the
workload's intended hostname.

Runtime verdicts are cached by literal IPv4 address and TCP port. Another
hostname or direct-IP request to the same endpoint reuses its verdict; the
same hostname at a new IP prompts again.

**Always Allow and Save** permits that endpoint for every process now, then
persists the displayed bare hostname (or IP) globally without process, port,
or protocol scope. Review this broader rule. After the session, use the exact
VM-pinned recreation command printed by Watermelon. Deferring it leaves policy
stale; the next checked execution refuses it and can stop the verified VM.

### Bootstrap boundary

Trusted networking prerequisites and base-image pulls happen before workload
enforcement. Image pulls fetch data without running the configured workload.
User packages, root provision scripts, and later commands run after enforcement.
Allow registry domains only if the workload needs them, not solely for image
pulls. Project install hooks are not retried automatically after failure.

## Guest privilege boundary

Built-in provisioning and configured scripts run as root. Afterward, shells
and `exec` use the ordinary `watermelon` user. Per-process rules use root-owned
launchers with narrow authorization to enter network namespaces; invoke the
configured wrappers rather than internal helpers. See [process rules](CONFIG_SPEC.md#networkprocess).

## Filesystem isolation

| Workflow/path | Exposure |
|---------------|----------|
| Local project | Entire root read-write at `/project` by default |
| `vm.mount_project = false` | No host project mount |
| Managed clone | Source and logs on retained guest disk; no writable host mounts |
| Extra mounts | Explicit paths under `/mnt/watermelon`, read-only by default |
| Internal state | Read-only bootstrap; other VM modes may also use narrow writable log state |

Tool containers do not automatically receive every guest mount. See
[mount rules](CONFIG_SPEC.md#mounts). Explicit workdirs select guest directories;
they do not expose new host paths.

The guest UID matches the invoking host user's effective UID for mount
ownership. Root host execution is rejected; a changed UID requires VM
recreation. Managed/automatic VMs disable SSH-agent/X11 forwarding and extra
host SSH keys, and omit host proxy variables.

## VM identity and management boundary

VM names belong to their canonical creating project. Durable ownership and
read-only guest identity prevent unmanaged or cross-project adoption. Normal
`--name` use still requires valid settings. Recovery through config errors
requires verified ownership; see [selection and recovery](COMMANDS.md#project-and-vm-selection).

Sessions and transfers hold usage leases. Stop interrupts immediately; destroy
stops immediately, then waits for clients to detach before deleting state or
reusing a name. Manual SSH holds no lease and starts no prompt controller.

`copy` is a low-level exception: it takes an explicit VM operand and does not
check Watermelon project ownership. Target only a VM you own. Stop/destroy
may interrupt it and leave partial files.

Prefer managed clones for unfamiliar public code, minimal allowlists, and
mounts containing only needed files. For package inspection, use the
[disposable inspection example](examples/audit-package/README.md).
