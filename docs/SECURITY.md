# Security Model

How watermelon protects your system and its limitations.

## Threat Model

Watermelon is designed to reduce host exposure when malicious packages attempt to:

| Threat | Attack Vector | Protection |
|--------|---------------|------------|
| **Credential theft** | Reading `~/.ssh`, `~/.aws`, `~/.gnupg` | Unmounted host paths are outside the VM |
| **Arbitrary outbound connections** | Sending project data to an unlisted service | `fail` and `silent` block non-allowlisted traffic and resolve only policy names |
| **Persistent access** | Cron jobs, launch agents, shell profiles | Unmounted host system directories are outside the VM |
| **Lateral movement** | Accessing other projects, `.env` files | At most the resolved project root and explicit mounts are exposed; no-mount mode excludes the project too |
| **Resource exhaustion** | Fork bombs, disk filling | VM resource limits enforced |

## What Watermelon Does NOT Protect Against

| Limitation | Explanation |
|------------|-------------|
| **Malicious code in the VM** | The VM isolates the host, not the code inside |
| **Attacks on project files** | The default project mount is read-write; use `vm.mount_project = false` to exclude it |
| **Supply chain attacks on allowed domains** | If you allow npm, malicious npm packages can run |
| **Exfiltration through allowed channels** | Services and DNS names permitted by policy remain reachable |
| **VM escape vulnerabilities** | Relies on Lima/QEMU security |

**Watermelon is a developer safety sandbox, not a jail for untrusted multi-tenant workloads.**

## Configuration Trust Boundary

Treat `.watermelon.toml` as trusted host policy, not as project code that Watermelon first confines inside the sandbox. Review it before the first run and after every change: it selects the VM name, project/workdir behavior, network enforcement and allow rules, host mounts, container images, provisioning packages and scripts, and forwarded ports. A configured provision script has root authority inside the guest. Watermelon refuses to use an existing VM when its recorded applied configuration has drifted, but drift detection does not make an attacker-authored configuration safe to run.

Configuration parsing is strict. Unknown or misspelled keys are rejected, including fields nested inside mount entries, so a typo cannot silently replace the intended policy with a safety-sensitive default.

Provision scripts also remain host-side policy inputs after creation. Watermelon rereads their exact bytes for status and before policy-checked execution. Keep them present, current-user-owned, readable, and unchanged; a preparation failure causes execution to be refused and may stop a securely bound running VM fail-closed.

## Network Isolation

### How It Works

Watermelon configures iptables inside the VM:

```bash
# Allow specified domains
iptables -A OUTPUT -d registry.npmjs.org -j ACCEPT
iptables -A OUTPUT -d github.com -j ACCEPT

# Redirect workload DNS to the managed resolver
iptables -t nat -A OUTPUT -p tcp --dport 53 -j REDIRECT
iptables -t nat -A OUTPUT -p udp --dport 53 -j REDIRECT

# In fail/silent, the resolver answers only policy names
# In log/ask, it resolves arbitrary names for discovery or prompting

# Allow loopback
iptables -A OUTPUT -o lo -j ACCEPT

# Allow responses to established connections
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Watermelon also allows scoped VM-control DHCPv4 lease traffic.
# This is not a general external UDP exception.

# Block everything else in fail/silent modes
iptables -A OUTPUT -j REJECT
```

### Policy Handling

When traffic does not match an allow rule, behavior depends on `[security].enforcement`:

| Setting | Behavior |
|---------|----------|
| `"fail"` | **Strict default:** block, record a rate-limited policy event, and resolve only policy names |
| `"log"` | **Discovery:** allow, record rate-limited IPv4 policy events, and resolve arbitrary names; IPv6 is not captured |
| `"silent"` | **Strict, quiet:** block without recording a policy event and resolve only policy names |
| `"ask"` | **Interactive:** resolve arbitrary names, prompt for non-allowlisted TCP connections, and reject other non-allowlisted traffic |

In `log` and `fail` modes, the VM-side firewall log writer records rate-limited IPv4 policy events. Legacy path-derived mounted VMs use the project's `.watermelon/logs.log`; registered VMs, including fixed-name and no-mount instances, resolve per-VM host log state so events remain attributable without exposing the project. Use `watermelon logs` rather than assuming one host path. In `log`, events were observed and allowed; in `fail`, they were blocked. `ask` VMs also use registered per-VM state, but report policy decisions through the foreground prompt instead of writing these events.

`ask` needs a foreground Watermelon verdict server. `run --no-shell` cannot provide one and is rejected. One saved verdict port permits only one foreground `run`, `exec`, or `code` prompt controller for a VM at a time. The controller listens only on host loopback. Verdict requests and responses are bound to that VM with a root-only per-instance key, fresh nonces, and HMAC authentication; unauthenticated or malformed traffic is dropped before it can prompt, update policy, or authorize a connection. On Linux, a single terminal-input coordinator routes foreground controlling-terminal input either to the prompt or to the guest, never both; redirected guest stdin is not a verdict channel. Without a foreground controlling terminal, the decision fails closed. A direct SSH connection neither hosts prompts nor acquires Watermelon's usage lease; keep a separate foreground Watermelon controller active if that workload needs prompted access, and expect Watermelon stop or destroy to terminate the connection without waiting for it.

An ask prompt's observed process is informational, not a security scope. **Always Allow and Save** permits the displayed TCP host and port for every process in the current VM runtime and persists the bare host as a global `[network].allow` rule with no process, protocol, or port scope. Managed DNS redirection still applies. Review that broader scope before accepting it. The saved rule becomes provisioned policy only after the current session ends and the VM is destroyed and recreated, which removes VM-local state. If recreation is deferred, the next `run`, `exec`, or `code` detects the stale configuration and stops a securely bound running VM fail-closed before returning an exact recreation instruction pinned to the selected VM name.

### Boundary

Workloads cannot select an arbitrary DNS path: their TCP/UDP port 53 traffic is redirected to Watermelon's managed resolver. In `fail` and `silent`, the resolver answers only names covered by the applicable general or per-process policy. `log` and `ask` deliberately resolve arbitrary names for discovery and prompting. Loopback, established/related response traffic, scoped DHCPv4 lease traffic required by VM control networking, and configured allow rules remain available. The DHCP exception is limited to VM lease control and does not admit arbitrary external UDP. Treat the policy as a way to constrain outbound paths, not as a categorical guarantee against data exfiltration through destinations the policy permits.

Exact policy names are resolved once during trusted VM bootstrap and served as exact records in `fail` and `silent`. Wildcard rules are dynamic and cover subdomains only, not the apex name. Per-process resolvers combine general and process-specific rules.

The firewall currently enforces IPv4. IPv6 is disabled in `fail`, `silent`, and `ask` to prevent a bypass. Discovery mode (`log`) leaves IPv6 enabled, and IPv6 traffic is outside its current policy-event capture.

### Bootstrap Boundary

During VM creation, Watermelon installs its trusted networking prerequisites and pulls configured base images before activating workload policy. Pulling fetches image data but does not run the configured workload. User-requested package provisioning, root provision scripts, and later tool execution happen after the policy is active. Registry domains therefore do not need workload allow rules solely for base-image pulls, while downloads performed by packages or scripts do need matching rules in blocking modes.

## Guest Privilege Boundary

Watermelon's built-in system provisioning and every configured `[provision].scripts` file run with root privileges; user-configured package installation runs in the configured tool containers after network policy is active. After provisioning completes, Watermelon removes the VM user's general passwordless `sudo` access. Interactive shells and commands submitted through `watermelon exec` therefore run as the ordinary VM user and cannot use `sudo` for arbitrary system changes.

A command listed in `[network.process]` needs elevated setup to enter its dedicated network namespace. Watermelon handles that through a root-owned launcher and authorization limited to that exact per-process helper. The helper and namespace identifiers are internal, non-human-readable implementation details; scripts should invoke the configured command wrapper rather than calling `sudo` or `ip netns` directly.

## Filesystem Isolation

| Path | Access |
|------|--------|
| Project directory | Mounted read-write at `/project` by default; absent when `vm.mount_project = false` |
| Watermelon bootstrap/log state | Narrow internal mounts when required; not a project mount |
| Configured mounts | As specified in `[mounts]` |
| Host home directory | **Not accessible** |
| Host system files | **Not accessible** |
| Other projects | **Not accessible** |

An explicit guest workdir is not a host mount and does not expose project data. Watermelon validates its syntax but does not create it; provision the directory with ownership suitable for the ordinary `watermelon` user before using it.

Watermelon assigns that fixed guest user's numeric UID from the invoking process's effective host UID. This preserves owner access on Lima's writable mounts without granting the guest access to any host path that was not mounted. VM creation must therefore run as an unprivileged host user and rejects an effective UID of zero. The UID is part of the applied-policy snapshot, so a host-account UID change makes an existing VM stale and requires recreation instead of silently reusing incompatible mount ownership.

## VM Identity and Management Boundary

A fixed `vm.name` is a project-owned public Lima name, not a shared alias. Watermelon stores a durable identity containing the canonical owning project and an immutable instance ID, rejects unmanaged or cross-project collisions, verifies the read-only guest marker before lifecycle operations, and uses registered ownership state for list attribution and log selection. Normal `--name` commands still require a valid local project configuration. Only the documented fail-closed `stop` and explicit-name `destroy` recovery paths can act through a config error, and only after ownership can be verified.

Interactive `run`, `exec`, foreground IDE commands, and `copy` transfers hold a shared usage lease. `stop` deliberately ignores that lease and shuts down the VM immediately. `destroy` also stops a running VM immediately, terminating sessions or interrupting transfers, then waits for their client processes to detach before deleting identity or VM state. This wait protects cleanup and public-name reuse; it does not let guest work finish before shutdown.

`watermelon copy` is an explicit low-level Lima operation. It validates `vm-name:path` syntax but does not verify Watermelon project ownership or load project configuration. Target only a VM you own. Copy does participate in name/lifecycle coordination and holds its shared usage lease for the transfer, so destroy waits for the client to detach before cleanup or name reuse. Stop and destroy can nevertheless interrupt the transfer by shutting down the VM immediately, potentially leaving a partial destination.

## Best Practices

### Minimal Network Access

Only allow domains you actually need:

```toml
[network]
allow = ["registry.npmjs.org"]

[security]
enforcement = "fail"
```

The catch-all value `"*"` is invalid. Avoid broad wildcard subdomains unless the workload requires them.

### Read-Only Mounts

When mounting sensitive files, use read-only mode:

```toml
[mounts]
"~/.gitconfig" = { target = "/mnt/watermelon/gitconfig", mode = "ro" }
```

Configured targets are restricted to `/mnt/watermelon` and its descendants. This prevents an additional mount from replacing guest system, home, policy, or project paths; tools must be pointed at mounted configuration files explicitly.

### Audit Mode

For inspecting suspicious packages, use the most restrictive built-in policy:

```toml
[network]
allow = []

[security]
enforcement = "fail"
```

This blocks new external traffic. Workload DNS still reaches the managed resolver, but with an empty policy the resolver does not resolve arbitrary names. Loopback, established/related responses, and scoped VM-control DHCPv4 lease traffic remain available; the DHCP exception does not allow arbitrary external UDP. The project remains read-write when the default mount is enabled; no-mount mode reduces that exposure but still does not turn the VM into a general-purpose hostile-code jail.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Host (macOS/Linux)                       │
│                                                             │
│   Watermelon CLI ──────► Lima (limactl)                     │
│                                │                            │
│                                │ manages                    │
│                                ▼                            │
│   ┌─────────────────────────────────────────────────────┐   │
│   │              Lima VM (VZ or QEMU)                   │   │
│   │                                                     │   │
│   │   Ubuntu 22.04 or 24.04                             │   │
│   │   ├── iptables (network firewall)                   │   │
│   │   ├── nerdctl (container runtime)                   │   │
│   │   └── /project (default, optional Lima mount)       │   │
│   │                                                     │   │
│   └─────────────────────────────────────────────────────┘   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## Input Validation

To prevent shell injection attacks, watermelon validates:

- **Domain names**: No shell metacharacters (`;|&$\``)
- **Port numbers**: Must be 1-65535
- **Mount paths**: Sanitized before use
- **VM names and workdirs**: Restricted syntax and clean paths
- **Provision scripts**: Project-relative, no symlink components, current-user-owned regular UTF-8 files with byte limits

All user input is validated before being rendered into Lima YAML templates.
