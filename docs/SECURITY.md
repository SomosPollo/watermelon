# Security Model

How watermelon protects your system and its limitations.

## Threat Model

Watermelon protects against malicious packages that attempt to:

| Threat | Attack Vector | Protection |
|--------|---------------|------------|
| **Credential theft** | Reading `~/.ssh`, `~/.aws`, `~/.gnupg` | Host filesystem is not mounted |
| **Data exfiltration** | Sending data to attacker servers | Network allowlist blocks unknown domains |
| **Persistent access** | Cron jobs, launch agents, shell profiles | No access to host system directories |
| **Lateral movement** | Accessing other projects, `.env` files | Only current project is mounted |
| **Resource exhaustion** | Fork bombs, disk filling | VM resource limits enforced |

## What Watermelon Does NOT Protect Against

| Limitation | Explanation |
|------------|-------------|
| **Malicious code in the VM** | The VM isolates the host, not the code inside |
| **Attacks on project files** | Your project is mounted read-write |
| **Supply chain attacks on allowed domains** | If you allow npm, malicious npm packages can run |
| **VM escape vulnerabilities** | Relies on Lima/QEMU security |

**Watermelon is a developer safety sandbox, not a jail for untrusted multi-tenant workloads.**

## Network Isolation

### How It Works

Watermelon configures iptables inside the VM:

```bash
# Allow specified domains
iptables -A OUTPUT -d registry.npmjs.org -j ACCEPT
iptables -A OUTPUT -d github.com -j ACCEPT

# Allow DNS resolution
iptables -A OUTPUT -p tcp --dport 53 -j ACCEPT
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT

# Allow responses to established connections
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Block everything else
iptables -A OUTPUT -j REJECT
```

### Violation Handling

When a blocked request occurs, behavior depends on `[security].enforcement`:

| Setting | Behavior |
|---------|----------|
| `"log"` | Log and allow (useful for discovery) |
| `"fail"` | Block and log error |
| `"silent"` | Block silently |

Violations are logged to `.watermelon/logs.log`.

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
# Good: specific domains
[network]
allow = ["registry.npmjs.org"]

# Bad: overly permissive
[network]
allow = ["*"]  # This doesn't work, but if it did...
```

### Read-Only Mounts

When mounting sensitive files, use read-only mode:

```toml
[mounts]
"~/.gitconfig" = { target = "/home/dev/.gitconfig", mode = "ro" }
```

### Audit Mode

For inspecting suspicious packages, use maximum restriction:

```toml
[network]
allow = []

[security]
enforcement = "fail"
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Host (macOS)                           │
│                                                             │
│   Watermelon CLI ──────► Lima (limactl)                     │
│                                │                            │
│                                │ manages                    │
│                                ▼                            │
│   ┌─────────────────────────────────────────────────────┐   │
│   │                    QEMU VM                          │   │
│   │                                                     │   │
│   │   Ubuntu 22.04                                      │   │
│   │   ├── iptables (network firewall)                   │   │
│   │   ├── nerdctl (container runtime)                   │   │
│   │   └── /project (virtiofs mount)                     │   │
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
