# pnpm Example

Sandbox configuration for JavaScript/TypeScript projects using `pnpm`.

## Setup

```bash
cd your-project
cp /path/to/watermelon/docs/examples/pnpm/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
pnpm install
pnpm dev
```

Visit your app from the host using the forwarded port (for example `http://localhost:3000` or `http://localhost:5173`).

## Optional: preinstall global CLIs

If you want common CLIs available every time the VM is created:

```toml
[provision]
npm = ["typescript", "eslint"]
```

This uses `npm install -g ...` during provisioning.

## Notes

- The example provisions `pnpm` globally during VM creation (`[provision].npm = ["pnpm"]`), so you can run `pnpm` directly.
- The allowlist includes Docker Hub domains so Watermelon can pull the base `node` image.

## Troubleshooting network allowlist

If installs fail, check blocked domains and add only what you need:

```bash
watermelon logs
```
