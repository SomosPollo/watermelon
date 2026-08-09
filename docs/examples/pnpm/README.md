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

This uses `npm install -g ...` during provisioning and creates the command wrappers when the VM is built. Prefer this configuration over an ad-hoc global install: a newly installed executable is not automatically exposed in the guest command path.

## Notes

- The example provisions `pnpm` globally during VM creation (`[provision].npm = ["pnpm"]`), so you can run `pnpm` directly.
- Watermelon pulls the base `node` image during trusted bootstrap, so Docker Hub does not need to be in the workload allowlist.

## Troubleshooting network allowlist

If installs fail, inspect network policy events and add only trusted destinations the project needs:

```bash
watermelon logs
```
