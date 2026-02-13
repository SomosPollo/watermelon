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
- Network requests to unexpected domains are blocked
- The malicious package can't install launch agents or cron jobs on your host

## Customization

If your project uses additional CDNs or APIs, add them to `network.allow`:

```toml
[network]
allow = [
    "registry.npmjs.org",
    "api.your-backend.com",  # Add your API
]
```
