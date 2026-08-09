# Security Model

How watermelon protects your system and its limitations.

## Threat Model

Watermelon is designed to reduce host exposure when malicious packages attempt to:

| Threat | Attack Vector | Protection |
|--------|---------------|------------|
| **Credential theft** | Reading `~/.ssh`, `~/.aws`, `~/.gnupg` | Unmounted host paths are outside the VM |
| **Arbitrary outbound connections** | Sending project data to an unlisted service | `fail` and `silent` block non-allowlisted traffic and resolve only policy names |
| **Persistent access** | Cron jobs, launch agents, shell profiles | Unmounted host system directories are outside the VM |
| **Lateral movement** | Accessing other projects, `.env` files | Only the current project and explicit mounts are exposed |
| **Resource exhaustion** | Fork bombs, disk filling | VM resource limits enforced |

## What Watermelon Does NOT Protect Against

| Limitation | Explanation |
|------------|-------------|
| **Malicious code in the VM** | The VM isolates the host, not the code inside |
| **Attacks on project files** | Your project is mounted read-write |
| **Supply chain attacks on allowed domains** | If you allow npm, malicious npm packages can run |
| **Exfiltration through allowed channels** | Services and DNS names permitted by policy remain reachable |
| **VM escape vulnerabilities** | Relies on Lima/QEMU security |

**Watermelon is a developer safety sandbox, not a jail for untrusted multi-tenant workloads.**

## Configuration Trust Boundary

Treat `.watermelon.toml` as trusted host policy, not as project code that Watermelon first confines inside the sandbox. Review it before the first run and after every change: it selects network enforcement and allow rules, host mounts, container images, provisioning packages, and forwarded ports. Watermelon refuses to use an existing VM when its recorded applied configuration has drifted, but drift detection does not make an attacker-authored configuration safe to run.

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

In `log` and `fail` modes, rate-limited IPv4 network policy events are written to `.watermelon/logs.log` via a VM-side firewall log writer. In `log`, those events were observed and allowed; in `fail`, they were blocked.

### Boundary

Workloads cannot select an arbitrary DNS path: their TCP/UDP port 53 traffic is redirected to Watermelon's managed resolver. In `fail` and `silent`, the resolver answers only names covered by the applicable general or per-process policy. `log` and `ask` deliberately resolve arbitrary names for discovery and prompting. Loopback, established/related response traffic, scoped DHCPv4 lease traffic required by VM control networking, and configured allow rules remain available. The DHCP exception is limited to VM lease control and does not admit arbitrary external UDP. Treat the policy as a way to constrain outbound paths, not as a categorical guarantee against data exfiltration through destinations the policy permits.

Exact policy names are resolved once during trusted VM bootstrap and served as exact records in `fail` and `silent`. Wildcard rules are dynamic and cover subdomains only, not the apex name. Per-process resolvers combine general and process-specific rules.

The firewall currently enforces IPv4. IPv6 is disabled in `fail`, `silent`, and `ask` to prevent a bypass. Discovery mode (`log`) leaves IPv6 enabled, and IPv6 traffic is outside its current policy-event capture.

### Bootstrap Boundary

During VM creation, Watermelon installs its trusted networking prerequisites and pulls configured base images before activating workload policy. Pulling fetches image data but does not run the configured workload. User-requested package provisioning and later tool execution happen after the policy is active. Registry domains therefore do not need workload allow rules solely for base-image pulls.

## Guest Privilege Boundary

Watermelon's system provisioning runs with root privileges; user-configured package installation runs in the configured tool containers after network policy is active. After provisioning completes, Watermelon removes the VM user's general passwordless `sudo` access. Interactive shells and commands submitted through `watermelon exec` therefore run as the ordinary VM user and cannot use `sudo` for arbitrary system changes.

A command listed in `[network.process]` needs elevated setup to enter its dedicated network namespace. Watermelon handles that through a root-owned launcher and authorization limited to that exact per-process helper. The helper and namespace identifiers are internal, non-human-readable implementation details; scripts should invoke the configured command wrapper rather than calling `sudo` or `ip netns` directly.

## Filesystem Isolation

| Path | Access |
|------|--------|
| Project directory | Mounted at `/project` (read-write) |
| Configured mounts | As specified in `[mounts]` |
| Host home directory | **Not accessible** |
| Host system files | **Not accessible** |
| Other projects | **Not accessible** |

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

This blocks new external traffic. Workload DNS still reaches the managed resolver, but with an empty policy the resolver does not resolve arbitrary names. Loopback, established/related responses, and scoped VM-control DHCPv4 lease traffic remain available; the DHCP exception does not allow arbitrary external UDP. The mounted project is still read-write, so do not treat this as a general-purpose hostile-code jail.

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
│   │   Ubuntu 22.04                                      │   │
│   │   ├── iptables (network firewall)                   │   │
│   │   ├── nerdctl (container runtime)                   │   │
│   │   └── /project (Lima mount)                         │   │
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

All user input is validated before being rendered into Lima YAML templates.
