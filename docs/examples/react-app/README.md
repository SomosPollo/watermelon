# React/Vite Example

Sandbox configuration for React development with Vite.

## Setup

```bash
cd your-react-project
cp /path/to/watermelon/docs/examples/react-app/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
npm install
npm run dev
# Visit http://localhost:5173 on your host
```

## What's protected

- Your `~/.ssh` keys are inaccessible to npm postinstall scripts
- Your `~/.aws` credentials can't be read by malicious packages
- New external connections outside the allowlist are blocked; managed DNS, loopback, established responses, and scoped VM-control DHCPv4 remain available, but DHCP is not arbitrary external UDP access
- Unmounted host launch-agent and cron locations are outside the VM; the package can still modify the project

## Customization

If your project uses additional CDNs or APIs, add them to `network.allow`:

```toml
[network]
allow = [
    "registry.npmjs.org",
    "api.your-backend.com",  # Add your API
]
```
