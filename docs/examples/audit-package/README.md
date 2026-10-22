# Strict Package Inspection Example

Restrictive configuration for inspecting suspicious packages away from unmounted host files.

## Use case

You want to reduce host exposure while examining a potentially malicious npm package.

## Setup

```bash
mkdir audit-workspace
cd audit-workspace
cp /path/to/watermelon/docs/examples/audit-package/.watermelon.toml ./
```

## Download the package archive

In the disposable host `audit-workspace` directory, download an exact tarball
without running a package-manager command. Replace the package name and version:

```bash
curl -fL -o suspicious-package-1.0.0.tgz \
  https://registry.npmjs.org/suspicious-package/-/suspicious-package-1.0.0.tgz
```

Leave the archive unexpanded until you are inside the sandbox. The example's
empty network allowlist prevents downloading it from the inspection VM.

## Inspect in the sandbox

```bash
watermelon run

# Inside sandbox (strict external-network policy)
tar -xzf suspicious-package-1.0.0.tgz
cd package

# Examine the code
cat package.json
grep -R -n -E "preinstall|postinstall" --include="*.json" --include="*.js" .

# Check for suspicious patterns
grep -r "child_process" .
grep -r "fs.readFile.*ssh\|aws\|credentials" .
grep -r "http\|https\|fetch\|axios" .
grep -r "eval\|Function(" .
```

Searches exit with status 1 when they find no matches; that is not a sandbox
failure. Open files referenced by the package's actual lifecycle scripts rather
than assuming a path such as `scripts/postinstall.js` exists. These searches
are starting points for inspection, not proof that a package is safe.

## What this protects against

- Unmounted SSH keys, cloud credentials, and other host files are inaccessible
- New non-allowlisted external traffic is rejected
- Unmounted host persistence locations are outside the VM; the package can still modify the mounted project
- `fail` mode records rate-limited policy events for rejected traffic

Workload DNS still reaches Watermelon's managed resolver, but with this empty strict policy it does not resolve arbitrary names. Loopback, established response traffic, and scoped DHCPv4 lease traffic required by VM control networking remain available; the DHCP exception is not arbitrary external UDP access. The project directory is mounted read-write, and VM escape vulnerabilities are outside Watermelon's guarantees. Do not expose real secrets while inspecting hostile code.

## Checking logs

If you see failures:

```bash
# On host
watermelon logs
```

This shows rate-limited network policy events for destinations the package tried to access.
