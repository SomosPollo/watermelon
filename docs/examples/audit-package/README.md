# Security Audit Example

Maximum isolation configuration for safely inspecting suspicious packages.

## Use case

You want to examine a potentially malicious npm package without risking your system.

## Setup

```bash
mkdir audit-workspace
cd audit-workspace
cp /path/to/watermelon/docs/examples/audit-package/.watermelon.toml ./
```

## Download the package offline first

On your host (or a separate sandboxed environment with network):

```bash
# Download package tarball without executing
npm pack suspicious-package
# or
curl -O https://registry.npmjs.org/suspicious-package/-/suspicious-package-1.0.0.tgz
```

## Inspect in the sandbox

```bash
watermelon run

# Inside sandbox (no network)
tar -xzf suspicious-package-1.0.0.tgz
cd package

# Examine the code
cat package.json
find . -name "*.js" | xargs grep -l "postinstall\|preinstall"
cat scripts/postinstall.js

# Check for suspicious patterns
grep -r "child_process" .
grep -r "fs.readFile.*ssh\|aws\|credentials" .
grep -r "http\|https\|fetch\|axios" .
grep -r "eval\|Function(" .
```

## What this protects against

- Package can't read your SSH keys, AWS credentials, etc.
- Package can't exfiltrate data (no network)
- Package can't install persistence mechanisms
- Any violation immediately fails and alerts you

## Checking logs

If you see failures:

```bash
# On host
watermelon logs
```

This shows what the package tried to access.
