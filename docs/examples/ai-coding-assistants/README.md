# AI Coding Assistants Example

Sandbox configuration for using AI coding assistants (Claude Code, Codex, Aider, etc.) while keeping build tools isolated.

## The Problem

AI coding assistants need API access to work, but you want to sandbox `npm install` and other build tools. With per-process network policies, you can allow Claude to reach Anthropic's API while keeping everything else locked down.

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

This runs `npm install -g` and `pip install` automatically when the VM is first created.

### Custom Setup Scripts

For more complex provisioning — configuring dotfiles, installing system packages, or setting up credentials — use `scripts`:

```toml
[provision]
npm = ["@anthropic-ai/claude-code"]
pip = ["aider-chat"]
scripts = ["./vm/setup.sh"]  # Runs as root during provisioning
```

Example `./vm/setup.sh`:
```bash
#!/bin/bash
set -e
# Install additional system tools
apt-get install -y ripgrep fd-find
# Configure git
git config --system core.editor vim
```

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
1. A wrapper script routes it through a dedicated network namespace
2. That namespace allows both general domains AND Claude-specific domains
3. Build tools like `npm` only get the general domains

## Full Example Config

```toml
[vm]
image = "ubuntu-24.04"

[network]
allow = [
    "registry.npmjs.org",
    "pypi.org",
    "files.pythonhosted.org",
    "github.com",
    "*.githubusercontent.com",
]

[network.process]
claude = ["api.anthropic.com", "*.anthropic.com"]
codex  = ["api.openai.com"]
aider  = ["api.anthropic.com", "api.openai.com"]

[tools]
"node:20-slim"    = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[provision]
npm = ["@anthropic-ai/claude-code"]
pip = ["aider-chat"]
scripts = ["./vm/setup.sh"]

[resources]
memory = "4GB"
cpus = 2
disk = "20GB"

[security]
enforcement = "log"

[ide]
command = "cursor"
```

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
