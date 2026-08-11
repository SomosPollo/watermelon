# AI Coding Assistants Example

Sandbox configuration for using AI coding assistants (Claude Code, Codex, Aider, etc.) while keeping build tools isolated.

## The Problem

AI coding assistants need API access to work, while build tools usually need only package registries. With the example's strict `fail` policy, per-process rules can give Claude access to Anthropic while blocking other new external connections that do not match an allow rule.

## Setup

```bash
cd your-project
cp /path/to/watermelon/docs/examples/ai-coding-assistants/.watermelon.toml ./
watermelon run
```

## How It Works

### Automatic Installation

The `[provision]` section installs AI tools during VM provisioning:

```toml
[provision]
npm = ["@anthropic-ai/claude-code", "@openai/codex"]
pip = ["aider-chat"]
```

This runs `npm install -g` and `pip install` during initial VM creation or recreation, then creates command wrappers for the provisioned CLIs.

### Custom Setup Scripts

For setup that is not covered by the package fields, add an idempotent host script:

```toml
[provision]
npm = ["@anthropic-ai/claude-code", "@openai/codex"]
pip = ["aider-chat"]
scripts = ["./vm/setup.sh"]
```

```bash
#!/bin/sh
set -eu

# This remains safe when Lima runs provisioning again.
install -d -m 0755 /opt/assistant-work
git config --system core.editor vim
```

Scripts are embedded when the VM is created and run as root after Watermelon's network policy is active. Review them as trusted configuration, make them idempotent, and recreate the VM after changes. Keep the configured host files present and readable while the VM exists: Watermelon rereads their exact bytes for status and before policy-checked execution, and refuses or fail-closed stops a VM when it cannot verify them. Script paths must be project-relative, contain no `..` component, and contain no symlink component. Watermelon accepts only current-user-owned regular files, requires UTF-8 without NUL bytes, and limits scripts to 1 MiB each and 4 MiB total. Editing a script at the same path makes the applied configuration stale. Any network downloads performed by a script still need matching `[network].allow` entries under this example's strict `fail` policy.

### Per-Process Network Access

The `[network.process]` section gives specific processes additional network access:

```toml
[network]
# General rules - apply to npm, pip, etc.
allow = ["registry.npmjs.org", "pypi.org"]

[network.process]
# Claude Code gets API access
claude = ["api.anthropic.com", "*.anthropic.com"]

# Other AI tools
codex = ["api.openai.com"]
aider = ["api.anthropic.com", "api.openai.com"]
```

When you run `claude` inside the sandbox:

1. Its command wrapper invokes Watermelon's narrowly authorized, root-owned launcher
2. The launcher routes it through a dedicated network namespace
3. That namespace allows both general domains AND Claude-specific domains
4. Build tools like `npm` get only the general allowed external destinations

Workload DNS is redirected to a managed resolver. With this example's `fail` policy, it answers only names in the applicable general or process-specific rules. Loopback, established response traffic, and scoped VM-control DHCPv4 lease traffic remain available; the DHCP exception is not arbitrary external UDP access.

## Inside the Sandbox

```bash
# Install dependencies (new external connections restricted to the general allowlist)
npm install

# Use Claude Code (has API access)
claude

# Both work with different external-network permissions
```

## Wildcard Domains

Wildcards like `*.anthropic.com` are supported. The managed resolver dynamically allows resolved subdomain IPs. A wildcard does not include its apex, so add `anthropic.com` separately if the tool needs it.

## Verifying Isolation

```bash
# Use Claude normally; its generated wrapper applies the process-specific policy
claude

# From the ordinary shell, this should fail under the strict general policy
curl -I https://api.anthropic.com
```

Do not call `sudo ip netns exec` directly. Watermelon removes the VM user's general passwordless `sudo` access after provisioning, and namespace/helper names are internal rather than stable `watermelon-<process>` names. The configured command wrapper is the supported launcher; ordinary shell and `watermelon exec` commands remain unprivileged.
