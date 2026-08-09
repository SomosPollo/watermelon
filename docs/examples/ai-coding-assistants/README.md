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
