# AI Coding Assistants Example

Sandbox configuration for using AI coding assistants (Claude Code, Codex, Aider, etc.) while keeping build tools isolated.

## The Problem

AI coding assistants need API access to work, but you want to sandbox `npm install` and other build tools. With per-process network policies, you can allow Claude to reach Anthropic's API while keeping everything else locked down.

## Setup

```bash
cd your-project
cp .watermelon.toml ./
watermelon run
```

## How It Works

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
1. A wrapper script routes it through a dedicated network namespace
2. That namespace allows both general domains AND Claude-specific domains
3. Build tools like `npm` only get the general domains

## Inside the Sandbox

```bash
# Install dependencies (restricted to registries only)
npm install

# Use Claude Code (has API access)
claude

# Both work, but with different network permissions
```

## Wildcard Domains

Wildcards like `*.anthropic.com` are supported. The sandbox uses dnsmasq to dynamically allow resolved IPs.

## Verifying Isolation

```bash
# This should work (Claude has API access)
ip netns exec watermelon-claude curl -I https://api.anthropic.com

# This should fail (npm doesn't have API access)
curl -I https://api.anthropic.com
```
